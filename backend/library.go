package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════
// Library — Grimmory client (JWT auth)
// ═══════════════════════════════════════════════════════════════════════════

// Grimmory (BookLore) does not accept static API tokens; the gateway logs in
// with admin credentials and holds a short-lived JWT (expires: 7200s).
var grimmoryBaseURL = "http://grimmory:6060"

var grimmoryTok struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

var grimmoryMetadataHTTPClient = func() *http.Client {
	transport := upstreamTransport.Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport}
}()

func grimmoryLogin(ctx context.Context) (string, time.Duration, error) {
	payload, _ := json.Marshal(map[string]string{
		"username": os.Getenv("GRIMMORY_ADMIN_USER"),
		"password": os.Getenv("GRIMMORY_ADMIN_PASSWORD"),
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		grimmoryBaseURL+"/api/v1/auth/login", bytes.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	req = req.WithContext(requestCtx)
	resp, err := upstreamHTTPClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("grimmory login returned %s", resp.Status)
	}
	var body struct {
		AccessToken string `json:"accessToken"`
		Expires     int    `json:"expires"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", 0, err
	}
	if body.AccessToken == "" {
		return "", 0, fmt.Errorf("grimmory login returned empty token")
	}
	return body.AccessToken, time.Duration(body.Expires) * time.Second, nil
}

func getGrimmoryToken(ctx context.Context) (string, error) {
	grimmoryTok.mu.Lock()
	defer grimmoryTok.mu.Unlock()
	// 60s slack so a token never expires mid-request.
	if grimmoryTok.token != "" && time.Now().Before(grimmoryTok.expiresAt.Add(-60*time.Second)) {
		return grimmoryTok.token, nil
	}
	tok, ttl, err := grimmoryLogin(ctx)
	if err != nil {
		return "", err
	}
	grimmoryTok.token = tok
	grimmoryTok.expiresAt = time.Now().Add(ttl)
	return tok, nil
}

// grimmoryRequest performs one explicit authenticated upstream request,
// re-logging-in once on 401 (for example after a Grimmory restart).
func grimmoryRequest(ctx context.Context, client *http.Client, method, path, contentType string, body []byte, extra http.Header) (*http.Response, error) {
	do := func(tok string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, grimmoryBaseURL+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		// Forward only allowlisted range/conditional request headers, never the
		// browser's cookies, Authorization, or arbitrary headers.
		for name := range extra {
			if v := extra.Values(name); len(v) > 0 {
				req.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), v...)
			}
		}
		return client.Do(req)
	}
	tok, err := getGrimmoryToken(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := do(tok)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		grimmoryTok.mu.Lock()
		grimmoryTok.token = ""
		grimmoryTok.mu.Unlock()
		if tok, err = getGrimmoryToken(ctx); err != nil {
			return nil, err
		}
		return do(tok)
	}
	return resp, err
}

func grimmoryGET(ctx context.Context, path string) (*http.Response, error) {
	return grimmoryRequest(ctx, upstreamHTTPClient, http.MethodGet, path, "", nil, nil)
}

// ═══════════════════════════════════════════════════════════════════════════
// Library — books catalog (translated from Grimmory)
// ═══════════════════════════════════════════════════════════════════════════

type LibraryBook struct {
	ID            int64    `json:"id"`
	Title         string   `json:"title"`
	Subtitle      string   `json:"subtitle"`
	Authors       []string `json:"authors"`
	Categories    []string `json:"categories"`
	Language      string   `json:"language"`
	Description   string   `json:"description"`
	SeriesName    string   `json:"seriesName"`
	SeriesNumber  *float64 `json:"seriesNumber"`
	Publisher     string   `json:"publisher"`
	PublishedDate string   `json:"publishedDate"`
	ISBN10        string   `json:"isbn10"`
	ISBN13        string   `json:"isbn13"`
	Format        string   `json:"format"` // primaryFile.bookType: "EPUB" | "PDF"
	FileSizeKB    int64    `json:"fileSizeKb"`
	AddedOn       string   `json:"addedOn"`
	Library       string   `json:"library"`
}

type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type LibraryFacets struct {
	Authors    []FacetValue `json:"authors"`
	Categories []FacetValue `json:"categories"`
	Languages  []FacetValue `json:"languages"`
	Formats    []FacetValue `json:"formats"`
}

// grimmoryBook is the verified upstream shape (BookLore GET /api/v1/books).
type grimmoryBook struct {
	ID          int64  `json:"id"`
	AddedOn     string `json:"addedOn"`
	LibraryName string `json:"libraryName"`
	Metadata    struct {
		Title         string   `json:"title"`
		Subtitle      string   `json:"subtitle"`
		Authors       []string `json:"authors"`
		Categories    []string `json:"categories"`
		Language      string   `json:"language"`
		Description   string   `json:"description"`
		SeriesName    string   `json:"seriesName"`
		SeriesNumber  *float64 `json:"seriesNumber"`
		Publisher     string   `json:"publisher"`
		PublishedDate string   `json:"publishedDate"`
		ISBN10        string   `json:"isbn10"`
		ISBN13        string   `json:"isbn13"`
	} `json:"metadata"`
	PrimaryFile struct {
		BookType   string `json:"bookType"`
		FileSizeKB int64  `json:"fileSizeKb"`
	} `json:"primaryFile"`
}

func libraryBookFromGrimmory(b grimmoryBook) LibraryBook {
	return LibraryBook{
		ID: b.ID, Title: b.Metadata.Title, Subtitle: b.Metadata.Subtitle,
		Authors: b.Metadata.Authors, Categories: b.Metadata.Categories,
		Language: b.Metadata.Language, Description: b.Metadata.Description,
		SeriesName: b.Metadata.SeriesName, SeriesNumber: b.Metadata.SeriesNumber,
		Publisher: b.Metadata.Publisher, PublishedDate: b.Metadata.PublishedDate,
		ISBN10: b.Metadata.ISBN10, ISBN13: b.Metadata.ISBN13,
		Format: b.PrimaryFile.BookType, FileSizeKB: b.PrimaryFile.FileSizeKB,
		AddedOn: b.AddedOn, Library: b.LibraryName,
	}
}

func fetchGrimmoryBooks(ctx context.Context) ([]LibraryBook, error) {
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	resp, err := grimmoryGET(requestCtx, "/api/v1/books")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grimmory books returned %s", resp.Status)
	}
	// Bound the decoded catalog: at most 8 MiB of JSON and 5,000 records so a
	// misbehaving or compromised upstream cannot exhaust memory.
	const maxGrimmoryBytes = 8 << 20
	const maxGrimmoryRecords = 5000
	var raw []grimmoryBook
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxGrimmoryBytes)).Decode(&raw); err != nil {
		return nil, err
	}
	if len(raw) > maxGrimmoryRecords {
		raw = raw[:maxGrimmoryRecords]
	}
	books := make([]LibraryBook, 0, len(raw))
	for _, b := range raw {
		books = append(books, libraryBookFromGrimmory(b))
	}
	return books, nil
}

var errGrimmoryBookNotFound = errors.New("grimmory book not found")

func fetchGrimmoryBook(ctx context.Context, id string) (LibraryBook, error) {
	resp, err := grimmoryGET(ctx, "/api/v1/books/"+id+"?withDescription=true")
	if err != nil {
		return LibraryBook{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return LibraryBook{}, errGrimmoryBookNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return LibraryBook{}, fmt.Errorf("grimmory book returned %s", resp.Status)
	}
	var raw grimmoryBook
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return LibraryBook{}, err
	}
	return libraryBookFromGrimmory(raw), nil
}

// getLibraryBooks serves from Redis (60s), then Grimmory. Upstream failures
// are returned to callers; fabricated catalog entries must never look real or
// be embedded into durable chat history.
func getLibraryBooks(ctx context.Context) ([]LibraryBook, error) {
	const cacheKey = "telos:grimmory:books"
	if redisClient != nil {
		if val, err := redisClient.Get(ctx, cacheKey).Result(); err == nil && val != "" {
			var books []LibraryBook
			if json.Unmarshal([]byte(val), &books) == nil {
				return books, nil
			}
		}
	}
	books, err := fetchGrimmoryBooks(ctx)
	if err != nil {
		log.Printf("WARN: grimmory books unavailable: %v", err)
		return nil, fmt.Errorf("%w: grimmory: %v", errUpstreamUnavailable, err)
	}
	if redisClient != nil {
		if raw, err := json.Marshal(books); err == nil {
			_ = redisClient.Set(ctx, cacheKey, string(raw), 60*time.Second).Err()
		}
	}
	return books, nil
}

func handleLibraryBooks(w http.ResponseWriter, r *http.Request) {
	books, err := getLibraryBooks(r.Context())
	if err != nil {
		http.Error(w, "Grimmory unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(books)
}

func handleLibraryBookByID(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	requestCtx, cancel := upstreamRequestContext(r.Context())
	defer cancel()
	book, err := fetchGrimmoryBook(requestCtx, id)
	if errors.Is(err, errGrimmoryBookNotFound) {
		http.Error(w, "book not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Grimmory unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(book)
}

// Grimmory's /api/v1/books/facets endpoint does not exist in the deployed
// build (500s); facets are derived here from the book list instead.
func handleLibraryFacets(w http.ResponseWriter, r *http.Request) {
	books, err := getLibraryBooks(r.Context())
	if err != nil {
		http.Error(w, "Grimmory unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	count := func(pick func(LibraryBook) []string) []FacetValue {
		m := map[string]int{}
		for _, b := range books {
			for _, v := range pick(b) {
				if v != "" {
					m[v]++
				}
			}
		}
		out := make([]FacetValue, 0, len(m))
		for v, c := range m {
			out = append(out, FacetValue{Value: v, Count: c})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].Value < out[j].Value
		})
		return out
	}
	facets := LibraryFacets{
		Authors:    count(func(b LibraryBook) []string { return b.Authors }),
		Categories: count(func(b LibraryBook) []string { return b.Categories }),
		Languages:  count(func(b LibraryBook) []string { return []string{b.Language} }),
		Formats:    count(func(b LibraryBook) []string { return []string{b.Format} }),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(facets)
}

// ═══════════════════════════════════════════════════════════════════════════
// Library — shared catalog management
//
// Verified against the deployed Grimmory image on 2026-07-16:
//   - metadata updates take {"metadata": ..., "clearFlags": {}} and support
//     replaceMode=REPLACE_WHEN_PROVIDED, which preserves unlisted fields;
//   - prospective metadata takes FetchMetadataRequest and returns SSE;
//   - cover upload uses multipart field "file";
//   - delete takes DELETE /api/v1/books?ids=<id> and removes the file.
// ═══════════════════════════════════════════════════════════════════════════

const maxLibraryCoverBytes = 5 << 20

type LibraryMetadata struct {
	Title         string   `json:"title"`
	Subtitle      string   `json:"subtitle"`
	Authors       []string `json:"authors"`
	Categories    []string `json:"categories"`
	Language      string   `json:"language"`
	Description   string   `json:"description"`
	SeriesName    string   `json:"seriesName"`
	SeriesNumber  *float64 `json:"seriesNumber"`
	Publisher     string   `json:"publisher"`
	PublishedDate string   `json:"publishedDate"`
	ISBN10        string   `json:"isbn10"`
	ISBN13        string   `json:"isbn13"`
}

type LibraryMetadataCandidate struct {
	LibraryMetadata
	Provider string `json:"provider"`
	CoverURL string `json:"coverUrl"`
}

type grimmoryMetadataCandidate struct {
	LibraryMetadata
	Provider     string `json:"provider"`
	ThumbnailURL string `json:"thumbnailUrl"`
	CoverURL     string `json:"coverUrl"`
}

func writeLibraryError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func mapGrimmoryMutationStatus(w http.ResponseWriter, resp *http.Response) bool {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true
	}
	if resp.StatusCode == http.StatusNotFound {
		writeLibraryError(w, http.StatusNotFound, "book not found")
	} else {
		writeLibraryError(w, http.StatusBadGateway, "Grimmory rejected the request")
	}
	return false
}

func invalidateLibraryCache(ctx context.Context) {
	if redisClient == nil {
		return
	}
	var cursor uint64
	for {
		keys, next, err := redisClient.Scan(ctx, cursor, "telos:grimmory:*", 100).Result()
		if err != nil {
			log.Printf("WARN: could not scan Grimmory cache keys: %v", err)
			return
		}
		if len(keys) > 0 {
			if err := redisClient.Del(ctx, keys...).Err(); err != nil {
				log.Printf("WARN: could not invalidate Grimmory cache: %v", err)
				return
			}
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}

func handleUpdateLibraryBookMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		writeLibraryError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	var metadata LibraryMetadata
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&metadata); err != nil {
		writeLibraryError(w, http.StatusBadRequest, "invalid metadata")
		return
	}
	payload, err := json.Marshal(map[string]any{
		"metadata": metadata, "clearFlags": map[string]bool{},
	})
	if err != nil {
		writeLibraryError(w, http.StatusInternalServerError, "could not encode metadata")
		return
	}
	ctx, cancel := upstreamRequestContext(r.Context())
	path := "/api/v1/books/" + id + "/metadata?mergeCategories=false&replaceMode=REPLACE_WHEN_PROVIDED"
	resp, err := grimmoryRequest(ctx, upstreamHTTPClient, http.MethodPut, path, "application/json", payload, nil)
	if err != nil {
		cancel()
		writeLibraryError(w, http.StatusBadGateway, "Grimmory unavailable")
		return
	}
	if !mapGrimmoryMutationStatus(w, resp) {
		resp.Body.Close()
		cancel()
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	cancel()
	invalidateLibraryCache(r.Context())

	reloadCtx, reloadCancel := upstreamRequestContext(r.Context())
	defer reloadCancel()
	book, err := fetchGrimmoryBook(reloadCtx, id)
	if errors.Is(err, errGrimmoryBookNotFound) {
		writeLibraryError(w, http.StatusNotFound, "book not found")
		return
	}
	if err != nil {
		writeLibraryError(w, http.StatusBadGateway, "could not reload updated book")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(book)
}

func normalizeCandidate(raw grimmoryMetadataCandidate) LibraryMetadataCandidate {
	coverURL := raw.CoverURL
	if coverURL == "" {
		coverURL = raw.ThumbnailURL
	}
	return LibraryMetadataCandidate{
		LibraryMetadata: raw.LibraryMetadata,
		Provider:        raw.Provider,
		CoverURL:        coverURL,
	}
}

func decodeProspectiveMetadata(body io.Reader) ([]LibraryMetadataCandidate, error) {
	raw, err := io.ReadAll(io.LimitReader(body, 4<<20))
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return []LibraryMetadataCandidate{}, nil
	}

	var upstream []grimmoryMetadataCandidate
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &upstream); err != nil {
			return nil, err
		}
	} else if trimmed[0] == '{' {
		var one grimmoryMetadataCandidate
		if err := json.Unmarshal(trimmed, &one); err != nil {
			return nil, err
		}
		upstream = append(upstream, one)
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(trimmed))
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var candidate grimmoryMetadataCandidate
			if err := json.Unmarshal([]byte(data), &candidate); err != nil {
				return nil, err
			}
			upstream = append(upstream, candidate)
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	candidates := make([]LibraryMetadataCandidate, 0, len(upstream))
	for _, candidate := range upstream {
		candidates = append(candidates, normalizeCandidate(candidate))
	}
	return candidates, nil
}

func handleFetchLibraryBookMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		writeLibraryError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	// These are every MetadataProvider enum value in the deployed image.
	payload, _ := json.Marshal(map[string]any{"providers": []string{
		"Amazon", "GoodReads", "Google", "Hardcover", "Comicvine",
		"Douban", "Lubimyczytac", "Ranobedb", "Audible",
	}})
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := grimmoryRequest(ctx, grimmoryMetadataHTTPClient, http.MethodPost,
		"/api/v1/books/"+id+"/metadata/prospective", "application/json", payload, nil)
	if err != nil {
		writeLibraryError(w, http.StatusBadGateway, "metadata providers unavailable")
		return
	}
	defer resp.Body.Close()
	if !mapGrimmoryMutationStatus(w, resp) {
		return
	}
	candidates, err := decodeProspectiveMetadata(resp.Body)
	if err != nil {
		writeLibraryError(w, http.StatusBadGateway, "invalid metadata provider response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"candidates": candidates})
}

func acceptedCoverType(data []byte) (string, bool) {
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/jpeg", "image/png":
		return contentType, true
	default:
		return "", false
	}
}

func readUploadedCover(w http.ResponseWriter, r *http.Request) ([]byte, string, int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLibraryCoverBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, "", http.StatusBadRequest, err
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", http.StatusBadRequest, err
		}
		if part.FormName() != "cover" {
			part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, maxLibraryCoverBytes+1))
		part.Close()
		if err != nil {
			return nil, "", http.StatusBadRequest, err
		}
		if len(data) > maxLibraryCoverBytes {
			return nil, "", http.StatusRequestEntityTooLarge, errors.New("cover exceeds 5 MB")
		}
		contentType, ok := acceptedCoverType(data)
		if !ok {
			return nil, "", http.StatusUnsupportedMediaType, errors.New("cover must be JPEG or PNG")
		}
		return data, contentType, 0, nil
	}
	return nil, "", http.StatusBadRequest, errors.New("missing cover file")
}

func candidateCoverHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := upstreamTransport.Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		probe := &url.URL{Scheme: "http", Host: net.JoinHostPort(host, port)}
		ips, err := publicURLIPs(ctx, probe)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many cover redirects")
		}
		if req.URL.Scheme != via[len(via)-1].URL.Scheme && req.URL.Scheme != "https" {
			return errors.New("cover redirect downgrades the scheme")
		}
		_, err := publicURLIPs(req.Context(), req.URL)
		return err
	}
	return client
}

func downloadCandidateCover(ctx context.Context, raw string) ([]byte, string, int, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, "", http.StatusBadRequest, errors.New("invalid cover URL")
	}
	if _, err := publicURLIPs(ctx, parsed); err != nil {
		return nil, "", http.StatusBadRequest, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", http.StatusBadRequest, err
	}
	resp, err := candidateCoverHTTPClient().Do(req)
	if err != nil {
		return nil, "", http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", http.StatusBadGateway, errors.New("cover source rejected request")
	}
	if resp.ContentLength > maxLibraryCoverBytes {
		return nil, "", http.StatusRequestEntityTooLarge, errors.New("cover exceeds 5 MB")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLibraryCoverBytes+1))
	if err != nil {
		return nil, "", http.StatusBadGateway, err
	}
	if len(data) > maxLibraryCoverBytes {
		return nil, "", http.StatusRequestEntityTooLarge, errors.New("cover exceeds 5 MB")
	}
	contentType, ok := acceptedCoverType(data)
	if !ok {
		return nil, "", http.StatusUnsupportedMediaType, errors.New("cover must be JPEG or PNG")
	}
	return data, contentType, 0, nil
}

func forwardCoverToGrimmory(ctx context.Context, id string, data []byte, contentType string) (*http.Response, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	headers := textproto.MIMEHeader{}
	headers.Set("Content-Disposition", `form-data; name="file"; filename="cover"`)
	headers.Set("Content-Type", contentType)
	part, err := writer.CreatePart(headers)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return grimmoryRequest(ctx, upstreamHTTPClient, http.MethodPost,
		"/api/v1/books/"+id+"/metadata/cover/upload", writer.FormDataContentType(), body.Bytes(), nil)
}

func handleUpdateLibraryBookCover(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		writeLibraryError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	var data []byte
	var contentType string
	var status int
	var err error
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	switch mediaType {
	case "multipart/form-data":
		data, contentType, status, err = readUploadedCover(w, r)
	case "application/json":
		var body struct {
			CoverURL string `json:"coverUrl"`
		}
		decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body)
		if decodeErr != nil || body.CoverURL == "" {
			writeLibraryError(w, http.StatusBadRequest, "invalid cover URL")
			return
		}
		data, contentType, status, err = downloadCandidateCover(r.Context(), body.CoverURL)
	default:
		writeLibraryError(w, http.StatusUnsupportedMediaType, "cover must be multipart or JSON")
		return
	}
	if err != nil {
		writeLibraryError(w, status, err.Error())
		return
	}
	ctx, cancel := upstreamRequestContext(r.Context())
	defer cancel()
	resp, err := forwardCoverToGrimmory(ctx, id, data, contentType)
	if err != nil {
		writeLibraryError(w, http.StatusBadGateway, "Grimmory unavailable")
		return
	}
	defer resp.Body.Close()
	if !mapGrimmoryMutationStatus(w, resp) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleDeleteLibraryBook(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		writeLibraryError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	if !authorizeBookHTTP(w, r, id, BookManage) {
		return
	}
	ctx, cancel := upstreamRequestContext(r.Context())
	defer cancel()
	resp, err := grimmoryRequest(ctx, upstreamHTTPClient, http.MethodDelete,
		"/api/v1/books?ids="+url.QueryEscape(id), "", nil, nil)
	if err != nil {
		writeLibraryError(w, http.StatusBadGateway, "Grimmory unavailable")
		return
	}
	defer resp.Body.Close()
	if !mapGrimmoryMutationStatus(w, resp) {
		return
	}
	invalidateLibraryCache(r.Context())
	if dbPool != nil {
		if _, err := dbPool.Exec(r.Context(), `DELETE FROM book_progress WHERE book_id = $1`, id); err != nil {
			writeLibraryError(w, http.StatusInternalServerError, "book deleted but progress cleanup failed")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ═══════════════════════════════════════════════════════════════════════════
// Library — cover & content proxies
// ═══════════════════════════════════════════════════════════════════════════

// grimmoryBinaryResponseHeaders are the upstream response headers propagated for
// a book binary. Range/validator headers must survive so 206/304 work.
var grimmoryBinaryResponseHeaders = []string{
	"Content-Range", "Accept-Ranges", "Content-Length", "ETag", "Last-Modified",
}

func proxyGrimmoryBinary(w http.ResponseWriter, r *http.Request, path, forceContentType string) {
	// No total-transfer deadline: a large EPUB/PDF over a slow client must not be
	// aborted mid-stream. The upstream client keeps a bounded response-header
	// timeout, and r.Context() cancellation closes the body on client disconnect.
	extra := http.Header{}
	for name := range rangeRequestHeaders {
		if v := r.Header.Values(name); len(v) > 0 {
			extra[http.CanonicalHeaderKey(name)] = append([]string(nil), v...)
		}
	}
	resp, err := grimmoryRequest(r.Context(), upstreamHTTPClient, http.MethodGet, path, "", nil, extra)
	if err != nil {
		log.Printf("library: grimmory binary fetch failed request_id=%s: %v", requestIDFrom(r.Context()), err)
		writeAPIError(w, r, http.StatusBadGateway, "upstream_unavailable", "The book service is unavailable.")
		return
	}
	defer resp.Body.Close()
	// Accept OK, Partial Content, and Not Modified; anything else is an error.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusNotModified {
		log.Printf("library: grimmory binary status request_id=%s: %s", requestIDFrom(r.Context()), resp.Status)
		writeAPIError(w, r, http.StatusBadGateway, "upstream_error", "The book service returned an error.")
		return
	}
	ct := resp.Header.Get("Content-Type")
	if forceContentType != "" {
		ct = forceContentType
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	for _, name := range grimmoryBinaryResponseHeaders {
		if v := resp.Header.Values(name); len(v) > 0 {
			w.Header()[http.CanonicalHeaderKey(name)] = append([]string(nil), v...)
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead && resp.StatusCode != http.StatusNotModified {
		io.Copy(w, resp.Body)
	}
}

func libraryBookID(r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if id == "" {
		return "", false
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return "", false
	}
	return id, true
}

func handleLibraryBookCover(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	// Upstream serves JPEG bytes with a JSON content-type — force the real one.
	proxyGrimmoryBinary(w, r, "/api/v1/media/book/"+id+"/thumbnail", "image/jpeg")
}

func handleLibraryBookContent(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	if !authorizeBookHTTP(w, r, id, BookRead) {
		return
	}
	proxyGrimmoryBinary(w, r, "/api/v1/books/"+id+"/content", "")
}

// ═══════════════════════════════════════════════════════════════════════════
// Library — per-user reading progress (Telos Postgres, not Grimmory: the
// gateway is a single admin account upstream, so progress must be local)
// ═══════════════════════════════════════════════════════════════════════════

func validateProgress(raw []byte) ([]byte, float64, error) {
	if len(raw) > 4096 {
		return nil, 0, errors.New("progress payload too large")
	}
	var body struct {
		Locator json.RawMessage `json:"locator"`
		Percent float64         `json:"percent"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, 0, errors.New("invalid JSON")
	}
	if body.Percent < 0 || body.Percent > 1 {
		return nil, 0, errors.New("percent must be between 0 and 1")
	}
	locator := []byte("{}")
	if len(body.Locator) > 0 {
		var probe map[string]any
		if err := json.Unmarshal(body.Locator, &probe); err != nil {
			return nil, 0, errors.New("locator must be a JSON object")
		}
		if len(body.Locator) > 2048 {
			return nil, 0, errors.New("locator too large")
		}
		locator = body.Locator
	}
	return locator, body.Percent, nil
}

func handleGetBookProgress(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	var locator []byte
	var percent float64
	var updated time.Time
	err := dbPool.QueryRow(r.Context(), `
		SELECT locator, percent, updated_at FROM book_progress
		WHERE user_id = $1 AND book_id = $2
	`, user.ID, id).Scan(&locator, &percent, &updated)
	w.Header().Set("Content-Type", "application/json")
	if err != nil { // no row yet — zero progress
		w.Write([]byte(`{"locator":{},"percent":0,"updatedAt":null}`))
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"locator": json.RawMessage(locator), "percent": percent,
		"updatedAt": updated.Format(time.RFC3339),
	})
}

func handlePutBookProgress(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	locator, percent, err := validateProgress(raw)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := dbPool.Exec(r.Context(), `
		INSERT INTO book_progress (user_id, book_id, locator, percent, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id, book_id)
		DO UPDATE SET locator = EXCLUDED.locator, percent = EXCLUDED.percent, updated_at = NOW()
	`, user.ID, id, locator, percent); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
