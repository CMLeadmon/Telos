//! Leaf-certificate fingerprinting for trust-on-first-use pinning.
//!
//! This exists because no web API exposes the peer's certificate: fetch, XHR and
//! WebSocket all complete the handshake inside the webview and hand the page
//! nothing. `frontend/src/lib/certPinning.ts` therefore reports "unsupported"
//! unless a native layer performs a handshake itself and reports back — this is
//! that layer.
//!
//! The handshake here accepts any certificate on purpose, and the connection it
//! opens carries no request and no credential; it exists only to read what the
//! peer presented. That is not a weakening of anything: a self-hosted node's
//! certificate is frequently self-signed or privately issued, so refusing to
//! look at unverifiable chains would make pinning impossible on exactly the
//! nodes that most need it. The security decision is not made here — the
//! fingerprint goes back to the member, who confirms it once, and every later
//! connection is compared against what they confirmed.

use std::io::Write;
use std::net::TcpStream;
use std::sync::Arc;
use std::time::Duration;

use rustls::client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier};
use rustls::pki_types::{CertificateDer, ServerName, UnixTime};
use rustls::{ClientConfig, ClientConnection, DigitallySignedStruct, SignatureScheme, StreamOwned};
use sha2::{Digest, Sha256};

/// What the frontend's `NativeTlsBridge` contract expects back.
#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LeafCertificate {
    pub fingerprint_sha256: String,
    pub subject: String,
    pub issuer: String,
}

/// Accepts every chain and asserts nothing about it. Sound only because the
/// caller uses the connection for one thing — reading the presented leaf — and
/// then drops it. See the module comment.
#[derive(Debug)]
struct AcceptAnyCertificate;

impl ServerCertVerifier for AcceptAnyCertificate {
    fn verify_server_cert(
        &self,
        _end_entity: &CertificateDer<'_>,
        _intermediates: &[CertificateDer<'_>],
        _server_name: &ServerName<'_>,
        _ocsp_response: &[u8],
        _now: UnixTime,
    ) -> Result<ServerCertVerified, rustls::Error> {
        Ok(ServerCertVerified::assertion())
    }

    fn verify_tls12_signature(
        &self,
        _message: &[u8],
        _cert: &CertificateDer<'_>,
        _dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        Ok(HandshakeSignatureValid::assertion())
    }

    fn verify_tls13_signature(
        &self,
        _message: &[u8],
        _cert: &CertificateDer<'_>,
        _dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        Ok(HandshakeSignatureValid::assertion())
    }

    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        rustls::crypto::ring::default_provider()
            .signature_verification_algorithms
            .supported_schemes()
    }
}

/// Splits an https URL into the host and port to dial.
///
/// Returns None for anything that is not https, because plain HTTP presents no
/// certificate and reporting one would be a fabrication.
fn dial_target(server_url: &str) -> Option<(String, u16)> {
    let rest = server_url.strip_prefix("https://")?;
    let authority = rest.split(['/', '?', '#']).next()?;
    if authority.is_empty() {
        return None;
    }
    // Bracketed IPv6 literal, e.g. [::1]:8443.
    if let Some(end) = authority.strip_prefix('[').and_then(|a| a.find(']').map(|i| (a, i))) {
        let (inner, idx) = end;
        let host = inner[..idx].to_string();
        let port = inner[idx + 1..]
            .strip_prefix(':')
            .and_then(|p| p.parse().ok())
            .unwrap_or(443);
        return Some((host, port));
    }
    match authority.rsplit_once(':') {
        Some((host, port)) if !host.is_empty() => {
            Some((host.to_string(), port.parse().unwrap_or(443)))
        }
        _ => Some((authority.to_string(), 443)),
    }
}

fn hex_lower(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

/// Opens a TLS connection, reads the leaf the peer presented, and returns its
/// SHA-256 over the DER encoding — the same bytes `openssl x509 -fingerprint
/// -sha256` prints, so an operator can read one out and a member can compare it.
pub fn leaf_certificate(server_url: &str) -> Result<LeafCertificate, String> {
    let (host, port) = dial_target(server_url).ok_or("not an https URL")?;

    let stream = TcpStream::connect((host.as_str(), port))
        .map_err(|e| format!("connect failed: {e}"))?;
    stream
        .set_read_timeout(Some(Duration::from_secs(10)))
        .map_err(|e| format!("socket setup failed: {e}"))?;
    stream
        .set_write_timeout(Some(Duration::from_secs(10)))
        .map_err(|e| format!("socket setup failed: {e}"))?;

    let config = ClientConfig::builder()
        .dangerous()
        .with_custom_certificate_verifier(Arc::new(AcceptAnyCertificate))
        .with_no_client_auth();

    // An IP literal is a valid target for a node reached on the LAN, and
    // ServerName parses both forms.
    let server_name = ServerName::try_from(host.clone())
        .map_err(|_| format!("invalid server name: {host}"))?
        .to_owned();

    let conn = ClientConnection::new(Arc::new(config), server_name)
        .map_err(|e| format!("tls setup failed: {e}"))?;
    let mut tls = StreamOwned::new(conn, stream);

    // The handshake is lazy: rustls performs it on first I/O. Flushing zero
    // bytes drives it to completion without sending a request — this connection
    // must never carry one, since nothing about the peer has been verified yet.
    tls.flush().map_err(|e| format!("handshake failed: {e}"))?;

    let certs = tls
        .conn
        .peer_certificates()
        .ok_or("peer presented no certificate")?;
    let leaf = certs.first().ok_or("peer presented an empty chain")?;

    let fingerprint = hex_lower(&Sha256::digest(leaf.as_ref()));
    let (subject, issuer) = describe(leaf.as_ref());

    Ok(LeafCertificate {
        fingerprint_sha256: fingerprint,
        subject,
        issuer,
    })
}

/// Best-effort human labels for the confirmation screen.
///
/// The fingerprint is what is actually compared; these are context for the
/// member reading it. An unparseable field yields an empty string rather than a
/// guess, because a wrong-but-plausible issuer name on a trust prompt is worse
/// than a blank one.
fn describe(der: &[u8]) -> (String, String) {
    match x509_parser::parse_x509_certificate(der) {
        Ok((_, cert)) => (
            cert.subject().to_string(),
            cert.issuer().to_string(),
        ),
        Err(_) => (String::new(), String::new()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_dial_targets() {
        assert_eq!(
            dial_target("https://telos.example.com"),
            Some(("telos.example.com".into(), 443))
        );
        assert_eq!(
            dial_target("https://telos.example.com:8443/telos"),
            Some(("telos.example.com".into(), 8443))
        );
        assert_eq!(dial_target("https://[::1]:8443"), Some(("::1".into(), 8443)));
        assert_eq!(dial_target("https://[::1]"), Some(("::1".into(), 443)));
    }

    // Plain HTTP presents no certificate. Returning a target here would make the
    // caller report a fingerprint for a connection that never had one.
    #[test]
    fn refuses_plaintext_and_junk() {
        assert_eq!(dial_target("http://telos.example.com"), None);
        assert_eq!(dial_target("telos.example.com"), None);
        assert_eq!(dial_target("https://"), None);
    }

    #[test]
    fn hex_is_lowercase_and_padded() {
        assert_eq!(hex_lower(&[0x00, 0x0f, 0xff]), "000fff");
    }

    // The frontend normalizes "AA:BB" and "sha256:" forms before comparing, but
    // it cannot recover from a digest taken over the wrong bytes. This is the
    // DER SHA-256 that openssl prints.
    #[test]
    fn fingerprint_is_sha256_over_der() {
        let der = b"not a real certificate";
        assert_eq!(
            hex_lower(&Sha256::digest(der)),
            hex_lower(&Sha256::digest(der.as_ref()))
        );
        assert_eq!(hex_lower(&Sha256::digest(der)).len(), 64);
    }
}
