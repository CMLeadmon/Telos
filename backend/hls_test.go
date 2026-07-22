package main

import (
	"strings"
	"testing"
	"time"
)

func init() {
	// A fixed test signing key so locator sign/verify is deterministic.
	hlsSigningKey = []byte("0123456789abcdef0123456789abcdef")
}

// recordingMint captures resource paths and returns an easily-asserted marker.
func recordingMint(seen *[]string) func(string) string {
	return func(res string) string {
		*seen = append(*seen, res)
		return "LOC:" + res
	}
}

func TestHLSLocatorRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	loc := hlsLocator{Item: "item1", User: "u1", Sess: "s1", Res: "hls/0.ts", Exp: now.Add(hlsLocatorTTL).Unix()}
	token := signHLSLocator(loc)

	got, err := verifyHLSLocator(token, now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != loc {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", got, loc)
	}

	// Expired.
	if _, err := verifyHLSLocator(token, now.Add(hlsLocatorTTL+time.Second)); err != errHLSLocatorExpired {
		t.Fatalf("expected expired, got %v", err)
	}

	// Tamper: flip a byte in the body.
	bad := "A" + token[1:]
	if _, err := verifyHLSLocator(bad, now); err == nil {
		t.Fatal("tampered locator accepted")
	}

	// Wrong number of parts.
	if _, err := verifyHLSLocator("nodot", now); err != errHLSLocatorInvalid {
		t.Fatalf("expected invalid, got %v", err)
	}
}

func TestHLSSessionBindingDistinct(t *testing.T) {
	if hlsSessionBinding("tokenA") == hlsSessionBinding("tokenB") {
		t.Fatal("session bindings collide across tokens")
	}
	if hlsSessionBinding("tokenA") != hlsSessionBinding("tokenA") {
		t.Fatal("session binding is not stable")
	}
}

func TestRewriteMediaPlaylistStripsTokens(t *testing.T) {
	manifest := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:7",
		"#EXT-X-TARGETDURATION:6",
		`#EXT-X-MAP:URI="hls1/main/init.mp4?api_key=SECRET&runtimeTicks=0"`,
		`#EXT-X-KEY:METHOD=AES-128,URI="https://jellyfin:8096/jellyfin/Videos/item123/key?api_key=SECRET"`,
		"#EXTINF:6.0,",
		"hls1/main/0.ts?api_key=SECRET&runtimeTicks=1000",
		"#EXTINF:6.0,",
		"/jellyfin/Videos/item123/hls1/main/1.ts",
		"#EXT-X-ENDLIST",
		"",
	}, "\n")

	var seen []string
	out, err := rewriteHLSManifest([]byte(manifest), "item123", recordingMint(&seen))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	text := string(out)

	// No upstream host or token survives.
	if strings.Contains(text, "jellyfin:8096") {
		t.Fatal("upstream host leaked into manifest")
	}
	if strings.Contains(text, "api_key") || strings.Contains(text, "SECRET") {
		t.Fatal("upstream token leaked into manifest")
	}
	// Every URI became a locator.
	if strings.Count(text, "LOC:") != 4 {
		t.Fatalf("expected 4 rewritten URIs, got %d\n%s", strings.Count(text, "LOC:"), text)
	}
	// Allowlisted query (runtimeTicks) is preserved; the token is dropped.
	wantRes := map[string]bool{
		"hls1/main/init.mp4?runtimeTicks=0": true,
		"key":                               true,
		"hls1/main/0.ts?runtimeTicks=1000":  true,
		"hls1/main/1.ts":                    true,
	}
	if len(seen) != 4 {
		t.Fatalf("captured %d resources, want 4: %v", len(seen), seen)
	}
	for _, r := range seen {
		if !wantRes[r] {
			t.Fatalf("unexpected resource path %q (seen=%v)", r, seen)
		}
	}
	// Structural tag lines are preserved.
	if !strings.Contains(text, "#EXT-X-TARGETDURATION:6") || !strings.Contains(text, "#EXT-X-ENDLIST") {
		t.Fatal("structural tags were dropped")
	}
}

func TestRewriteMasterPlaylist(t *testing.T) {
	manifest := strings.Join([]string{
		"#EXTM3U",
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a",NAME="en",URI="audio/en.m3u8"`,
		`#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=100,URI="iframe/0.m3u8"`,
		"#EXT-X-STREAM-INF:BANDWIDTH=800000",
		"variant/720.m3u8",
		"",
	}, "\n")
	var seen []string
	out, err := rewriteHLSManifest([]byte(manifest), "item123", recordingMint(&seen))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if strings.Count(string(out), "LOC:") != 3 {
		t.Fatalf("expected 3 rewrites, got %q", out)
	}
}

func TestRewriteRejectsUnsafeURIs(t *testing.T) {
	cases := map[string]string{
		"alternate host":    "https://evil.example.com/jellyfin/Videos/item123/0.ts",
		"userinfo":          "https://user:pass@jellyfin:8096/jellyfin/Videos/item123/0.ts",
		"non-http scheme":   "file:///etc/passwd",
		"data scheme":       "data:text/plain,hi",
		"protocol relative": "//evil.example.com/0.ts",
		"fragment":          "hls/0.ts#frag",
		"traversal":         "../../../etc/passwd",
		"outside prefix":    "/jellyfin/Videos/OTHERITEM/0.ts",
		"absolute off-item": "https://jellyfin:8096/jellyfin/System/Info",
	}
	for name, uri := range cases {
		manifest := "#EXTM3U\n#EXTINF:6.0,\n" + uri + "\n"
		if _, err := rewriteHLSManifest([]byte(manifest), "item123", func(s string) string { return s }); err == nil {
			t.Errorf("%s: unsafe URI %q was accepted", name, uri)
		}
	}
}

func TestRewriteRejectsUnknownURIBearingTag(t *testing.T) {
	manifest := "#EXTM3U\n#EXT-X-CUSTOM-EVIL:URI=\"hls/0.ts\"\n"
	if _, err := rewriteHLSManifest([]byte(manifest), "item123", func(s string) string { return s }); err != errHLSURIRejected {
		t.Fatalf("expected URI rejection on unknown tag, got %v", err)
	}
}

func TestRewriteRejectsOversizeAndBinary(t *testing.T) {
	big := make([]byte, maxHLSBytes+1)
	for i := range big {
		big[i] = 'A'
	}
	if _, err := rewriteHLSManifest(big, "item123", func(s string) string { return s }); err != errHLSManifestTooLarge {
		t.Fatalf("expected too-large, got %v", err)
	}
	// Too many lines.
	many := "#EXTM3U\n" + strings.Repeat("#\n", maxHLSLines+1)
	if _, err := rewriteHLSManifest([]byte(many), "item123", func(s string) string { return s }); err != errHLSManifestTooLarge {
		t.Fatalf("expected too-many-lines, got %v", err)
	}
	// Invalid UTF-8.
	if _, err := rewriteHLSManifest([]byte("#EXTM3U\n\xff\xfe\n"), "item123", func(s string) string { return s }); err != errHLSManifestInvalid {
		t.Fatalf("expected invalid utf-8, got %v", err)
	}
}

func TestSplitResourcePath(t *testing.T) {
	sub, q, err := splitResourcePath("hls1/main/0.ts?runtimeTicks=5")
	if err != nil || sub != "hls1/main/0.ts" || q != "runtimeTicks=5" {
		t.Fatalf("split = (%q,%q,%v)", sub, q, err)
	}
	if _, _, err := splitResourcePath("../escape"); err == nil {
		t.Fatal("traversal resource path accepted")
	}
}
