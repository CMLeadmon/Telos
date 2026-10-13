//! Operating-system credential storage for the device refresh token.
//!
//! The shell hands the page a narrow capability: read, write or delete the refresh
//! token of ONE (node origin, account, device). JavaScript never names a keychain
//! service or account string; it supplies a structured key and this module derives
//! the strings, so a compromised page cannot read another application's secrets or
//! another node's credentials by guessing a name.
//!
//! Failures are explicit and never degrade: a missing, locked or denied credential
//! service is a visible error, and there is no in-memory or file fallback. A
//! caller that cannot persist the refresh token must treat the sign-in as failed.
//!
//! Nothing here formats a secret. Error values carry a fixed classification and
//! message only; the platform error text and the secret bytes that some platform
//! errors carry are discarded at the boundary.

use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::fmt;
use std::path::Path;
use url::Url;

/// Keychain service namespace. The per-origin suffix keeps two nodes' entries apart.
pub const SERVICE_PREFIX: &str = "com.telos.app";

/// The file the pre-keychain shell kept secrets in. It is deleted, never read.
pub const LEGACY_STORE_FILE: &str = "telos-secrets.json";

/// The structured address of one credential. Unknown fields are rejected so the
/// page cannot smuggle a raw storage key through the command boundary.
#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CredentialKey {
    pub origin: String,
    pub account_id: String,
    pub device_id: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum CredentialError {
    /// The key does not name a credential this shell will touch.
    InvalidKey(&'static str),
    /// No credential service could be reached (no session bus, no Secret Service).
    Unavailable,
    /// The service exists but refused access (locked collection, denied prompt).
    Locked,
    /// Any other failure inside the platform store.
    Backend,
    /// The stored value could not be read back as expected.
    Corrupt,
    /// The retired plaintext store could not be removed safely.
    LegacyStore(&'static str),
}

impl CredentialError {
    pub fn code(&self) -> &'static str {
        match self {
            CredentialError::InvalidKey(_) => "credential_invalid_key",
            CredentialError::Unavailable => "credential_store_unavailable",
            CredentialError::Locked => "credential_store_locked",
            CredentialError::Backend => "credential_store_error",
            CredentialError::Corrupt => "credential_store_corrupt",
            CredentialError::LegacyStore(_) => "credential_legacy_store",
        }
    }

    /// The string returned to the page: a stable code and a fixed sentence.
    pub fn to_frontend(&self) -> String {
        format!("{}: {}", self.code(), self)
    }
}

impl fmt::Display for CredentialError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            CredentialError::InvalidKey(why) => write!(f, "the credential key is not valid ({why})"),
            CredentialError::Unavailable => write!(
                f,
                "no operating-system credential service is available; start a Secret Service such as GNOME Keyring and sign in again"
            ),
            CredentialError::Locked => write!(
                f,
                "the operating-system credential service is locked or denied access; unlock it and try again"
            ),
            CredentialError::Backend => write!(f, "the operating-system credential service reported an error"),
            CredentialError::Corrupt => write!(f, "the stored credential could not be read"),
            CredentialError::LegacyStore(why) => write!(f, "the previous plaintext credential file could not be removed ({why})"),
        }
    }
}

impl std::error::Error for CredentialError {}

/// The storage seam. `delete` of a missing entry succeeds: logout and cleanup are
/// idempotent, and "it is already gone" is the state they want.
pub trait CredentialBackend: Send + Sync {
    fn get(&self, service: &str, account: &str) -> Result<Option<String>, CredentialError>;
    fn set(&self, service: &str, account: &str, secret: &str) -> Result<(), CredentialError>;
    fn delete(&self, service: &str, account: &str) -> Result<(), CredentialError>;
}

/// Whether a plain-HTTP loopback node is acceptable. Compiled in for debug builds
/// only; a release build cannot enable it, and the TypeScript side mirrors the
/// same rule.
pub fn debug_loopback_http_allowed() -> bool {
    cfg!(debug_assertions)
}

fn is_loopback_host(host: &str) -> bool {
    matches!(host, "localhost" | "127.0.0.1" | "[::1]")
}

/// Canonical `scheme://host[:port]` for a node, default port folded, nothing else.
pub fn normalize_origin(raw: &str, allow_loopback_http: bool) -> Result<String, CredentialError> {
    let url =
        Url::parse(raw.trim()).map_err(|_| CredentialError::InvalidKey("origin is not a URL"))?;
    if !url.username().is_empty() || url.password().is_some() {
        return Err(CredentialError::InvalidKey(
            "origin may not contain credentials",
        ));
    }
    if url.path() != "/" || url.query().is_some() || url.fragment().is_some() {
        return Err(CredentialError::InvalidKey(
            "origin may not contain a path, query or fragment",
        ));
    }
    let host = url
        .host_str()
        .ok_or(CredentialError::InvalidKey("origin has no host"))?;
    match url.scheme() {
        "https" => {}
        "http" if allow_loopback_http && is_loopback_host(host) => {}
        "http" => return Err(CredentialError::InvalidKey("origin must be https")),
        _ => return Err(CredentialError::InvalidKey("origin must be https")),
    }
    Ok(url.origin().ascii_serialization())
}

fn is_canonical_uuid(value: &str) -> bool {
    let bytes = value.as_bytes();
    bytes.len() == 36
        && bytes.iter().enumerate().all(|(index, byte)| match index {
            8 | 13 | 18 | 23 => *byte == b'-',
            _ => matches!(byte, b'0'..=b'9' | b'a'..=b'f'),
        })
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

/// `(service, account)` for a key: `com.telos.app/<sha256(origin)>` and
/// `<account-id>/<device-id>`.
pub fn derive_names(
    key: &CredentialKey,
    allow_loopback_http: bool,
) -> Result<(String, String), CredentialError> {
    let origin = normalize_origin(&key.origin, allow_loopback_http)?;
    if !is_canonical_uuid(&key.account_id) {
        return Err(CredentialError::InvalidKey(
            "account id is not a canonical UUID",
        ));
    }
    if !is_canonical_uuid(&key.device_id) {
        return Err(CredentialError::InvalidKey(
            "device id is not a canonical UUID",
        ));
    }
    let digest = Sha256::digest(origin.as_bytes());
    Ok((
        format!("{SERVICE_PREFIX}/{}", hex(&digest)),
        format!("{}/{}", key.account_id, key.device_id),
    ))
}

/// Key derivation plus a backend. This is what the Tauri commands call.
pub struct CredentialService<B: CredentialBackend> {
    backend: B,
    allow_loopback_http: bool,
}

impl<B: CredentialBackend> CredentialService<B> {
    pub fn new(backend: B, allow_loopback_http: bool) -> Self {
        Self {
            backend,
            allow_loopback_http,
        }
    }

    pub fn get(&self, key: &CredentialKey) -> Result<Option<String>, CredentialError> {
        let (service, account) = derive_names(key, self.allow_loopback_http)?;
        self.backend.get(&service, &account)
    }

    pub fn set(&self, key: &CredentialKey, secret: &str) -> Result<(), CredentialError> {
        if secret.is_empty() || secret.len() > 4096 {
            return Err(CredentialError::InvalidKey("secret must be 1..4096 bytes"));
        }
        let (service, account) = derive_names(key, self.allow_loopback_http)?;
        self.backend.set(&service, &account, secret)
    }

    pub fn delete(&self, key: &CredentialKey) -> Result<(), CredentialError> {
        let (service, account) = derive_names(key, self.allow_loopback_http)?;
        self.backend.delete(&service, &account)
    }
}

/// Maps a platform error to a classification. The error's own text and any bytes
/// it carries are dropped deliberately.
fn classify(error: keyring::v1::Error) -> CredentialError {
    use keyring::v1::Error as E;
    match error {
        E::NoDefaultStore => CredentialError::Unavailable,
        E::NoStorageAccess(_) => CredentialError::Locked,
        E::BadEncoding(_) | E::BadDataFormat(_, _) | E::BadStoreFormat(_) => {
            CredentialError::Corrupt
        }
        E::PlatformFailure(_) => CredentialError::Backend,
        _ => CredentialError::Backend,
    }
}

/// The real backend: the Secret Service on Linux via the `keyring` crate.
pub struct OsBackend;

impl OsBackend {
    fn entry(service: &str, account: &str) -> Result<keyring::v1::Entry, CredentialError> {
        keyring::v1::Entry::new(service, account).map_err(classify)
    }
}

impl CredentialBackend for OsBackend {
    fn get(&self, service: &str, account: &str) -> Result<Option<String>, CredentialError> {
        match Self::entry(service, account)?.get_password() {
            Ok(secret) => Ok(Some(secret)),
            Err(keyring::v1::Error::NoEntry) => Ok(None),
            Err(error) => Err(classify(error)),
        }
    }

    fn set(&self, service: &str, account: &str, secret: &str) -> Result<(), CredentialError> {
        Self::entry(service, account)?
            .set_password(secret)
            .map_err(classify)
    }

    fn delete(&self, service: &str, account: &str) -> Result<(), CredentialError> {
        match Self::entry(service, account)?.delete_credential() {
            Ok(()) | Err(keyring::v1::Error::NoEntry) => Ok(()),
            Err(error) => Err(classify(error)),
        }
    }
}

#[derive(Debug, PartialEq, Eq)]
pub enum LegacyOutcome {
    Absent,
    Removed,
}

/// Deletes the plaintext secret file the earlier shell wrote, without reading,
/// parsing, logging or importing it. Only an exact regular file is removed:
/// a symlink or any other type is refused rather than followed.
pub fn retire_legacy_store(app_data_dir: &Path) -> Result<LegacyOutcome, CredentialError> {
    let path = app_data_dir.join(LEGACY_STORE_FILE);
    match std::fs::symlink_metadata(&path) {
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(LegacyOutcome::Absent),
        Err(_) => Err(CredentialError::LegacyStore("it could not be inspected")),
        Ok(metadata) if metadata.file_type().is_file() => std::fs::remove_file(&path)
            .map(|_| LegacyOutcome::Removed)
            .map_err(|_| CredentialError::LegacyStore("deletion failed")),
        Ok(_) => Err(CredentialError::LegacyStore("it is not a regular file")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    use std::sync::Mutex;

    const ACCOUNT: &str = "0f8fad5b-d9cb-469f-a165-70867728950e";
    const DEVICE: &str = "7c9e6679-7425-40de-944b-e07fc1f90ae7";
    const SECRET: &str = "rt_super-secret-refresh-token-value";

    fn key(origin: &str) -> CredentialKey {
        CredentialKey {
            origin: origin.into(),
            account_id: ACCOUNT.into(),
            device_id: DEVICE.into(),
        }
    }

    #[derive(Default)]
    struct Fake {
        entries: Mutex<HashMap<(String, String), String>>,
        calls: Mutex<Vec<String>>,
        fail_with: Mutex<Option<CredentialError>>,
    }

    impl Fake {
        fn failing(error: CredentialError) -> Self {
            let fake = Fake::default();
            *fake.fail_with.lock().unwrap() = Some(error);
            fake
        }
        fn check(&self, call: &str) -> Result<(), CredentialError> {
            self.calls.lock().unwrap().push(call.to_string());
            match self.fail_with.lock().unwrap().clone() {
                Some(error) => Err(error),
                None => Ok(()),
            }
        }
    }

    impl CredentialBackend for &Fake {
        fn get(&self, service: &str, account: &str) -> Result<Option<String>, CredentialError> {
            self.check(&format!("get {service} {account}"))?;
            Ok(self
                .entries
                .lock()
                .unwrap()
                .get(&(service.into(), account.into()))
                .cloned())
        }
        fn set(&self, service: &str, account: &str, secret: &str) -> Result<(), CredentialError> {
            self.check(&format!("set {service} {account}"))?;
            self.entries
                .lock()
                .unwrap()
                .insert((service.into(), account.into()), secret.into());
            Ok(())
        }
        fn delete(&self, service: &str, account: &str) -> Result<(), CredentialError> {
            self.check(&format!("delete {service} {account}"))?;
            self.entries
                .lock()
                .unwrap()
                .remove(&(service.to_string(), account.to_string()));
            Ok(())
        }
    }

    #[test]
    fn origins_normalize_to_one_canonical_https_form() {
        for raw in [
            "https://Node.Example.com",
            "https://node.example.com/",
            "https://node.example.com:443",
            "  https://NODE.example.com  ",
        ] {
            assert_eq!(
                normalize_origin(raw, false).unwrap(),
                "https://node.example.com",
                "{raw}"
            );
        }
        assert_eq!(
            normalize_origin("https://node.example.com:8443", false).unwrap(),
            "https://node.example.com:8443"
        );
    }

    #[test]
    fn hostile_origins_are_rejected() {
        for raw in [
            "http://node.example.com",
            "ftp://node.example.com",
            "https://user@node.example.com",
            "https://user:pw@node.example.com",
            "https://node.example.com/telos",
            "https://node.example.com/?x=1",
            "https://node.example.com/#frag",
            "node.example.com",
            "",
            "tauri://localhost",
            "javascript:alert(1)",
        ] {
            assert!(
                normalize_origin(raw, false).is_err(),
                "{raw} should be rejected"
            );
        }
    }

    #[test]
    fn loopback_http_exists_only_behind_the_debug_flag() {
        assert!(normalize_origin("http://localhost:8080", false).is_err());
        assert!(normalize_origin("http://127.0.0.1:8080", false).is_err());
        assert_eq!(
            normalize_origin("http://localhost:8080", true).unwrap(),
            "http://localhost:8080"
        );
        assert_eq!(
            normalize_origin("http://127.0.0.1:8080", true).unwrap(),
            "http://127.0.0.1:8080"
        );
        assert_eq!(
            normalize_origin("http://[::1]:8080", true).unwrap(),
            "http://[::1]:8080"
        );
        // Even with the flag, plain HTTP to a real host stays refused.
        assert!(normalize_origin("http://node.example.com", true).is_err());
        assert!(normalize_origin("http://localhost.evil.example", true).is_err());
    }

    #[test]
    fn names_are_stable_and_distinct_per_origin_account_and_device() {
        let (service, account) = derive_names(&key("https://node.example.com"), false).unwrap();
        let (again, _) = derive_names(&key("https://NODE.example.com:443/"), false).unwrap();
        assert_eq!(service, again);
        assert!(service.starts_with("com.telos.app/"));
        assert_eq!(service.len(), "com.telos.app/".len() + 64);
        assert_eq!(account, format!("{ACCOUNT}/{DEVICE}"));
        let (other_origin, _) = derive_names(&key("https://other.example.com"), false).unwrap();
        assert_ne!(service, other_origin);
        let mut other_account = key("https://node.example.com");
        other_account.account_id = "11111111-2222-3333-4444-555555555555".into();
        assert_ne!(derive_names(&other_account, false).unwrap().1, account);
        let mut other_device = key("https://node.example.com");
        other_device.device_id = "99999999-2222-3333-4444-555555555555".into();
        assert_ne!(derive_names(&other_device, false).unwrap().1, account);
    }

    #[test]
    fn ids_must_be_canonical_uuids() {
        for bad in [
            "",
            "not-a-uuid",
            "0F8FAD5B-D9CB-469F-A165-70867728950E",
            "0f8fad5bd9cb469fa16570867728950e",
            "0f8fad5b-d9cb-469f-a165-70867728950e/../x",
        ] {
            let mut k = key("https://node.example.com");
            k.account_id = bad.into();
            assert!(derive_names(&k, false).is_err(), "account {bad:?}");
            let mut k = key("https://node.example.com");
            k.device_id = bad.into();
            assert!(derive_names(&k, false).is_err(), "device {bad:?}");
        }
    }

    #[test]
    fn unknown_fields_cannot_smuggle_a_raw_storage_key() {
        let parsed: Result<CredentialKey, _> = serde_json::from_str(
            r#"{"origin":"https://n.example.com","accountId":"a","deviceId":"d","service":"other-app"}"#,
        );
        assert!(parsed.is_err());
        let parsed: Result<CredentialKey, _> = serde_json::from_str(
            r#"{"origin":"https://n.example.com","accountId":"a","deviceId":"d"}"#,
        );
        assert!(parsed.is_ok());
    }

    #[test]
    fn get_set_delete_forward_to_the_backend_with_derived_names() {
        let fake = Fake::default();
        let service = CredentialService::new(&fake, false);
        let k = key("https://node.example.com");
        assert_eq!(
            service.get(&k).unwrap(),
            None,
            "a missing entry is None, not an error"
        );
        service.set(&k, SECRET).unwrap();
        assert_eq!(service.get(&k).unwrap().as_deref(), Some(SECRET));
        service.delete(&k).unwrap();
        assert_eq!(service.get(&k).unwrap(), None);
        service.delete(&k).unwrap();
        let (svc, acct) = derive_names(&k, false).unwrap();
        let calls = fake.calls.lock().unwrap();
        assert!(calls.iter().all(|c| c.contains(&svc) && c.contains(&acct)));
        assert_eq!(calls.iter().filter(|c| c.starts_with("set")).count(), 1);
    }

    #[test]
    fn different_nodes_never_share_an_entry() {
        let fake = Fake::default();
        let service = CredentialService::new(&fake, false);
        service
            .set(&key("https://one.example.com"), "token-one")
            .unwrap();
        service
            .set(&key("https://two.example.com"), "token-two")
            .unwrap();
        assert_eq!(
            service
                .get(&key("https://one.example.com"))
                .unwrap()
                .as_deref(),
            Some("token-one")
        );
        assert_eq!(
            service
                .get(&key("https://two.example.com"))
                .unwrap()
                .as_deref(),
            Some("token-two")
        );
        service.delete(&key("https://one.example.com")).unwrap();
        assert_eq!(
            service
                .get(&key("https://two.example.com"))
                .unwrap()
                .as_deref(),
            Some("token-two")
        );
    }

    #[test]
    fn invalid_keys_never_reach_the_backend() {
        let fake = Fake::default();
        let service = CredentialService::new(&fake, false);
        assert!(service
            .set(&key("http://node.example.com"), SECRET)
            .is_err());
        assert!(service.get(&key("https://node.example.com/path")).is_err());
        assert!(service.set(&key("https://node.example.com"), "").is_err());
        assert!(fake.calls.lock().unwrap().is_empty());
    }

    #[test]
    fn backend_errors_propagate_visibly_and_never_carry_the_secret() {
        for error in [
            CredentialError::Unavailable,
            CredentialError::Locked,
            CredentialError::Backend,
            CredentialError::Corrupt,
        ] {
            let fake = Fake::failing(error.clone());
            let service = CredentialService::new(&fake, false);
            let got = service
                .set(&key("https://node.example.com"), SECRET)
                .unwrap_err();
            assert_eq!(got, error);
            for text in [format!("{got}"), format!("{got:?}"), got.to_frontend()] {
                assert!(
                    !text.contains(SECRET),
                    "error text leaked the secret: {text}"
                );
            }
            assert!(got.to_frontend().starts_with(got.code()));
            assert_eq!(
                service.get(&key("https://node.example.com")).unwrap_err(),
                error
            );
            assert_eq!(
                service
                    .delete(&key("https://node.example.com"))
                    .unwrap_err(),
                error
            );
        }
    }

    #[test]
    fn error_codes_are_distinct() {
        let codes: std::collections::HashSet<_> = [
            CredentialError::InvalidKey("x"),
            CredentialError::Unavailable,
            CredentialError::Locked,
            CredentialError::Backend,
            CredentialError::Corrupt,
            CredentialError::LegacyStore("x"),
        ]
        .iter()
        .map(|e| e.code())
        .collect();
        assert_eq!(codes.len(), 6);
    }

    fn scratch(name: &str) -> std::path::PathBuf {
        let dir =
            std::env::temp_dir().join(format!("telos-credentials-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        dir
    }

    #[test]
    fn the_legacy_plaintext_store_is_deleted_unread_and_unrelated_files_survive() {
        let dir = scratch("legacy");
        assert_eq!(retire_legacy_store(&dir).unwrap(), LegacyOutcome::Absent);
        std::fs::write(
            dir.join(LEGACY_STORE_FILE),
            r#"{"telos.refreshToken":"plaintext"}"#,
        )
        .unwrap();
        std::fs::write(dir.join("keep.txt"), "keep").unwrap();
        std::fs::create_dir(dir.join("subdir")).unwrap();
        assert_eq!(retire_legacy_store(&dir).unwrap(), LegacyOutcome::Removed);
        assert!(!dir.join(LEGACY_STORE_FILE).exists());
        assert_eq!(
            std::fs::read_to_string(dir.join("keep.txt")).unwrap(),
            "keep"
        );
        assert!(dir.join("subdir").is_dir());
        assert_eq!(retire_legacy_store(&dir).unwrap(), LegacyOutcome::Absent);
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn a_symlink_or_directory_in_place_of_the_legacy_file_is_refused() {
        let dir = scratch("refuse");
        let target = dir.join("elsewhere.txt");
        std::fs::write(&target, "do not delete").unwrap();
        std::os::unix::fs::symlink(&target, dir.join(LEGACY_STORE_FILE)).unwrap();
        assert!(matches!(
            retire_legacy_store(&dir),
            Err(CredentialError::LegacyStore(_))
        ));
        assert!(target.exists(), "the symlink target must not be touched");
        std::fs::remove_file(dir.join(LEGACY_STORE_FILE)).unwrap();
        std::fs::create_dir(dir.join(LEGACY_STORE_FILE)).unwrap();
        assert!(matches!(
            retire_legacy_store(&dir),
            Err(CredentialError::LegacyStore(_))
        ));
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn release_builds_cannot_enable_loopback_http() {
        // The flag is derived from debug_assertions, so a release build has it off.
        assert_eq!(debug_loopback_http_allowed(), cfg!(debug_assertions));
    }
}
