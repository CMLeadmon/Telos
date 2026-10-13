package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════
// Self-authorizing media capabilities
// ═══════════════════════════════════════════════════════════════════════════
//
// A media element fetches its own source, so the URL has to carry its own
// authority: there is no hook to attach an Authorization header to what an
// <audio> or a native <video> requests, and a desktop client holds no cookie.
// A capability URL is therefore a bearer credential, with two safeguards that
// make a 12-hour lifetime acceptable:
//
//   - it names the AUTHORITY that minted it (a browser session, or a device) and
//     every spend re-checks that authority, the account's active state and the
//     current permission against the database. Logout, a revoked device, a
//     disabled account or a removed role end playback at the next request;
//   - it is sealed with an HMAC under a key derived per capability family, so one
//     family's token cannot be decoded as another's.
//
// The authority is the browser session or the DEVICE, never the 15-minute access
// token: a desktop client rotates its access token four times an hour and
// playback has to survive that.
//
// What a capability cannot do is identify who spends it. A copied URL is usable
// by whoever holds it until the issuer's authority ends or the URL expires; the
// server has no cookie or header to tell a second viewer apart. That boundary is
// explicit (see the tests), not a promise of copy detection.

const (
	authoritySession = "session"
	authorityDevice  = "device"

	// mediaCapabilityTTL is the longest any capability lives, and also the cap on
	// every URL rewritten from one: rewriting cannot extend a grant.
	mediaCapabilityTTL = 12 * time.Hour

	labelHLSLocator    = "telos-hls-locator-v1"
	labelStreamTicket  = "telos-stream-ticket-v1" // the existing audiobook derivation, preserved
	labelJellyfinAudio = "telos-jellyfin-audio-ticket-v1"

	typeHLS            = "hls"
	typeJellyfinAudio  = "jellyfin-audio"
	typeGrimmoryAudiob = "grimmory-audiobook"
)

var errMediaAuthority = errors.New("media authority rejected")

var loadAuthorizedMediaUser = loadAuthorizedMediaUserFromDB

// mediaAuthority is the stable principal a capability was minted from.
type mediaAuthority struct {
	Kind string `json:"k"` // session | device
	ID   string `json:"a"` // session token hash | device id
}

// authorityFromUser names the stable authority behind an authenticated request.
func authorityFromUser(user *UserContext) (mediaAuthority, error) {
	if user == nil || user.ID == "" {
		return mediaAuthority{}, errMediaAuthority
	}
	if user.DeviceID != "" {
		return mediaAuthority{Kind: authorityDevice, ID: user.DeviceID}, nil
	}
	if user.SessionHash != "" {
		return mediaAuthority{Kind: authoritySession, ID: user.SessionHash}, nil
	}
	return mediaAuthority{}, errMediaAuthority
}

// loadAuthorizedMediaUser is the seam handlers use (a package var, like
// resolveCatalogIdentity) so unit tests can stub the database. It re-establishes, at spend time, that the authority is
// still live and the user may still do `permission`. It returns the user (with
// DeviceID/SessionHash set as an ordinary request would have them) and the
// instant the authority itself ends, so callers can clamp any URL they mint.
//
// Browser session: the session row must belong to the user, not be a device
// session, be unrevoked and unexpired. Device: the device must belong to the user
// and be unrevoked, and have at least one live (unconsumed, unreplaced,
// unexpired) refresh token -- devices have no expiry column of their own, so
// "expired device" means no live refresh credential, and that credential's
// expiry is the horizon. The 15-minute access session is deliberately not
// consulted. Every failure is the same error: the caller must not reveal which
// part failed.
func loadAuthorizedMediaUserFromDB(ctx context.Context, authority mediaAuthority, userID, permission string, now time.Time) (*UserContext, time.Time, error) {
	if dbPool == nil || userID == "" || authority.ID == "" {
		return nil, time.Time{}, errMediaAuthority
	}
	var horizon *time.Time
	switch authority.Kind {
	case authoritySession:
		if err := dbPool.QueryRow(ctx, `
			SELECT expires_at FROM sessions
			WHERE token_hash = $1 AND user_id = $2 AND device_id IS NULL
			  AND revoked_at IS NULL AND expires_at > $3
		`, authority.ID, userID, now).Scan(&horizon); err != nil {
			return nil, time.Time{}, errMediaAuthority
		}
	case authorityDevice:
		if err := dbPool.QueryRow(ctx, `
			SELECT MAX(t.expires_at)
			FROM devices d
			JOIN device_refresh_tokens t ON t.device_id = d.id
			WHERE d.id = $1 AND d.user_id = $2 AND d.revoked_at IS NULL
			  AND t.consumed_at IS NULL AND t.replaced_by IS NULL AND t.expires_at > $3
		`, authority.ID, userID, now).Scan(&horizon); err != nil {
			return nil, time.Time{}, errMediaAuthority
		}
	default:
		return nil, time.Time{}, errMediaAuthority
	}
	if horizon == nil || !horizon.After(now) {
		return nil, time.Time{}, errMediaAuthority
	}
	var active bool
	if err := dbPool.QueryRow(ctx, `SELECT active FROM users WHERE id = $1`, userID).Scan(&active); err != nil || !active {
		return nil, time.Time{}, errMediaAuthority
	}
	user, err := loadUserContext(ctx, userID)
	if err != nil {
		return nil, time.Time{}, errMediaAuthority
	}
	if authority.Kind == authorityDevice {
		user.DeviceID = authority.ID
	} else {
		user.SessionHash = authority.ID
	}
	if permission != "" {
		allowed, err := hasPermission(ctx, user, permission, nil)
		if err != nil || !allowed {
			return nil, time.Time{}, errMediaAuthority
		}
	}
	return user, *horizon, nil
}

// capabilityExpiry is when a capability minted now should end: the standard
// lifetime, never past the authority's own end.
func capabilityExpiry(now, authorityEnd time.Time) (time.Time, error) {
	exp := now.Add(mediaCapabilityTTL)
	if authorityEnd.Before(exp) {
		exp = authorityEnd
	}
	if !exp.After(now) {
		return time.Time{}, errMediaAuthority
	}
	return exp, nil
}

// nestedCapabilityExpiry bounds a URL rewritten from an existing capability: it
// can never outlive its parent or the authority.
func nestedCapabilityExpiry(parentExp int64, authorityEnd time.Time) time.Time {
	exp := time.Unix(parentExp, 0)
	if authorityEnd.Before(exp) {
		exp = authorityEnd
	}
	return exp
}

// ---------------------------------------------------------------------------
// Sealing
// ---------------------------------------------------------------------------

// mediaKey derives the signing key for one capability family from the dedicated
// HLS root secret. The labels are the domain separation: the families' JSON bodies
// overlap, and with a shared key a token of one family could be spent as another.
func mediaKey(label string) []byte {
	mac := hmac.New(sha256.New, hlsSigningKey)
	mac.Write([]byte(label))
	return mac.Sum(nil)
}

func sealClaims(label string, claims any) string {
	body, _ := json.Marshal(claims)
	mac := hmac.New(sha256.New, mediaKey(label))
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// openClaims authenticates a token (constant-time MAC compare, before anything in
// the body is believed) and decodes it into dst.
func openClaims(label, token string, dst any) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, mediaKey(label))
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return false
	}
	return json.Unmarshal(body, dst) == nil
}

// ---------------------------------------------------------------------------
// Response hygiene for capability URLs
// ---------------------------------------------------------------------------

// capabilityResponseWriter forces the headers a capability response must carry,
// at the moment headers are written, so an upstream Cache-Control relayed by the
// media proxy cannot weaken them. The URL is a credential: it must not be cached
// by a shared cache or leak through a Referer.
type capabilityResponseWriter struct{ http.ResponseWriter }

func (w capabilityResponseWriter) WriteHeader(status int) {
	h := w.ResponseWriter.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	w.ResponseWriter.WriteHeader(status)
}

func (w capabilityResponseWriter) Write(b []byte) (int, error) {
	// A handler that never calls WriteHeader still gets the headers.
	h := w.ResponseWriter.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	return w.ResponseWriter.Write(b)
}

func (w capabilityResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func withCapabilityHeaders(w http.ResponseWriter) http.ResponseWriter {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	return capabilityResponseWriter{w}
}
