mod tls;

use tauri::Runtime;
use tauri_plugin_store::StoreExt;

/// Where the refresh token and certificate pins live between launches.
const SECRET_STORE: &str = "telos-secrets.json";

/// Reads the leaf certificate a node presents, for trust-on-first-use pinning.
/// Blocking socket work, so it is moved off the UI thread.
#[tauri::command]
async fn telos_leaf_certificate(server_url: String) -> Result<tls::LeafCertificate, String> {
    tauri::async_runtime::spawn_blocking(move || tls::leaf_certificate(&server_url))
        .await
        .map_err(|e| format!("certificate probe did not run: {e}"))?
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
fn telos_write_clipboard<R: Runtime>(
    app: tauri::AppHandle<R>,
    text: String,
) -> Result<(), String> {
    tauri_plugin_clipboard_manager::ClipboardExt::clipboard(&app)
        .write_text(text)
        .map_err(|e| e.to_string())
}

#[tauri::command]
fn telos_secret_get<R: Runtime>(app: tauri::AppHandle<R>, key: String) -> Result<Option<String>, String> {
    let store = app.store(SECRET_STORE).map_err(|e| e.to_string())?;
    Ok(store
        .get(&key)
        .and_then(|v| v.as_str().map(|s| s.to_string())))
}

#[tauri::command]
fn telos_secret_set<R: Runtime>(
    app: tauri::AppHandle<R>,
    key: String,
    value: String,
) -> Result<(), String> {
    let store = app.store(SECRET_STORE).map_err(|e| e.to_string())?;
    store.set(&key, serde_json::Value::String(value));
    // Written through on every set. These are credentials whose loss costs the
    // member a re-registration, and a crash between set and exit would take a
    // refresh token with it — the frontend would then hold an access token it
    // could never rotate.
    store.save().map_err(|e| e.to_string())
}

#[tauri::command]
fn telos_secret_delete<R: Runtime>(app: tauri::AppHandle<R>, key: String) -> Result<(), String> {
    let store = app.store(SECRET_STORE).map_err(|e| e.to_string())?;
    store.delete(&key);
    store.save().map_err(|e| e.to_string())
}

/// Installs the bridges the frontend looks for on `window`.
///
/// The frontend is one static export shared by the web build, which must not
/// import Tauri modules at all — so the shell publishes plain objects and the
/// frontend feature-detects them. The names are the contracts documented in
/// `frontend/src/lib/certPinning.ts`, `frontend/src/lib/secureStorage.ts` and
/// `frontend/src/lib/platform.ts`;
/// changing one here without changing it there silently disables the feature,
/// because both fall back rather than fail.
fn bridge_script() -> String {
    r#"
(function () {
  var invoke = window.__TAURI_INTERNALS__ && window.__TAURI_INTERNALS__.invoke;
  if (!invoke) return;

  window.__TELOS_NATIVE_TLS__ = {
    leafCertificate: function (serverUrl) {
      return invoke("telos_leaf_certificate", { serverUrl: serverUrl }).catch(function () {
        // A node that cannot be probed is reported as "no certificate seen",
        // never as a fingerprint. certPinning.ts then reports "unsupported"
        // rather than inventing a trusted state.
        return null;
      });
    },
  };

  window.__TELOS_NATIVE_SHELL__ = {
    openExternal: function (url) {
      return invoke("telos_open_external", { url: url });
    },
    writeClipboard: function (text) {
      return invoke("telos_write_clipboard", { text: text });
    },
  };

  window.__TELOS_NATIVE_STORE__ = {
    get: function (key) {
      return invoke("telos_secret_get", { key: key });
    },
    set: function (key, value) {
      return invoke("telos_secret_set", { key: key, value: value });
    },
    delete: function (key) {
      return invoke("telos_secret_delete", { key: key });
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
        .plugin(tauri_plugin_store::Builder::default().build())
        .invoke_handler(tauri::generate_handler![
            telos_leaf_certificate,
            telos_open_external,
            telos_write_clipboard,
            telos_secret_get,
            telos_secret_set,
            telos_secret_delete
        ])
        .setup(|app| {
            // Opening the store here surfaces a broken data directory at launch
            // instead of at the first secret write, which would otherwise look
            // like a silent fallback to non-persistent storage.
            app.store(SECRET_STORE)?;
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
        assert!(script.contains("window.__TELOS_NATIVE_TLS__"));
        assert!(script.contains("window.__TELOS_NATIVE_STORE__"));
        assert!(script.contains("window.__TELOS_NATIVE_SHELL__"));
        assert!(script.contains("leafCertificate"));
    }

    // Every command the script invokes has to be registered, or the call rejects
    // at runtime and the bridge degrades to "unsupported" without explanation.
    #[test]
    fn script_only_invokes_registered_commands() {
        let script = initialization_script();
        for command in [
            "telos_leaf_certificate",
            "telos_open_external",
            "telos_write_clipboard",
            "telos_secret_get",
            "telos_secret_set",
            "telos_secret_delete",
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
