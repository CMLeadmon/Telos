package main

// Guided-setup discovery (`telos-core validate-integrations`).
//
// The operator CLI runs this command in a throwaway gateway container on the
// production networks so the upstream credentials are tried over the same paths
// the gateway will use. Contract:
//
//   - exactly one JSON object on stdin (<= 64 KiB, unknown keys and trailing
//     data rejected): {jellyfinToken, grimmoryUser, grimmoryPassword}
//   - success: exactly one JSON object on stdout with the non-secret library
//     lists {jellyfin:[{id,name}], grimmory:[{id,name}]}, exit 0
//   - failure: nothing on stdout; one bounded fixed-vocabulary line on stderr,
//     exit 1 (usage errors exit 2)
//
// Credentials are never logged, echoed, or copied into an error message, and
// upstream response bodies are never reflected. Discovery speaks to Jellyfin and
// Grimmory only over their HTTP APIs, which keeps the GPL/AGPL process boundary
// intact: no upstream code is linked here.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	integrationRequestLimit = 64 << 10
	integrationBodyLimit    = 1 << 20
	defaultJellyfinURL      = "http://jellyfin:8096"
	defaultGrimmoryURL      = "http://grimmory:6060"
)

// integrationHTTPTimeout bounds each whole upstream exchange; tests shorten it.
var integrationHTTPTimeout = 15 * time.Second

type IntegrationValidationRequest struct {
	JellyfinToken    string `json:"jellyfinToken"`
	GrimmoryUser     string `json:"grimmoryUser"`
	GrimmoryPassword string `json:"grimmoryPassword"`
}

type IntegrationLibrary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type IntegrationValidationResponse struct {
	Jellyfin []IntegrationLibrary `json:"jellyfin"`
	Grimmory []IntegrationLibrary `json:"grimmory"`
}

// integrationError carries only a fixed code and message, so nothing an upstream
// or the operator supplied can leak through an error value.
type integrationError struct {
	code    string
	message string
}

func (e *integrationError) Error() string { return e.code + ": " + e.message }

func newIntegrationError(code, message string) error {
	return &integrationError{code: code, message: message}
}

var (
	errIntegrationRequest       = newIntegrationError("invalid_request", "the request must be exactly one JSON object with jellyfinToken, grimmoryUser and grimmoryPassword")
	errJellyfinUnavailable      = newIntegrationError("jellyfin_unavailable", "Jellyfin could not be reached")
	errJellyfinRejected         = newIntegrationError("jellyfin_rejected", "Jellyfin rejected the token or the request")
	errJellyfinInvalidResponse  = newIntegrationError("jellyfin_invalid_response", "Jellyfin returned an unexpected response")
	errJellyfinNoLibraries      = newIntegrationError("jellyfin_no_libraries", "Jellyfin has no libraries; create one first")
	errJellyfinDuplicateLibrary = newIntegrationError("jellyfin_duplicate_library", "Jellyfin returned the same library ID twice")
	errGrimmoryUnavailable      = newIntegrationError("grimmory_unavailable", "Grimmory could not be reached")
	errGrimmoryRejected         = newIntegrationError("grimmory_rejected", "Grimmory rejected the credentials or the request")
	errGrimmoryInvalidResponse  = newIntegrationError("grimmory_invalid_response", "Grimmory returned an unexpected response")
	errGrimmoryNoLibraries      = newIntegrationError("grimmory_no_libraries", "Grimmory has no libraries; create one first")
	errGrimmoryDuplicateLibrary = newIntegrationError("grimmory_duplicate_library", "Grimmory returned the same library ID twice")
	errIntegrationRedirect      = newIntegrationError("redirect_refused", "an upstream tried to redirect the request, which is never followed")
	errIntegrationTooLarge      = newIntegrationError("response_too_large", "an upstream response exceeded the size limit")
	errIntegrationConfig        = newIntegrationError("invalid_configuration", "JELLYFIN_URL or GRIMMORY_URL is not a plain http(s) URL")
	errIntegrationTimeout       = newIntegrationError("timeout", "an upstream did not answer in time")
)

// classifyTransportError separates a timeout from every other connection failure.
func classifyTransportError(err error, unavailable error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return errIntegrationTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errIntegrationTimeout
	}
	return unavailable
}

// redactIntegrationError renders an error for stderr without any caller- or
// upstream-controlled text.
func redactIntegrationError(err error) string {
	var known *integrationError
	if errors.As(err, &known) {
		return known.Error()
	}
	return "unexpected_error: integration validation failed"
}

// integrationClient never follows redirects and never uses a proxy: the request
// carries credentials, and a redirect or proxy hop could deliver them elsewhere.
func integrationClient() *http.Client {
	return &http.Client{
		Timeout:   integrationHTTPTimeout,
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func validateIntegrationBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errIntegrationConfig
	}
	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/"), nil
}

// readIntegrationBody reads a bounded response and classifies failures. A 3xx is
// reported as a refused redirect before any body is trusted.
func readIntegrationBody(resp *http.Response, rejected, unavailable error) ([]byte, error) {
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, errIntegrationRedirect
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, rejected
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, unavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, integrationBodyLimit+1))
	if err != nil {
		return nil, unavailable
	}
	if len(data) > integrationBodyLimit {
		return nil, errIntegrationTooLarge
	}
	return data, nil
}

func doIntegrationRequest(ctx context.Context, client *http.Client, request *http.Request) (*http.Response, error) {
	resp, err := client.Do(request.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func sortIntegrationLibraries(libraries []IntegrationLibrary) {
	sort.SliceStable(libraries, func(i, j int) bool {
		if libraries[i].Name != libraries[j].Name {
			return libraries[i].Name < libraries[j].Name
		}
		return libraries[i].ID < libraries[j].ID
	})
}

func discoverJellyfinLibraries(ctx context.Context, client *http.Client, base, token string) ([]IntegrationLibrary, error) {
	request, err := http.NewRequest(http.MethodGet, base+"/Library/VirtualFolders", nil)
	if err != nil {
		return nil, errJellyfinUnavailable
	}
	request.Header.Set("X-Emby-Token", token)
	request.Header.Set("Accept", "application/json")
	resp, err := doIntegrationRequest(ctx, client, request)
	if err != nil {
		return nil, classifyTransportError(err, errJellyfinUnavailable)
	}
	body, err := readIntegrationBody(resp, errJellyfinRejected, errJellyfinUnavailable)
	if err != nil {
		return nil, err
	}
	var folders []struct {
		ItemID string `json:"ItemId"`
		Name   string `json:"Name"`
	}
	if err := json.Unmarshal(body, &folders); err != nil {
		return nil, errJellyfinInvalidResponse
	}
	seen := make(map[string]bool, len(folders))
	libraries := make([]IntegrationLibrary, 0, len(folders))
	for _, folder := range folders {
		id := strings.TrimSpace(folder.ItemID)
		if id == "" {
			continue
		}
		if seen[id] {
			return nil, errJellyfinDuplicateLibrary
		}
		seen[id] = true
		libraries = append(libraries, IntegrationLibrary{ID: id, Name: strings.TrimSpace(folder.Name)})
	}
	if len(libraries) == 0 {
		return nil, errJellyfinNoLibraries
	}
	sortIntegrationLibraries(libraries)
	return libraries, nil
}

// integrationStableID accepts a JSON string or a bare integer and returns its
// exact text. Floats, booleans, objects and blanks are not stable identifiers.
func integrationStableID(raw json.RawMessage) (string, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return "", false
	}
	if text[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return "", false
		}
		value = strings.TrimSpace(value)
		return value, value != ""
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return "", false
		}
	}
	return text, true
}

func discoverGrimmoryLibraries(ctx context.Context, client *http.Client, base, user, password string) ([]IntegrationLibrary, error) {
	// Credentials travel only in this one login request body.
	payload, err := json.Marshal(map[string]string{"username": user, "password": password})
	if err != nil {
		return nil, errGrimmoryUnavailable
	}
	login, err := http.NewRequest(http.MethodPost, base+"/api/v1/auth/login", bytes.NewReader(payload))
	if err != nil {
		return nil, errGrimmoryUnavailable
	}
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Accept", "application/json")
	resp, err := doIntegrationRequest(ctx, client, login)
	if err != nil {
		return nil, classifyTransportError(err, errGrimmoryUnavailable)
	}
	body, err := readIntegrationBody(resp, errGrimmoryRejected, errGrimmoryUnavailable)
	if err != nil {
		return nil, err
	}
	var session struct {
		AccessToken string `json:"accessToken"`
	}
	if json.Unmarshal(body, &session) != nil || session.AccessToken == "" {
		return nil, errGrimmoryInvalidResponse
	}

	list, err := http.NewRequest(http.MethodGet, base+"/api/v1/libraries", nil)
	if err != nil {
		return nil, errGrimmoryUnavailable
	}
	list.Header.Set("Authorization", "Bearer "+session.AccessToken)
	list.Header.Set("Accept", "application/json")
	resp, err = doIntegrationRequest(ctx, client, list)
	if err != nil {
		return nil, classifyTransportError(err, errGrimmoryUnavailable)
	}
	body, err = readIntegrationBody(resp, errGrimmoryRejected, errGrimmoryUnavailable)
	if err != nil {
		return nil, err
	}
	// The shape below (a JSON array of objects carrying "id" and "name") follows the
	// Phase 2 contract; it has NOT been captured from a live pinned Grimmory. The
	// decoder is strict so a different shape fails closed with
	// grimmory_invalid_response instead of guessing.
	var records []struct {
		ID   json.RawMessage `json:"id"`
		Name string          `json:"name"`
	}
	if json.Unmarshal(body, &records) != nil {
		return nil, errGrimmoryInvalidResponse
	}
	seen := make(map[string]bool, len(records))
	libraries := make([]IntegrationLibrary, 0, len(records))
	for _, record := range records {
		id, ok := integrationStableID(record.ID)
		if !ok {
			return nil, errGrimmoryInvalidResponse
		}
		if seen[id] {
			return nil, errGrimmoryDuplicateLibrary
		}
		seen[id] = true
		libraries = append(libraries, IntegrationLibrary{ID: id, Name: strings.TrimSpace(record.Name)})
	}
	if len(libraries) == 0 {
		return nil, errGrimmoryNoLibraries
	}
	sortIntegrationLibraries(libraries)
	return libraries, nil
}

func validateIntegrationConfig(ctx context.Context, req IntegrationValidationRequest, jellyfinURL, grimmoryURL string) (IntegrationValidationResponse, error) {
	if strings.TrimSpace(req.JellyfinToken) == "" || strings.TrimSpace(req.GrimmoryUser) == "" || req.GrimmoryPassword == "" {
		return IntegrationValidationResponse{}, errIntegrationRequest
	}
	jellyfinBase, err := validateIntegrationBaseURL(jellyfinURL)
	if err != nil {
		return IntegrationValidationResponse{}, err
	}
	grimmoryBase, err := validateIntegrationBaseURL(grimmoryURL)
	if err != nil {
		return IntegrationValidationResponse{}, err
	}
	client := integrationClient()
	jellyfin, err := discoverJellyfinLibraries(ctx, client, jellyfinBase, req.JellyfinToken)
	if err != nil {
		return IntegrationValidationResponse{}, err
	}
	grimmory, err := discoverGrimmoryLibraries(ctx, client, grimmoryBase, req.GrimmoryUser, req.GrimmoryPassword)
	if err != nil {
		return IntegrationValidationResponse{}, err
	}
	return IntegrationValidationResponse{Jellyfin: jellyfin, Grimmory: grimmory}, nil
}

// readIntegrationRequest decodes exactly one request object: bounded, strict on
// unknown keys, and refusing any trailing data.
func readIntegrationRequest(in io.Reader) (IntegrationValidationRequest, error) {
	data, err := io.ReadAll(io.LimitReader(in, integrationRequestLimit+1))
	if err != nil || len(data) > integrationRequestLimit {
		return IntegrationValidationRequest{}, errIntegrationRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var req IntegrationValidationRequest
	if err := decoder.Decode(&req); err != nil {
		return IntegrationValidationRequest{}, errIntegrationRequest
	}
	if _, err := decoder.Token(); err != io.EOF {
		return IntegrationValidationRequest{}, errIntegrationRequest
	}
	return req, nil
}

func runIntegrationValidationCommand(in io.Reader, out, errOut io.Writer) error {
	req, err := readIntegrationRequest(in)
	if err != nil {
		return err
	}
	jellyfinURL := os.Getenv("JELLYFIN_URL")
	if jellyfinURL == "" {
		jellyfinURL = defaultJellyfinURL
	}
	grimmoryURL := os.Getenv("GRIMMORY_URL")
	if grimmoryURL == "" {
		grimmoryURL = defaultGrimmoryURL
	}
	response, err := validateIntegrationConfig(context.Background(), req, jellyfinURL, grimmoryURL)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode response")
	}
	_, err = out.Write(append(encoded, '\n'))
	return err
}

// integrationCommandMain is the process entry for `validate-integrations`. It
// returns the exit code: 0 success, 1 failed validation, 2 usage.
func integrationCommandMain(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errOut, "usage: telos-core validate-integrations < request.json")
		return 2
	}
	var stdout bytes.Buffer
	if err := runIntegrationValidationCommand(in, &stdout, errOut); err != nil {
		fmt.Fprintln(errOut, "integration validation failed:", redactIntegrationError(err))
		return 1
	}
	_, _ = out.Write(stdout.Bytes())
	return 0
}
