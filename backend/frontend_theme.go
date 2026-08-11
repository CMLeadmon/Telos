//go:build embedfrontend

package main

import (
	"bytes"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Theme is applied by a data-theme attribute on <html>. The static export is
// built with the default baked in, so without help the first paint is always
// the dark theme and ThemeSync corrects it after hydration — a visible flash
// for anyone on the light theme. The client mirrors its theme into a cookie;
// this stamps that value into the served document so the first paint is right.
//
// The cookie is a paint-time hint only. Server preferences remain the source
// of truth and still correct the client after load.

const themeCookieName = "telos_theme"

const defaultThemeAttr = `data-theme="synthwave"`

// validThemeCookies is an exact allow-list. The cookie is attacker-controlled
// input that ends up inside an HTML attribute, so nothing outside this set is
// ever interpolated — no escaping, no sanitizing, just a fixed set.
var validThemeCookies = map[string]bool{"synthwave": true, "ink": true}

func themeFromCookie(r *http.Request) string {
	c, err := r.Cookie(themeCookieName)
	if err != nil || !validThemeCookies[c.Value] {
		return ""
	}
	return c.Value
}

// documentRequest reports whether a path should be treated as an HTML document.
// The export serves routes as extensionless paths or directory indexes; every
// asset carries a real extension. Restricting rewrites this way keeps us from
// touching stylesheets, which legitimately contain data-theme selectors.
func documentRequest(p string) bool {
	ext := path.Ext(p)
	return ext == "" || ext == ".html"
}

// themeCapture buffers a response so the body can be rewritten before it is
// flushed. Only used for document requests, so assets never pay for it.
type themeCapture struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (c *themeCapture) WriteHeader(status int) { c.status = status }

func (c *themeCapture) Write(b []byte) (int, error) { return c.buf.Write(b) }

func themedFrontend(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		theme := themeFromCookie(r)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		if !documentRequest(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// A 304 carries no body to stamp, and net/http strips Content-Type from
		// it, so the theme rewrite and the Vary below would both be skipped: a
		// client that cached the document before choosing a theme would keep
		// revalidating into the default-theme copy — exactly the flash this
		// middleware exists to remove. Documents are small and there are a
		// handful of them; assets, which are the ones worth revalidating, never
		// reach this middleware. Today the embedded FS reports a zero modtime
		// so net/http emits no validator and never answers 304 on its own, but
		// that is a property of embed.FS, not of this handler — serving the
		// same export from a real directory restores both.
		conditional := r
		if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" || r.Header.Get("If-Range") != "" {
			conditional = r.Clone(r.Context())
			conditional.Header.Del("If-None-Match")
			conditional.Header.Del("If-Modified-Since")
			conditional.Header.Del("If-Range")
		}

		cap := &themeCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cap, conditional)

		body := cap.buf.Bytes()
		isHTML := strings.HasPrefix(cap.Header().Get("Content-Type"), "text/html")

		// Any document response can differ by cookie, including the untouched
		// default — without this a cached copy could be served to the other
		// theme's viewers.
		if isHTML {
			cap.Header().Add("Vary", "Cookie")
		}

		if isHTML && theme != "" && theme != "synthwave" {
			body = bytes.Replace(body, []byte(defaultThemeAttr),
				[]byte(`data-theme="`+theme+`"`), 1)
			// The bytes no longer match what the validators describe.
			cap.Header().Del("ETag")
			cap.Header().Del("Last-Modified")
		}

		cap.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(cap.status)
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	})
}
