package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	canaryJellyfinToken = "canary-jellyfin-token-9f3a"
	canaryGrimmoryUser  = "canary-gateway-user"
	canaryGrimmoryPass  = "canary-grimmory-pass-7c1d"
)

func canaryRequest() IntegrationValidationRequest {
	return IntegrationValidationRequest{JellyfinToken: canaryJellyfinToken, GrimmoryUser: canaryGrimmoryUser, GrimmoryPassword: canaryGrimmoryPass}
}

type integrationUpstreamLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *integrationUpstreamLog) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}

func (l *integrationUpstreamLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

// jellyfinFakeOptions shapes the fake Jellyfin beyond its library listing. base
// is the BaseUrl Jellyfin is already serving under ("" = a fresh install).
type jellyfinFakeOptions struct {
	base        string
	networkBody string // GET /System/Configuration/network; default carries BaseUrl and an extra field
	getStatus   int
	postStatus  int
}

// integrationFakeJellyfin is a fresh Jellyfin: it serves from the root and
// records every request, with the token it was shown.
func integrationFakeJellyfin(t *testing.T, log *integrationUpstreamLog, body string, status int) *httptest.Server {
	t.Helper()
	return integrationFakeJellyfinWith(t, log, body, status, jellyfinFakeOptions{})
}

// integrationFakeJellyfinWith emulates the Jellyfin endpoints setup touches:
// public info, the library listing and the network settings. Like the real
// server it ignores a saved BaseUrl until restarted, so a POST changes nothing
// about where the fake answers.
func integrationFakeJellyfinWith(t *testing.T, log *integrationUpstreamLog, body string, status int, opts jellyfinFakeOptions) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add("jellyfin %s %s token=%q", r.Method, r.URL.Path, r.Header.Get("X-Emby-Token"))
		path := r.URL.Path
		if opts.base != "" {
			if !strings.HasPrefix(path, opts.base+"/") {
				http.Redirect(w, r, opts.base+path, http.StatusFound)
				return
			}
			path = strings.TrimPrefix(path, opts.base)
		}
		switch {
		case path == "/System/Info/Public" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"ProductName":"Jellyfin Server"}`)
		case path == "/System/Configuration/network":
			if r.Header.Get("X-Emby-Token") != canaryJellyfinToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodPost {
				var posted map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&posted)
				log.add("jellyfin network saved BaseUrl=%s InternalHttpPort=%s Extra=%s", posted["BaseUrl"], posted["InternalHttpPort"], posted["Extra"])
				if opts.postStatus != 0 {
					w.WriteHeader(opts.postStatus)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if opts.getStatus != 0 {
				w.WriteHeader(opts.getStatus)
				return
			}
			networkBody := opts.networkBody
			if networkBody == "" {
				networkBody = fmt.Sprintf(`{"BaseUrl":%q,"InternalHttpPort":8096,"Extra":{"keep":true}}`, opts.base)
			}
			_, _ = io.WriteString(w, networkBody)
		case path == "/Library/VirtualFolders" && r.Method == http.MethodGet:
			if r.Header.Get("X-Emby-Token") != canaryJellyfinToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

const jellyfinSample = `[
 {"Name":"Movies","ItemId":"b3","Locations":["/data/media/movies"],"CollectionType":"movies"},
 {"Name":"Anime","ItemId":"a1"},
 {"Name":"Blank","ItemId":""},
 {"Name":"Anime","ItemId":"0a"}
]`

func integrationFakeGrimmory(t *testing.T, log *integrationUpstreamLog, libraries string, loginStatus, listStatus int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login":
			var credentials map[string]string
			_ = json.NewDecoder(r.Body).Decode(&credentials)
			log.add("grimmory login user=%q password=%q auth=%q", credentials["username"], credentials["password"], r.Header.Get("Authorization"))
			if loginStatus != http.StatusOK || credentials["username"] != canaryGrimmoryUser || credentials["password"] != canaryGrimmoryPass {
				w.WriteHeader(map[bool]int{true: loginStatus, false: http.StatusUnauthorized}[loginStatus != http.StatusOK])
				// An upstream that echoes what it received must still never leak through us.
				_, _ = io.WriteString(w, "bad credentials for "+credentials["username"]+"/"+credentials["password"])
				return
			}
			_, _ = io.WriteString(w, `{"accessToken":"session-token-abc","expires":7200}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/libraries":
			log.add("grimmory libraries auth=%q", r.Header.Get("Authorization"))
			if r.Header.Get("Authorization") != "Bearer session-token-abc" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(listStatus)
			_, _ = io.WriteString(w, libraries)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func discover(t *testing.T, jellyfin, grimmory *httptest.Server) (IntegrationValidationResponse, error) {
	t.Helper()
	return validateIntegrationConfig(context.Background(), canaryRequest(), jellyfin.URL, grimmory.URL)
}

func TestValidateIntegrationsMapsAndSortsStableLibraries(t *testing.T) {
	log := &integrationUpstreamLog{}
	jellyfin := integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK)
	grimmory := integrationFakeGrimmory(t, log, `[{"id":7,"name":"Books"},{"id":"3","name":"Audiobooks"},{"id":11,"name":"Books"}]`, http.StatusOK, http.StatusOK)
	response, err := discover(t, jellyfin, grimmory)
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	wantJellyfin := []IntegrationLibrary{{ID: "0a", Name: "Anime"}, {ID: "a1", Name: "Anime"}, {ID: "b3", Name: "Movies"}}
	wantGrimmory := []IntegrationLibrary{{ID: "3", Name: "Audiobooks"}, {ID: "11", Name: "Books"}, {ID: "7", Name: "Books"}}
	if fmt.Sprint(response.Jellyfin) != fmt.Sprint(wantJellyfin) {
		t.Errorf("jellyfin = %v, want %v (blank IDs removed, sorted by name then ID)", response.Jellyfin, wantJellyfin)
	}
	if fmt.Sprint(response.Grimmory) != fmt.Sprint(wantGrimmory) {
		t.Errorf("grimmory = %v, want %v", response.Grimmory, wantGrimmory)
	}
	text := log.text()
	if !strings.Contains(text, `jellyfin GET /Library/VirtualFolders token="`+canaryJellyfinToken+`"`) {
		t.Errorf("Jellyfin did not receive GET /Library/VirtualFolders with the exact token:\n%s", text)
	}
	if !strings.Contains(text, `grimmory libraries auth="Bearer session-token-abc"`) {
		t.Errorf("Grimmory libraries was not called with the session bearer:\n%s", text)
	}
}

func TestGrimmoryCredentialsAppearOnlyInTheLoginBody(t *testing.T) {
	log := &integrationUpstreamLog{}
	jellyfin := integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK)
	grimmory := integrationFakeGrimmory(t, log, `[{"id":1,"name":"Books"}]`, http.StatusOK, http.StatusOK)
	if _, err := discover(t, jellyfin, grimmory); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(log.text(), "\n") {
		if strings.HasPrefix(line, "grimmory libraries") && (strings.Contains(line, canaryGrimmoryPass) || strings.Contains(line, canaryGrimmoryUser)) {
			t.Errorf("credentials leaked past the login request: %s", line)
		}
		if strings.HasPrefix(line, "jellyfin") && (strings.Contains(line, canaryGrimmoryPass) || strings.Contains(line, canaryGrimmoryUser)) {
			t.Errorf("Grimmory credentials were sent to Jellyfin: %s", line)
		}
	}
}

// The gateway reaches Jellyfin at /jellyfin and its HLS rewriting depends on
// that prefix, but a fresh Jellyfin serves from the root. Setup closes the gap by
// saving BaseUrl, and must change nothing else about Jellyfin's network settings.
func TestSetupMakesAFreshJellyfinServeUnderTheGatewayPrefix(t *testing.T) {
	log := &integrationUpstreamLog{}
	jellyfin := integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK)
	grimmory := integrationFakeGrimmory(t, log, `[{"id":1,"name":"Books"}]`, http.StatusOK, http.StatusOK)
	if _, err := discover(t, jellyfin, grimmory); err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	text := log.text()
	if got := strings.Count(text, "jellyfin network saved"); got != 1 {
		t.Fatalf("expected exactly one network save, got %d:\n%s", got, text)
	}
	if !strings.Contains(text, `jellyfin network saved BaseUrl="/jellyfin" InternalHttpPort=8096 Extra={"keep":true}`) {
		t.Errorf("the save must set BaseUrl and preserve the other fields:\n%s", text)
	}
	if strings.Index(text, "VirtualFolders") > strings.Index(text, "network saved") {
		t.Errorf("Jellyfin was modified before the token was proven by a successful listing:\n%s", text)
	}
}

func TestSetupLeavesAnAlreadyPrefixedJellyfinAlone(t *testing.T) {
	log := &integrationUpstreamLog{}
	jellyfin := integrationFakeJellyfinWith(t, log, jellyfinSample, http.StatusOK, jellyfinFakeOptions{base: "/jellyfin"})
	grimmory := integrationFakeGrimmory(t, log, `[{"id":1,"name":"Books"}]`, http.StatusOK, http.StatusOK)
	response, err := discover(t, jellyfin, grimmory)
	if err != nil {
		t.Fatalf("a configured node must still validate: %v", err)
	}
	if len(response.Jellyfin) != 3 {
		t.Errorf("libraries were not discovered under the prefix: %v", response.Jellyfin)
	}
	if strings.Contains(log.text(), "network saved") {
		t.Errorf("an already-prefixed Jellyfin must not be modified:\n%s", log.text())
	}
	if !strings.Contains(log.text(), "jellyfin GET /jellyfin/Library/VirtualFolders") {
		t.Errorf("discovery did not use the prefix:\n%s", log.text())
	}
}

func TestSetupDoesNotTouchJellyfinWhenTheTokenIsRejected(t *testing.T) {
	log := &integrationUpstreamLog{}
	jellyfin := integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK)
	grimmory := integrationFakeGrimmory(t, log, `[{"id":1,"name":"Books"}]`, http.StatusOK, http.StatusOK)
	_, err := validateIntegrationConfig(context.Background(),
		IntegrationValidationRequest{JellyfinToken: "wrong", GrimmoryUser: canaryGrimmoryUser, GrimmoryPassword: canaryGrimmoryPass}, jellyfin.URL, grimmory.URL)
	if !errors.Is(err, errJellyfinRejected) {
		t.Fatalf("error = %v, want jellyfin_rejected", err)
	}
	if strings.Contains(log.text(), "network") {
		t.Errorf("settings were read or written with a rejected token:\n%s", log.text())
	}
}

func TestSetupReportsAJellyfinThatWillNotAcceptTheBaseURL(t *testing.T) {
	cases := []struct {
		name string
		opts jellyfinFakeOptions
		want error
	}{
		{"save refused", jellyfinFakeOptions{postStatus: http.StatusForbidden}, errJellyfinRejected},
		{"save fails", jellyfinFakeOptions{postStatus: http.StatusInternalServerError}, errJellyfinBasePath},
		{"settings unreadable", jellyfinFakeOptions{getStatus: http.StatusInternalServerError}, errJellyfinUnavailable},
		{"settings shaped unexpectedly", jellyfinFakeOptions{networkBody: `{"InternalHttpPort":8096}`}, errJellyfinInvalidResponse},
		{"settings not an object", jellyfinFakeOptions{networkBody: `[]`}, errJellyfinInvalidResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &integrationUpstreamLog{}
			jellyfin := integrationFakeJellyfinWith(t, log, jellyfinSample, http.StatusOK, tc.opts)
			grimmory := integrationFakeGrimmory(t, log, `[{"id":1,"name":"Books"}]`, http.StatusOK, http.StatusOK)
			_, err := discover(t, jellyfin, grimmory)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateIntegrationsFailuresAreDistinctAndFixed(t *testing.T) {
	good := func(t *testing.T, log *integrationUpstreamLog) *httptest.Server {
		return integrationFakeGrimmory(t, log, `[{"id":1,"name":"Books"}]`, http.StatusOK, http.StatusOK)
	}
	oversize := `[{"Name":"` + strings.Repeat("x", integrationBodyLimit+10) + `","ItemId":"1"}]`
	cases := []struct {
		name string
		run  func(t *testing.T) error
		want *integrationError
	}{
		{"jellyfin rejects the token", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := validateIntegrationConfig(context.Background(), IntegrationValidationRequest{JellyfinToken: "wrong", GrimmoryUser: canaryGrimmoryUser, GrimmoryPassword: canaryGrimmoryPass},
				integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK).URL, good(t, log).URL)
			return err
		}, errJellyfinRejected.(*integrationError)},
		{"jellyfin server error", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, "boom", http.StatusInternalServerError), good(t, log))
			return err
		}, errJellyfinUnavailable.(*integrationError)},
		{"jellyfin invalid JSON", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, "<html>", http.StatusOK), good(t, log))
			return err
		}, errJellyfinInvalidResponse.(*integrationError)},
		{"jellyfin empty library set", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, `[{"Name":"x","ItemId":""}]`, http.StatusOK), good(t, log))
			return err
		}, errJellyfinNoLibraries.(*integrationError)},
		{"jellyfin duplicate ID", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, `[{"Name":"a","ItemId":"1"},{"Name":"b","ItemId":"1"}]`, http.StatusOK), good(t, log))
			return err
		}, errJellyfinDuplicateLibrary.(*integrationError)},
		{"jellyfin oversize body", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, oversize, http.StatusOK), good(t, log))
			return err
		}, errIntegrationTooLarge.(*integrationError)},
		{"grimmory rejects the credentials", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK), integrationFakeGrimmory(t, log, `[]`, http.StatusUnauthorized, http.StatusOK))
			return err
		}, errGrimmoryRejected.(*integrationError)},
		{"grimmory libraries server error", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK), integrationFakeGrimmory(t, log, `[]`, http.StatusOK, http.StatusBadGateway))
			return err
		}, errGrimmoryUnavailable.(*integrationError)},
		{"grimmory unexpected shape", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK), integrationFakeGrimmory(t, log, `{"libraries":[{"id":1}]}`, http.StatusOK, http.StatusOK))
			return err
		}, errGrimmoryInvalidResponse.(*integrationError)},
		{"grimmory unstable ID types", func(t *testing.T) error {
			for _, body := range []string{`[{"id":1.5,"name":"x"}]`, `[{"id":true,"name":"x"}]`, `[{"id":null,"name":"x"}]`, `[{"id":"","name":"x"}]`, `[{"name":"x"}]`, `[{"id":{"a":1},"name":"x"}]`} {
				log := &integrationUpstreamLog{}
				_, err := discover(t, integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK), integrationFakeGrimmory(t, log, body, http.StatusOK, http.StatusOK))
				if !errors.Is(err, errGrimmoryInvalidResponse) {
					t.Errorf("body %s: got %v, want grimmory_invalid_response", body, err)
				}
			}
			return errGrimmoryInvalidResponse
		}, errGrimmoryInvalidResponse.(*integrationError)},
		{"grimmory empty library set", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK), integrationFakeGrimmory(t, log, `[]`, http.StatusOK, http.StatusOK))
			return err
		}, errGrimmoryNoLibraries.(*integrationError)},
		{"grimmory duplicate ID", func(t *testing.T) error {
			log := &integrationUpstreamLog{}
			_, err := discover(t, integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK), integrationFakeGrimmory(t, log, `[{"id":1,"name":"a"},{"id":"1","name":"b"}]`, http.StatusOK, http.StatusOK))
			return err
		}, errGrimmoryDuplicateLibrary.(*integrationError)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			var got *integrationError
			if !errors.As(err, &got) || got.code != tc.want.code {
				t.Fatalf("error = %v, want code %s", err, tc.want.code)
			}
			rendered := redactIntegrationError(err)
			for _, secret := range []string{canaryJellyfinToken, canaryGrimmoryPass, canaryGrimmoryUser, "session-token-abc"} {
				if strings.Contains(rendered, secret) {
					t.Fatalf("error output leaked a secret: %q", rendered)
				}
			}
		})
	}
}

func TestValidateIntegrationsNeverFollowsRedirects(t *testing.T) {
	var attackerHits atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerHits.Add(1)
		_, _ = io.WriteString(w, `[{"Name":"stolen","ItemId":"x"}]`)
	}))
	defer attacker.Close()
	redirect := func(status int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", attacker.URL+r.URL.Path)
			w.WriteHeader(status)
		}))
	}
	log := &integrationUpstreamLog{}
	good := integrationFakeGrimmory(t, log, `[{"id":1,"name":"Books"}]`, http.StatusOK, http.StatusOK)
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		jellyfin := redirect(status)
		_, err := validateIntegrationConfig(context.Background(), canaryRequest(), jellyfin.URL, good.URL)
		jellyfin.Close()
		if !errors.Is(err, errIntegrationRedirect) {
			t.Errorf("status %d: error = %v, want redirect_refused", status, err)
		}
	}
	jellyfinOK := integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK)
	grimmoryRedirect := redirect(http.StatusTemporaryRedirect)
	defer grimmoryRedirect.Close()
	if _, err := validateIntegrationConfig(context.Background(), canaryRequest(), jellyfinOK.URL, grimmoryRedirect.URL); !errors.Is(err, errIntegrationRedirect) {
		t.Errorf("Grimmory redirect error = %v", err)
	}
	if attackerHits.Load() != 0 {
		t.Fatalf("a redirect target received %d request(s); credentials could have followed", attackerHits.Load())
	}
}

func TestValidateIntegrationsTimesOut(t *testing.T) {
	previous := integrationHTTPTimeout
	integrationHTTPTimeout = 150 * time.Millisecond
	defer func() { integrationHTTPTimeout = previous }()
	done := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()
	defer close(done)
	log := &integrationUpstreamLog{}
	good := integrationFakeGrimmory(t, log, `[{"id":1,"name":"Books"}]`, http.StatusOK, http.StatusOK)
	_, err := validateIntegrationConfig(context.Background(), canaryRequest(), slow.URL, good.URL)
	if !errors.Is(err, errIntegrationTimeout) {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestValidateIntegrationsRejectsUnsafeBaseURLsAndBlankInputs(t *testing.T) {
	for _, bad := range []string{"ftp://x", "http://user:pw@host:1", "http://host:1/path", "http://host:1?x=1", "", "host:1", "http://"} {
		if _, err := validateIntegrationConfig(context.Background(), canaryRequest(), bad, "http://grimmory:6060"); !errors.Is(err, errIntegrationConfig) {
			t.Errorf("Jellyfin URL %q: error = %v, want invalid_configuration", bad, err)
		}
	}
	blank := []IntegrationValidationRequest{
		{GrimmoryUser: "u", GrimmoryPassword: "p"},
		{JellyfinToken: "t", GrimmoryPassword: "p"},
		{JellyfinToken: "t", GrimmoryUser: "u"},
		{JellyfinToken: "   ", GrimmoryUser: "u", GrimmoryPassword: "p"},
	}
	for _, request := range blank {
		if _, err := validateIntegrationConfig(context.Background(), request, "http://j:1", "http://g:1"); !errors.Is(err, errIntegrationRequest) {
			t.Errorf("request %+v: error = %v, want invalid_request", request, err)
		}
	}
}

func TestIntegrationCommandStdinContract(t *testing.T) {
	log := &integrationUpstreamLog{}
	jellyfin := integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK)
	grimmory := integrationFakeGrimmory(t, log, `[{"id":1,"name":"Books"}]`, http.StatusOK, http.StatusOK)
	t.Setenv("JELLYFIN_URL", jellyfin.URL)
	t.Setenv("GRIMMORY_URL", grimmory.URL)
	valid := `{"jellyfinToken":"` + canaryJellyfinToken + `","grimmoryUser":"` + canaryGrimmoryUser + `","grimmoryPassword":"` + canaryGrimmoryPass + `"}`

	var out, errOut bytes.Buffer
	if code := integrationCommandMain(nil, strings.NewReader(valid+"\n"), &out, &errOut); code != 0 {
		t.Fatalf("valid request exited %d: %s", code, errOut.String())
	}
	var parsed IntegrationValidationResponse
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil || len(parsed.Jellyfin) != 3 || len(parsed.Grimmory) != 1 {
		t.Fatalf("stdout is not exactly one response object: %q (%v)", out.String(), err)
	}
	if strings.Count(strings.TrimSpace(out.String()), "\n") != 0 {
		t.Errorf("stdout must be one line: %q", out.String())
	}
	for _, secret := range []string{canaryJellyfinToken, canaryGrimmoryPass, canaryGrimmoryUser, "session-token-abc"} {
		if strings.Contains(out.String()+errOut.String(), secret) {
			t.Errorf("command output leaked %q", secret)
		}
	}

	rejected := map[string]string{
		"trailing JSON":      valid + `{"jellyfinToken":"x"}`,
		"trailing garbage":   valid + "garbage",
		"unknown key":        `{"jellyfinToken":"t","grimmoryUser":"u","grimmoryPassword":"p","extra":1}`,
		"selection smuggled": `{"jellyfinToken":"t","grimmoryUser":"u","grimmoryPassword":"p","jellyfinLibraryIds":["1"]}`,
		"not an object":      `["x"]`,
		"empty":              ``,
		"oversize":           `{"jellyfinToken":"` + strings.Repeat("a", integrationRequestLimit) + `","grimmoryUser":"u","grimmoryPassword":"p"}`,
		"wrong types":        `{"jellyfinToken":1,"grimmoryUser":"u","grimmoryPassword":"p"}`,
	}
	for name, input := range rejected {
		out.Reset()
		errOut.Reset()
		if code := integrationCommandMain(nil, strings.NewReader(input), &out, &errOut); code != 1 {
			t.Errorf("%s: exit %d, want 1", name, code)
		}
		if out.Len() != 0 {
			t.Errorf("%s: stdout must stay empty on failure, got %q", name, out.String())
		}
		if !strings.Contains(errOut.String(), "invalid_request") {
			t.Errorf("%s: stderr = %q", name, errOut.String())
		}
	}

	out.Reset()
	errOut.Reset()
	if code := integrationCommandMain([]string{"--extra"}, strings.NewReader(valid), &out, &errOut); code != 2 || out.Len() != 0 {
		t.Errorf("an argument after the mode must be a usage error, got exit %d", code)
	}

	grimmory.Close()
	out.Reset()
	errOut.Reset()
	if code := integrationCommandMain(nil, strings.NewReader(valid), &out, &errOut); code != 1 || out.Len() != 0 {
		t.Errorf("an unreachable upstream must exit 1 with empty stdout, got %d %q", code, out.String())
	}
	if strings.Contains(errOut.String(), canaryGrimmoryPass) || strings.Contains(errOut.String(), "127.0.0.1") {
		t.Errorf("stderr leaked secret or address: %q", errOut.String())
	}
}

func TestRedactIntegrationErrorHidesUnknownErrors(t *testing.T) {
	rendered := redactIntegrationError(fmt.Errorf("dial tcp 10.0.0.1: password=%s", canaryGrimmoryPass))
	if strings.Contains(rendered, canaryGrimmoryPass) || strings.Contains(rendered, "10.0.0.1") {
		t.Fatalf("unknown errors must be replaced, got %q", rendered)
	}
}

func TestValidateIntegrationsDispatchPrecedesStartup(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	whole := string(source)
	start := strings.Index(whole, "func main() {")
	if start < 0 {
		t.Fatal("main.go has no func main")
	}
	text := whole[start:]
	dispatch := strings.Index(text, `mode == "validate-integrations"`)
	if dispatch < 0 {
		t.Fatal("main.go does not dispatch validate-integrations")
	}
	for _, startup := range []string{"validateSecrets()", "initDatabase", "pgxpool", "redis.NewClient", "loadCursorKeys", "loadHLS"} {
		if index := strings.Index(text, startup); index >= 0 && index < dispatch {
			t.Errorf("%s runs before the validate-integrations dispatch", startup)
		}
	}
	if index := strings.Index(text, "validateSecrets()"); index < dispatch {
		t.Error("validateSecrets must run after the validate-integrations dispatch")
	}
}

func TestSyntheticGrimmoryFixtureIsMarkedSynthetic(t *testing.T) {
	data, err := os.ReadFile("testdata/integrations/grimmory-libraries.synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Provenance string            `json:"provenance"`
		Response   []json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fixture.Provenance, "SYNTHETIC") {
		t.Fatal("the checked-in Grimmory fixture must say it is synthetic until a live capture replaces it")
	}
	body, _ := json.Marshal(fixture.Response)
	log := &integrationUpstreamLog{}
	response, err := discover(t, integrationFakeJellyfin(t, log, jellyfinSample, http.StatusOK), integrationFakeGrimmory(t, log, string(body), http.StatusOK, http.StatusOK))
	if err != nil || len(response.Grimmory) != 2 {
		t.Fatalf("fixture did not decode: %v %v", response, err)
	}
}
