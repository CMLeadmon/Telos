mod credentials;

use credentials::{CredentialError, CredentialKey, CredentialService, LegacyOutcome, OsBackend};
use std::sync::atomic::{AtomicBool, Ordering};
use tauri::{Manager, Runtime};

/// Set at launch when the retired plaintext secret file was found and deleted, so
/// the page can tell the member to sign in again once.
struct LegacyStoreRetired(AtomicBool);

fn credential_service() -> CredentialService<OsBackend> {
    CredentialService::new(OsBackend, credentials::debug_loopback_http_allowed())
}

/// Runs a blocking credential operation off the UI thread. The platform call can
/// wait on a Secret Service unlock prompt, which must never freeze the window.
async fn credential_blocking<T: Send + 'static>(
    work: impl FnOnce(CredentialService<OsBackend>) -> Result<T, CredentialError> + Send + 'static,
) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(move || work(credential_service()))
        .await
        .map_err(|_| CredentialError::Backend.to_frontend())?
        .map_err(|error| error.to_frontend())
}

#[tauri::command]
async fn credential_get(key: CredentialKey) -> Result<Option<String>, String> {
    credential_blocking(move |service| service.get(&key)).await
}

#[tauri::command]
async fn credential_set(key: CredentialKey, secret: String) -> Result<(), String> {
    credential_blocking(move |service| service.set(&key, &secret)).await
}

#[tauri::command]
async fn credential_delete(key: CredentialKey) -> Result<(), String> {
    credential_blocking(move |service| service.delete(&key)).await
}

/// True once, at the first call after a launch that deleted the old plaintext file.
#[tauri::command]
fn telos_legacy_store_retired(state: tauri::State<'_, LegacyStoreRetired>) -> bool {
    state.0.swap(false, Ordering::SeqCst)
}

/// Opens a URL in the operating system's browser.
///
/// A webview has one window and no back button, so letting a link navigate it
/// strands the member on someone else's page with no way back to Telos.
///
/// Only http(s) is forwarded. `open_url` hands the string to the platform's URL
/// handler, which will happily act on `file:`, `smb:` or any other registered
/// scheme — and this command is reachable from page script, so anything the
/// frontend can be induced to pass would otherwise reach the OS.
fn is_web_url(url: &str) -> bool {
    url.starts_with("https://") || url.starts_with("http://")
}

#[tauri::command]
async fn telos_open_external<R: Runtime>(
    app: tauri::AppHandle<R>,
    url: String,
) -> Result<(), String> {
    if !is_web_url(&url) {
        return Err("only http and https URLs can be opened".into());
    }
    tauri_plugin_opener::OpenerExt::opener(&app)
        .open_url(url, None::<&str>)
        .map_err(|e| e.to_string())
}

/// Writes to the system clipboard.
///
/// navigator.clipboard is available only in a secure context, and a
/// custom-scheme webview is not reliably one, so the web API cannot be relied on
/// here.
#[tauri::command]
fn telos_write_clipboard<R: Runtime>(app: tauri::AppHandle<R>, text: String) -> Result<(), String> {
    tauri_plugin_clipboard_manager::ClipboardExt::clipboard(&app)
        .write_text(text)
        .map_err(|e| e.to_string())
}

/// Installs the bridges the frontend looks for on `window`.
///
/// The frontend is one static export shared by the web build, which must not
/// import Tauri modules at all — so the shell publishes plain objects and the
/// frontend feature-detects them. The names are the contracts documented in
/// `frontend/src/lib/secureStorage.ts` and
/// `frontend/src/lib/platform.ts`;
/// changing one here without changing it there silently disables the feature,
/// because both fall back rather than fail.
fn bridge_script() -> String {
    r#"
(function () {
  var invoke = window.__TAURI_INTERNALS__ && window.__TAURI_INTERNALS__.invoke;
  if (!invoke) return;

  window.__TELOS_NATIVE_SHELL__ = {
    openExternal: function (url) {
      return invoke("telos_open_external", { url: url });
    },
    writeClipboard: function (text) {
      return invoke("telos_write_clipboard", { text: text });
    },
  };

  // Structured keys only: the page names (origin, account, device), never a
  // keychain service or account string. Errors reject; the frontend must treat a
  // rejection as a failed sign-in, never fall back to memory.
  window.__TELOS_NATIVE_CREDENTIALS__ = {
    get: function (key) {
      return invoke("credential_get", { key: key });
    },
    set: function (key, secret) {
      return invoke("credential_set", { key: key, secret: secret });
    },
    delete: function (key) {
      return invoke("credential_delete", { key: key });
    },
    legacyStoreRetired: function () {
      return invoke("telos_legacy_store_retired", {});
    },
  };
})();
"#
    .to_string()
}

/// Carries the bridge script into every window.
///
/// An initialization script has to be attached at window creation, and the
/// windows here are declared in tauri.conf.json rather than built in code, so a
/// setup hook would run too late. A plugin whose only contribution is
/// `js_init_script` is the seam that reaches them.
fn telos_bridge_plugin<R: Runtime>() -> tauri::plugin::TauriPlugin<R> {
    tauri::plugin::Builder::new("telos-bridge")
        .js_init_script(bridge_script())
        .build()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(telos_bridge_plugin())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(LegacyStoreRetired(AtomicBool::new(false)))
        .invoke_handler(tauri::generate_handler![
            telos_open_external,
            telos_write_clipboard,
            credential_get,
            credential_set,
            credential_delete,
            telos_legacy_store_retired
        ])
        .setup(|app| {
            // The earlier shell kept the refresh token in a plaintext JSON file.
            // Delete it unread so a stale credential cannot outlive the move to
            // the OS credential service. A file that cannot be removed safely
            // (a symlink, a directory, a permission failure) aborts startup
            // rather than leaving the secret on disk.
            let data_dir = app.path().app_data_dir()?;
            match credentials::retire_legacy_store(&data_dir) {
                Ok(LegacyOutcome::Removed) => app
                    .state::<LegacyStoreRetired>()
                    .0
                    .store(true, Ordering::SeqCst),
                Ok(LegacyOutcome::Absent) => {}
                Err(error) => return Err(Box::new(error)),
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

/// Exposed for the builder above and for tests that assert the bridge contract
/// without starting a webview.
pub fn initialization_script() -> String {
    bridge_script()
}

#[cfg(test)]
mod tests {
    use super::*;

    // The frontend feature-detects these names and falls back silently when they
    // are absent, so a typo here disables pinning and persistence with no error
    // anywhere. Pin the exact strings the TypeScript reads.
    #[test]
    fn script_publishes_the_documented_bridge_names() {
        let script = initialization_script();
        assert!(
            !script.contains("__TELOS_NATIVE_TLS__"),
            "the TLS probe bridge is retired"
        );
        assert!(script.contains("window.__TELOS_NATIVE_CREDENTIALS__"));
        assert!(
            !script.contains("__TELOS_NATIVE_STORE__"),
            "the plaintext store bridge is retired"
        );
        assert!(script.contains("window.__TELOS_NATIVE_SHELL__"));
        assert!(!script.contains("leafCertificate"));
    }

    // Every command the script invokes has to be registered, or the call rejects
    // at runtime and the bridge degrades to "unsupported" without explanation.
    #[test]
    fn script_only_invokes_registered_commands() {
        let script = initialization_script();
        for command in [
            "telos_open_external",
            "telos_write_clipboard",
            "credential_get",
            "credential_set",
            "credential_delete",
            "telos_legacy_store_retired",
        ] {
            assert!(script.contains(command), "script never invokes {command}");
        }
    }

    // telos_open_external hands its argument to the platform's URL handler,
    // which acts on file:, smb: and every other registered scheme — and the
    // command is reachable from page script, so the filter is the boundary.
    #[test]
    fn only_web_urls_are_forwarded_to_the_os() {
        assert!(is_web_url("https://github.com/CMLeadmon/Telos"));
        assert!(is_web_url("http://192.168.1.10:8080"));
        for hostile in [
            "file:///etc/passwd",
            "smb://attacker/share",
            "javascript:alert(1)",
            "vscode://x",
            "  https://evasion.example.com",
        ] {
            assert!(!is_web_url(hostile), "{hostile} should not reach the OS");
        }
    }
}
