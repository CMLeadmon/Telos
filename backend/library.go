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
	"math"
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

	"github.com/jackc/pgx/v5"
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
	ID            string   `json:"id"`
	UpstreamID    int64    `json:"-"`
	LibraryID     string   `json:"-"`
	Narrator      string   `json:"-"`
	DurationMS    int64    `json:"-"`
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
	ID          int64           `json:"id"`
	LibraryID   json.RawMessage `json:"libraryId"`
	AddedOn     string          `json:"addedOn"`
	LibraryName string          `json:"libraryName"`
	Metadata    struct {
		Title             string   `json:"title"`
		Subtitle          string   `json:"subtitle"`
		Authors           []string `json:"authors"`
		Categories        []string `json:"categories"`
		Language          string   `json:"language"`
		Description       string   `json:"description"`
		SeriesName        string   `json:"seriesName"`
		SeriesNumber      *float64 `json:"seriesNumber"`
		Publisher         string   `json:"publisher"`
		PublishedDate     string   `json:"publishedDate"`
		ISBN10            string   `json:"isbn10"`
		ISBN13            string   `json:"isbn13"`
		Narrator          string   `json:"narrator"`
		AudiobookMetadata struct {
			DurationSeconds int64 `json:"durationSeconds"`
		} `json:"audiobookMetadata"`
	} `json:"metadata"`
	PrimaryFile struct {
		BookType   string `json:"bookType"`
		FileSizeKB int64  `json:"fileSizeKb"`
	} `json:"primaryFile"`
}

func libraryBookFromGrimmory(b grimmoryBook) LibraryBook {
	book := LibraryBook{
		UpstreamID: b.ID, LibraryID: grimmoryLibraryID(b.LibraryID),
		Title: b.Metadata.Title, Subtitle: b.Metadata.Subtitle,
		Authors: b.Metadata.Authors, Categories: b.Metadata.Categories,
		Language: b.Metadata.Language, Description: b.Metadata.Description,
		SeriesName: b.Metadata.SeriesName, SeriesNumber: b.Metadata.SeriesNumber,
		Publisher: b.Metadata.Publisher, PublishedDate: b.Metadata.PublishedDate,
		ISBN10: b.Metadata.ISBN10, ISBN13: b.Metadata.ISBN13,
		Narrator: b.Metadata.Narrator,
		Format:   b.PrimaryFile.BookType, FileSizeKB: b.PrimaryFile.FileSizeKB,
		AddedOn: b.AddedOn, Library: b.LibraryName,
	}
	if seconds := b.Metadata.AudiobookMetadata.DurationSeconds; seconds > 0 && seconds <= math.MaxInt64/1000 {
		book.DurationMS = seconds * 1000
	}
	return book
}

func grimmoryLibraryID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func grimmoryCatalogKind(format string) (CatalogKind, bool) {
	kind, err := libraryKindFromGrimmory(format)
	return kind, err == nil
}

func allowedGrimmoryLibraryID(libraryID string) (string, bool) {
	if libraryID == "" {
		return "", false
	}
	if grimmoryAuthorizer == nil {
		return libraryID, true
	}
	_, ok := grimmoryAuthorizer.allowed[libraryID]
	return libraryID, ok
}

var errUnsupportedGrimmoryFormat = errors.New("unsupported Grimmory book format")

func canonicalizeGrimmoryBook(ctx context.Context, book LibraryBook) (LibraryBook, error) {
	kind, ok := grimmoryCatalogKind(book.Format)
	if !ok {
		return LibraryBook{}, errUnsupportedGrimmoryFormat
	}
	libraryID, ok := allowedGrimmoryLibraryID(book.LibraryID)
	if !ok {
		return LibraryBook{}, errBookNotAuthorized
	}
	resolution, err := observeCatalogIdentity(ctx, CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: strconv.FormatInt(book.UpstreamID, 10),
		LibraryID: libraryID, Surface: SurfaceLibrary, Kind: kind,
	})
	if err != nil {
		return LibraryBook{}, err
	}
	current, err := resolveCatalogIdentity(ctx, resolution.ID, SurfaceLibrary)
	if err != nil || current.Provider != ProviderGrimmory || current.UpstreamID != strconv.FormatInt(book.UpstreamID, 10) ||
		!current.Active || !current.Available {
		return LibraryBook{}, errBookNotAuthorized
	}
	book.ID = current.ID
	book.LibraryID = libraryID
	return book, nil
}

func fetchGrimmoryBooks(ctx context.Context) ([]LibraryBook, error) {
	started := catalogMetricNow()
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	resp, err := grimmoryGET(requestCtx, "/api/v1/books")
	if err != nil {
		_ = RecordCatalogEnumerationFailure(ProviderGrimmory, catalogMetricSince(started))
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = RecordCatalogEnumerationFailure(ProviderGrimmory, catalogMetricSince(started))
		return nil, fmt.Errorf("grimmory books returned %s", resp.Status)
	}
	// Bound the decoded catalog: at most 8 MiB of JSON and 5,000 records so a
	// misbehaving or compromised upstream cannot exhaust memory.
	const maxGrimmoryBytes = 8 << 20
	const maxGrimmoryRecords = maxMemberProgressBatchItems
	var raw []grimmoryBook
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxGrimmoryBytes)).Decode(&raw); err != nil {
		_ = RecordCatalogEnumerationFailure(ProviderGrimmory, catalogMetricSince(started))
		return nil, err
	}
	if len(raw) > maxGrimmoryRecords {
		_ = RecordCatalogEnumerationFailure(ProviderGrimmory, catalogMetricSince(started))
		return nil, errors.New("Grimmory catalog enumeration exceeded the safe record limit")
	}
	_ = observeCatalogReconcileDuration(ProviderGrimmory, CatalogOperationEnumerate, CatalogResultSuccess, catalogMetricSince(started))
	books := make([]LibraryBook, 0, len(raw))
	observations := make([]CatalogObservation, 0, len(raw))
	unsupported := 0
	librarySet := map[string]struct{}{}
	if grimmoryAuthorizer != nil {
		for libraryID := range grimmoryAuthorizer.allowed {
			librarySet[libraryID] = struct{}{}
		}
	}
	for _, b := range raw {
		mapped := libraryBookFromGrimmory(b)
		kind, supported := grimmoryCatalogKind(mapped.Format)
		libraryID, allowed := allowedGrimmoryLibraryID(mapped.LibraryID)
		if !supported {
			unsupported++
			continue
		}
		if !allowed {
			continue
		}
		librarySet[libraryID] = struct{}{}
		observations = append(observations, CatalogObservation{
			Provider: ProviderGrimmory, UpstreamID: strconv.FormatInt(mapped.UpstreamID, 10),
			LibraryID: libraryID, Surface: SurfaceLibrary, Kind: kind,
		})

		book, err := canonicalizeGrimmoryBook(ctx, mapped)
		if errors.Is(err, errBookNotAuthorized) || errors.Is(err, errUnsupportedGrimmoryFormat) {
			continue
		}
		if err != nil {
			return nil, err
		}
		books = append(books, book)
	}
	if unsupported > 0 {
		log.Printf("catalog normalization provider=grimmory unsupported=%d", unsupported)
	}
	libraries := make([]string, 0, len(librarySet))
	for libraryID := range librarySet {
		libraries = append(libraries, libraryID)
	}
	report, err := reconcileCatalogEnumeration(ctx, CatalogEnumeration{
		Provider: ProviderGrimmory, Libraries: libraries, Observations: observations,
	})
	if err != nil {
		return nil, err
	}
	log.Printf("catalog reconciliation provider=grimmory observed=%d scans=%d backfill_updated=%d", report.Observed, len(report.Scans), report.Backfill.Updated)
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

// getLibraryBooks returns the complete current provider catalog. The previous
// whole-list Redis value had no source revision in its key, so it could serve
// stale metadata after an active-source switch. Item-scoped caches may be
// introduced only with canonical ID + catalog revision keys.
func getLibraryBooks(ctx context.Context) ([]LibraryBook, error) {
	books, err := fetchGrimmoryBooks(ctx)
	if err != nil {
		log.Printf("WARN: grimmory books unavailable: %v", err)
		return nil, fmt.Errorf("%w: grimmory: %v", errUpstreamUnavailable, err)
	}
	return books, nil
}

func handleLibraryBooks(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	catalog := &GrimmoryCatalog{repo: catalogRepo, continuity: continuityRepo}
	books, err := catalog.ListItems(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "Grimmory unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(books)
}

func handleLibraryBookByID(w http.ResponseWriter, r *http.Request) {
	rawID, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	_, book, ok := resolveGrimmoryBookHTTP(w, r, rawID, BookRead)
	if !ok {
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
	rawID, ok := libraryBookID(r)
	if !ok {
		writeLibraryError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	resolution, _, ok := resolveGrimmoryBookHTTP(w, r, rawID, BookManage)
	if !ok {
		return
	}
	id := resolution.UpstreamID
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
	resolution, book, err = validateCurrentGrimmoryBook(r.Context(), resolution, book)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
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
	rawID, ok := libraryBookID(r)
	if !ok {
		writeLibraryError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	resolution, _, ok := resolveGrimmoryBookHTTP(w, r, rawID, BookManage)
	if !ok {
		return
	}
	id := resolution.UpstreamID
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
		// Re-validate the hostname allowlist on every redirect so a redirect
		// cannot escape the approved provider set.
		if !coverHostAllowed(req.URL.Host) {
			return errors.New("cover redirect leaves the approved provider set")
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
	if !coverHostAllowed(parsed.Host) {
		return nil, "", http.StatusBadRequest, errors.New("cover host is not an approved provider")
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
	rawID, ok := libraryBookID(r)
	if !ok {
		writeLibraryError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	resolution, _, ok := resolveGrimmoryBookHTTP(w, r, rawID, BookManage)
	if !ok {
		return
	}
	id := resolution.UpstreamID
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
	rawID, ok := libraryBookID(r)
	if !ok {
		writeLibraryError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	resolution, _, ok := resolveGrimmoryBookHTTP(w, r, rawID, BookManage)
	if !ok {
		return
	}
	id := resolution.UpstreamID
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
		tx, err := dbPool.Begin(r.Context())
		if err != nil {
			writeLibraryError(w, http.StatusInternalServerError, "book deleted but progress cleanup failed")
			return
		}
		defer tx.Rollback(r.Context())
		if _, err := tx.Exec(r.Context(), `DELETE FROM book_progress WHERE book_id = $1`, id); err != nil {
			writeLibraryError(w, http.StatusInternalServerError, "book deleted but progress cleanup failed")
			return
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM member_progress WHERE catalog_item_id = $1::uuid`, resolution.ID); err != nil {
			writeLibraryError(w, http.StatusInternalServerError, "book deleted but progress cleanup failed")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
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
	if looksLikeUUID(id) {
		return id, true
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return "", false
	}
	return id, true
}

func currentGrimmoryBook(ctx context.Context, resolution CatalogResolution) (CatalogResolution, LibraryBook, error) {
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	book, err := fetchGrimmoryBook(requestCtx, resolution.UpstreamID)
	if err != nil {
		return CatalogResolution{}, LibraryBook{}, errBookNotAuthorized
	}
	return validateCurrentGrimmoryBook(ctx, resolution, book)
}

func validateCurrentGrimmoryBook(ctx context.Context, resolution CatalogResolution, book LibraryBook) (CatalogResolution, LibraryBook, error) {
	if strconv.FormatInt(book.UpstreamID, 10) != resolution.UpstreamID {
		return CatalogResolution{}, LibraryBook{}, errBookNotAuthorized
	}
	kind, supported := grimmoryCatalogKind(book.Format)
	libraryID, allowed := allowedGrimmoryLibraryID(book.LibraryID)
	if !supported || !allowed || kind != resolution.Kind {
		return CatalogResolution{}, LibraryBook{}, errBookNotAuthorized
	}
	observed, err := observeCatalogIdentity(ctx, CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: resolution.UpstreamID,
		LibraryID: libraryID, Surface: SurfaceLibrary, Kind: kind,
	})
	if err != nil || observed.ID != resolution.ID || observed.Provider != ProviderGrimmory ||
		!observed.Active || !observed.Available {
		return CatalogResolution{}, LibraryBook{}, errBookNotAuthorized
	}
	book.ID, book.LibraryID = observed.ID, observed.LibraryID
	return observed, book, nil
}

func discoverLegacyGrimmoryBook(ctx context.Context, upstreamID string) (CatalogResolution, error) {
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	book, err := fetchGrimmoryBook(requestCtx, upstreamID)
	if err != nil || strconv.FormatInt(book.UpstreamID, 10) != upstreamID {
		return CatalogResolution{}, errBookNotAuthorized
	}
	kind, supported := grimmoryCatalogKind(book.Format)
	libraryID, allowed := allowedGrimmoryLibraryID(book.LibraryID)
	if !supported || !allowed {
		return CatalogResolution{}, errBookNotAuthorized
	}
	return observeCatalogIdentity(ctx, CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: upstreamID, LibraryID: libraryID,
		Surface: SurfaceLibrary, Kind: kind,
	})
}

func resolveGrimmoryBookHTTP(w http.ResponseWriter, r *http.Request, rawID string, action BookAction) (CatalogResolution, LibraryBook, bool) {
	resolution, err := resolveCatalogIdentity(r.Context(), rawID, SurfaceLibrary)
	if errors.Is(err, errCatalogNotFound) && !looksLikeUUID(rawID) {
		resolution, err = discoverLegacyGrimmoryBook(r.Context(), rawID)
	}
	if err != nil || resolution.Provider != ProviderGrimmory || !resolution.Active || !resolution.Available {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return CatalogResolution{}, LibraryBook{}, false
	}
	resolution, book, err := currentGrimmoryBook(r.Context(), resolution)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return CatalogResolution{}, LibraryBook{}, false
	}
	if !authorizeBookHTTP(w, r, resolution.UpstreamID, action) {
		return CatalogResolution{}, LibraryBook{}, false
	}
	return resolution, book, true
}

func handleLibraryBookCover(w http.ResponseWriter, r *http.Request) {
	rawID, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	resolution, _, ok := resolveGrimmoryBookHTTP(w, r, rawID, BookRead)
	if !ok {
		return
	}
	// Upstream serves JPEG bytes with a JSON content-type — force the real one.
	proxyGrimmoryBinary(w, r, "/api/v1/media/book/"+resolution.UpstreamID+"/thumbnail", "image/jpeg")
}

func handleLibraryBookContent(w http.ResponseWriter, r *http.Request) {
	rawID, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	resolution, _, ok := resolveGrimmoryBookHTTP(w, r, rawID, BookRead)
	if !ok {
		return
	}
	proxyGrimmoryBinary(w, r, "/api/v1/books/"+resolution.UpstreamID+"/content", "")
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

// validateBookProgress server-validates a reading position against the book's
// format. An EPUB locator must be {cfi:string, fraction:0..1}; a PDF locator must
// be {page:>=1, zoom:0.1..10}. Mixed keys, nonfinite, negative, or out-of-range
// values are rejected so no client can persist an incoherent position.
func validateBookProgress(format string, raw []byte) ([]byte, float64, error) {
	locator, percent, err := validateProgress(raw)
	if err != nil {
		return nil, 0, err
	}
	var loc map[string]json.RawMessage
	if err := json.Unmarshal(locator, &loc); err != nil {
		return nil, 0, errors.New("locator must be a JSON object")
	}

	_, hasCFI := loc["cfi"]
	_, hasFraction := loc["fraction"]
	_, hasPage := loc["page"]
	_, hasZoom := loc["zoom"]

	switch strings.ToLower(format) {
	case "epub":
		if hasPage || hasZoom {
			return nil, 0, errors.New("epub locator must not carry pdf fields")
		}
		var cfi string
		if err := json.Unmarshal(loc["cfi"], &cfi); err != nil || strings.TrimSpace(cfi) == "" || len(cfi) > 1024 {
			return nil, 0, errors.New("epub locator requires a valid cfi")
		}
		if err := validateFractionField(loc["fraction"]); err != nil {
			return nil, 0, err
		}
	case "pdf":
		if hasCFI || hasFraction {
			return nil, 0, errors.New("pdf locator must not carry epub fields")
		}
		if err := validatePDFPageField(loc["page"]); err != nil {
			return nil, 0, err
		}
		if err := validateZoomField(loc["zoom"]); err != nil {
			return nil, 0, err
		}
	default:
		return nil, 0, errors.New("unsupported book format")
	}
	return locator, percent, nil
}

func validateFractionField(raw json.RawMessage) error {
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return errors.New("fraction must be a number")
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
		return errors.New("fraction must be between 0 and 1")
	}
	return nil
}

func validatePDFPageField(raw json.RawMessage) error {
	var p float64
	if err := json.Unmarshal(raw, &p); err != nil {
		return errors.New("page must be a number")
	}
	if math.IsNaN(p) || math.IsInf(p, 0) || p < 1 || p != math.Trunc(p) || p > 100000 {
		return errors.New("page must be a positive integer")
	}
	return nil
}

func validateZoomField(raw json.RawMessage) error {
	var z float64
	if err := json.Unmarshal(raw, &z); err != nil {
		return errors.New("zoom must be a number")
	}
	if math.IsNaN(z) || math.IsInf(z, 0) || z < 0.1 || z > 10 {
		return errors.New("zoom must be between 0.1 and 10")
	}
	return nil
}

func handleGetBookProgress(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	rawID, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	resolution, _, ok := resolveGrimmoryBookHTTP(w, r, rawID, BookRead)
	if !ok {
		return
	}
	progress, err := getMemberContinuity(r.Context(), user.ID, resolution.ID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if progress.UpdatedAt == nil && (resolution.Kind == "epub" || resolution.Kind == "pdf") {
		var locator json.RawMessage
		var percent float64
		var updated time.Time
		err = dbPool.QueryRow(r.Context(), `
			SELECT locator, percent, updated_at FROM book_progress
			WHERE user_id = $1 AND book_id = $2
		`, user.ID, resolution.UpstreamID).Scan(&locator, &percent, &updated)
		if err == nil {
			progress, err = putMemberContinuity(r.Context(), user.ID, resolution.ID, ProgressInput{
				Locator: locator, Percent: percent, Completed: percent >= 1,
			})
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if len(progress.Locator) == 0 {
		progress.Locator = json.RawMessage(`{}`)
	}
	json.NewEncoder(w).Encode(progress)
}

func handlePutBookProgress(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	rawID, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	resolution, _, ok := resolveGrimmoryBookHTTP(w, r, rawID, BookRead)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	var input ProgressInput
	if err := json.Unmarshal(raw, &input); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	input, err = ValidateProgress(resolution.Kind, input)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var progress MemberProgress
	if resolution.Kind == "epub" || resolution.Kind == "pdf" {
		tx, err := dbPool.Begin(r.Context())
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback(r.Context())

		progress, err = NewContinuityRepository(tx).Put(r.Context(), user.ID, resolution.ID, input)
		if err == nil {
			_, err = tx.Exec(r.Context(), `
			INSERT INTO book_progress (user_id, book_id, locator, percent, updated_at)
			VALUES ($1, $2, $3, $4, NOW())
			ON CONFLICT (user_id, book_id)
			DO UPDATE SET locator = EXCLUDED.locator, percent = EXCLUDED.percent, updated_at = NOW()
		`, user.ID, resolution.UpstreamID, input.Locator, input.Percent)
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	} else {
		progress, err = putMemberContinuity(r.Context(), user.ID, resolution.ID, input)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(progress)
}
