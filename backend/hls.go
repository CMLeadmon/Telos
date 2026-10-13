package main

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// ═══════════════════════════════════════════════════════════════════════════
// HLS locator signing key
// ═══════════════════════════════════════════════════════════════════════════

// hlsSigningKey signs opaque HLS resource locators. It is a dedicated secret,
// never shared with the session or cursor keys.
var hlsSigningKey []byte

// loadHLSSigningKey reads the mode-0600 TELOS_HLS_SIGNING_KEY_FILE (base64 of at
// least 32 random bytes). In development a distinct key is synthesized so HLS
// works without an operator file; the seed differs from the cursor dev key so
// the two are never equal.
func loadHLSSigningKey(path, env string) ([]byte, error) {
	if path == "" {
		if env == "development" {
			b := sha256.Sum256([]byte("telos-dev-hls-signing-key"))
			return b[:], nil
		}
		return nil, errors.New("TELOS_HLS_SIGNING_KEY_FILE is not set")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("TELOS_HLS_SIGNING_KEY_FILE must be mode 0600")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, errors.New("TELOS_HLS_SIGNING_KEY_FILE is not valid base64")
	}
	if len(key) < 32 {
		return nil, errors.New("TELOS_HLS_SIGNING_KEY_FILE must decode to at least 32 bytes")
	}
	return key, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Opaque HLS resource locator (HMAC-bound to item/user/authority/resource/expiry)
// ═══════════════════════════════════════════════════════════════════════════

// hlsLocatorTTL is the longest an HLS locator lives. It is long because a media
// element re-requests segments over a whole viewing session and cannot refresh
// its own URL; it is safe because every spend re-checks the minting authority
// (see media_authority.go). Nested locators never outlive their parent.
const hlsLocatorTTL = mediaCapabilityTTL

// hlsLocator is the sealed claim set an opaque locator carries. Field names are
// terse to keep tokens short; the token is never parsed by the browser.
type hlsLocator struct {
	Type string         `json:"y"`
	Item string         `json:"i"`
	User string         `json:"u"`
	Auth mediaAuthority `json:"a"`
	Res  string         `json:"r"`
	Exp  int64          `json:"e"`
}

var (
	errHLSLocatorInvalid = errors.New("hls locator invalid")
	errHLSLocatorExpired = errors.New("hls locator expired")
)

// signHLSLocator seals a locator into base64url(body).base64url(HMAC) under the
// HLS family key.
func signHLSLocator(loc hlsLocator) string {
	loc.Type = typeHLS
	return sealClaims(labelHLSLocator, loc)
}

// verifyHLSLocator authenticates and de-seals a locator, rejecting tamper, the
// wrong capability type and expiry. The signature is checked in constant time
// and before anything in the body is believed.
func verifyHLSLocator(token string, now time.Time) (hlsLocator, error) {
	var loc hlsLocator
	if !openClaims(labelHLSLocator, token, &loc) {
		return hlsLocator{}, errHLSLocatorInvalid
	}
	if loc.Type != typeHLS || loc.Item == "" || loc.User == "" || loc.Auth.Kind == "" || loc.Auth.ID == "" || loc.Res == "" {
		return hlsLocator{}, errHLSLocatorInvalid
	}
	if now.Unix() > loc.Exp {
		return hlsLocator{}, errHLSLocatorExpired
	}
	return loc, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Manifest parsing and URI rewriting
// ═══════════════════════════════════════════════════════════════════════════

const (
	maxHLSBytes = 1 << 20 // 1 MiB
	maxHLSLines = 10000
	maxHLSURI   = 4096
)

var (
	errHLSManifestTooLarge = errors.New("hls manifest exceeds size bounds")
	errHLSManifestInvalid  = errors.New("hls manifest is not valid utf-8 text")
	errHLSURIRejected      = errors.New("hls uri rejected")
)

// uriBearingTags are the #EXT tags whose URI="..." attribute must be rewritten.
var uriBearingTags = []string{
	"#EXT-X-KEY",
	"#EXT-X-MAP",
	"#EXT-X-MEDIA",
	"#EXT-X-I-FRAME-STREAM-INF",
	"#EXT-X-SESSION-KEY",
	"#EXT-X-PART",
	"#EXT-X-PRELOAD-HINT",
	"#EXT-X-RENDITION-REPORT",
}

// rewriteHLSManifest parses a bounded media/master playlist and rewrites every
// segment, variant, and URI-bearing attribute to an opaque Telos locator minted
// by mint(resourcePath). Any URI that cannot be proven within the authorized
// item's HLS prefix is rejected and the whole manifest fails closed.
func rewriteHLSManifest(body []byte, itemID string, mint func(resourcePath string) string) ([]byte, error) {
	if len(body) > maxHLSBytes {
		return nil, errHLSManifestTooLarge
	}
	if !utf8.Valid(body) {
		return nil, errHLSManifestInvalid
	}
	text := string(body)
	lines := strings.Split(text, "\n")
	if len(lines) > maxHLSLines {
		return nil, errHLSManifestTooLarge
	}

	var out strings.Builder
	out.Grow(len(text))
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		switch {
		case line == "":
			// preserve blank lines
		case strings.HasPrefix(line, "#"):
			rewritten, err := rewriteTagLine(line, itemID, mint)
			if err != nil {
				return nil, err
			}
			line = rewritten
		default:
			// A bare line is a segment or variant-playlist URI.
			resourcePath, err := resolveHLSURI(line, itemID)
			if err != nil {
				return nil, err
			}
			line = mint(resourcePath)
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return []byte(out.String()), nil
}

// rewriteTagLine rewrites the URI="..." attribute of a known URI-bearing tag.
// Tags with no URI attribute pass through unchanged.
func rewriteTagLine(line, itemID string, mint func(string) string) (string, error) {
	isURIBearing := false
	for _, tag := range uriBearingTags {
		if strings.HasPrefix(line, tag) {
			isURIBearing = true
			break
		}
	}
	idx := strings.Index(line, `URI="`)
	if idx < 0 {
		// No URI attribute. If an unknown tag nonetheless smuggles one in a
		// different form, reject; otherwise pass through.
		if strings.Contains(line, "URI=") {
			return "", errHLSURIRejected
		}
		return line, nil
	}
	if !isURIBearing {
		// A URI on a tag we do not model is not provably safe.
		return "", errHLSURIRejected
	}
	start := idx + len(`URI="`)
	end := strings.IndexByte(line[start:], '"')
	if end < 0 {
		return "", errHLSURIRejected
	}
	inner := line[start : start+end]
	resourcePath, err := resolveHLSURI(inner, itemID)
	if err != nil {
		return "", err
	}
	return line[:start] + mint(resourcePath) + line[start+end:], nil
}

// resolveHLSURI validates one manifest URI and returns the resource path (plus
// any allowlisted, token-free query) relative to the item's HLS prefix. It
// rejects userinfo, fragments, protocol-relative and non-HTTP schemes,
// alternate hosts/IPs, traversal, and anything outside the item's prefix.
func resolveHLSURI(raw, itemID string) (string, error) {
	if raw == "" || len(raw) > maxHLSURI {
		return "", errHLSURIRejected
	}
	if strings.ContainsAny(raw, " \t\r\n\\") {
		return "", errHLSURIRejected
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", errHLSURIRejected
		}
	}
	if strings.HasPrefix(raw, "//") { // protocol-relative
		return "", errHLSURIRejected
	}
	if strings.Contains(raw, "#") { // fragment
		return "", errHLSURIRejected
	}
	// A colon before the first slash is a scheme (or opaque URI like data:...);
	// only the explicit http(s)://origin form below is permitted.
	if c := strings.IndexByte(raw, ':'); c >= 0 && !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		if s := strings.IndexByte(raw, '/'); s < 0 || c < s {
			return "", errHLSURIRejected
		}
	}

	itemPrefix := hlsItemPrefix(itemID)

	var rawPath, rawQuery string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", errHLSURIRejected
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", errHLSURIRejected
		}
		if u.User != nil { // userinfo
			return "", errHLSURIRejected
		}
		if !strings.EqualFold(u.Host, hlsOriginHost) {
			return "", errHLSURIRejected
		}
		rawPath = u.EscapedPath()
		rawQuery = u.RawQuery
	} else if strings.HasPrefix(raw, "/") {
		// Absolute path on the origin.
		if q := strings.IndexByte(raw, '?'); q >= 0 {
			rawPath, rawQuery = raw[:q], raw[q+1:]
		} else {
			rawPath = raw
		}
	} else {
		// Relative to the item base: <prefix>/<raw>.
		if q := strings.IndexByte(raw, '?'); q >= 0 {
			rawPath, rawQuery = itemPrefix+raw[:q], raw[q+1:]
		} else {
			rawPath = itemPrefix + raw
		}
	}

	if !strings.HasPrefix(rawPath, itemPrefix) {
		return "", errHLSURIRejected
	}
	tail := strings.TrimPrefix(rawPath, itemPrefix)
	if !validUpstreamSubpath(tail) {
		return "", errHLSURIRejected
	}

	// Strip upstream tokens: keep only allowlisted, non-secret query keys.
	filtered := filterHLSQuery(rawQuery)
	if filtered != "" {
		return tail + "?" + filtered, nil
	}
	return tail, nil
}

// filterHLSQuery keeps only allowlisted stream query keys (dropping api_key and
// any other upstream token) and re-encodes them canonically.
func filterHLSQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	kept := url.Values{}
	for k := range jellyfinStreamQueryKeys {
		if v := values.Get(k); v != "" && values[k] != nil {
			kept.Set(k, v)
		}
	}
	return kept.Encode()
}

// hlsOriginHost is the host:port of the configured Jellyfin origin. Manifest
// URIs may only reference this exact authority.
var hlsOriginHost = "jellyfin:8096"

// hlsItemPrefix is the on-origin path prefix beneath which an item's HLS
// resources must live.
func hlsItemPrefix(itemID string) string {
	return "/jellyfin/Videos/" + itemID + "/"
}

// mintHLSLocatorURL builds the opaque media-element-facing URL for a resource
// path, bound to the authority that is allowed to keep it alive.
func mintHLSLocatorURL(itemID, userID string, authority mediaAuthority, resourcePath string, exp time.Time) string {
	loc := hlsLocator{
		Item: itemID,
		User: userID,
		Auth: authority,
		Res:  resourcePath,
		Exp:  exp.Unix(),
	}
	return "/api/v1/hls/" + signHLSLocator(loc)
}

// splitResourcePath separates a stored resource path into its subpath and
// validated query for building the upstream request.
func splitResourcePath(res string) (subpath, rawQuery string, err error) {
	subpath = res
	if q := strings.IndexByte(res, '?'); q >= 0 {
		subpath, rawQuery = res[:q], res[q+1:]
	}
	if !validUpstreamSubpath(subpath) {
		return "", "", fmt.Errorf("invalid resource subpath")
	}
	return subpath, rawQuery, nil
}
