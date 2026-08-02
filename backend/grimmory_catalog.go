package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

var (
	errUnsupportedLibraryKind     = errors.New("unsupported Grimmory Library kind")
	errLibraryProviderUnavailable = errors.New("Library provider unavailable")
	errLibraryCatalogInternal     = errors.New("Library catalog internal failure")
)

func libraryProviderError(err error) error {
	return fmt.Errorf("%w: %v", errLibraryProviderUnavailable, err)
}

func libraryCatalogError(err error) error {
	return fmt.Errorf("%w: %v", errLibraryCatalogInternal, err)
}

// LibraryItem is the member-facing Library contract. Provider identifiers and
// library names deliberately stay in the adapter's internal LibraryBook DTO.
type LibraryItem struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Subtitle      string      `json:"subtitle"`
	Authors       []string    `json:"authors"`
	Narrator      string      `json:"narrator,omitempty"`
	Categories    []string    `json:"categories"`
	Description   string      `json:"description"`
	Language      string      `json:"language"`
	SeriesName    string      `json:"seriesName"`
	SeriesNumber  *float64    `json:"seriesNumber"`
	PublishedDate string      `json:"publishedDate"`
	AddedOn       string      `json:"addedOn"`
	// Publisher and the ISBNs are bibliographic, not provider-internal, so they
	// belong in the member-facing contract. They are also load-bearing: the
	// manage modal prefills its form from this shape and PUTs the whole form
	// back under Grimmory's REPLACE_WHEN_PROVIDED, so omitting them here erases
	// them upstream on the next metadata save.
	Publisher string      `json:"publisher"`
	ISBN10    string      `json:"isbn10"`
	ISBN13    string      `json:"isbn13"`
	Kind      CatalogKind `json:"kind"`
	// Format is a temporary Task 1 compatibility projection for the existing
	// EPUB/PDF reader UI. It is derived from Kind, never copied from a provider,
	// and should be removed when Task 5 migrates the frontend to Kind.
	Format     string         `json:"format"`
	DurationMS int64          `json:"durationMs,omitempty"`
	CoverURL   string         `json:"coverUrl"`
	Progress   MemberProgress `json:"progress"`
}

func libraryFormatFromKind(kind CatalogKind) string {
	switch kind {
	case "epub":
		return "EPUB"
	case "pdf":
		return "PDF"
	case "audiobook":
		return "AUDIOBOOK"
	default:
		return ""
	}
}

type libraryProgressReader interface {
	GetMany(context.Context, string, []string) (map[string]MemberProgress, error)
}

// GrimmoryCatalog translates the complete, verified Grimmory catalog into
// stable Telos Library records. The Phase 1 repository remains the authority
// for identity and the continuity reader remains the authority for member
// progress.
type GrimmoryCatalog struct {
	repo       *CatalogRepository
	continuity libraryProgressReader
}

func currentGrimmoryCatalog() *GrimmoryCatalog {
	catalog := &GrimmoryCatalog{repo: catalogRepo}
	if continuityRepo != nil {
		catalog.continuity = continuityRepo
	}
	return catalog
}

func libraryKindFromGrimmory(format string) (CatalogKind, error) {
	switch strings.ToUpper(strings.TrimSpace(format)) {
	case "EPUB":
		return "epub", nil
	case "PDF":
		return "pdf", nil
	case "AUDIOBOOK":
		return "audiobook", nil
	default:
		return "", errUnsupportedLibraryKind
	}
}

func (c *GrimmoryCatalog) progressReader() (libraryProgressReader, error) {
	if c != nil && c.continuity != nil {
		return c.continuity, nil
	}
	if continuityRepo == nil {
		return nil, errors.New("continuity repository is not initialized")
	}
	return continuityRepo, nil
}

func normalizedLibraryItem(book LibraryBook) (LibraryItem, error) {
	kind, err := libraryKindFromGrimmory(book.Format)
	if err != nil {
		return LibraryItem{}, err
	}
	if !looksLikeUUID(book.ID) {
		return LibraryItem{}, fmt.Errorf("canonical Library item identifier is invalid")
	}
	authors := append([]string(nil), book.Authors...)
	if authors == nil {
		authors = []string{}
	}
	categories := append([]string(nil), book.Categories...)
	if categories == nil {
		categories = []string{}
	}
	return LibraryItem{
		ID: book.ID, Title: book.Title, Subtitle: book.Subtitle,
		Authors: authors, Narrator: book.Narrator, Categories: categories,
		Description: book.Description, Language: book.Language,
		SeriesName: book.SeriesName, SeriesNumber: book.SeriesNumber,
		PublishedDate: book.PublishedDate, AddedOn: book.AddedOn,
		Publisher: book.Publisher, ISBN10: book.ISBN10, ISBN13: book.ISBN13,
		Kind: kind, Format: libraryFormatFromKind(kind), DurationMS: book.DurationMS,
		CoverURL: "/api/v1/library/books/" + book.ID + "/cover",
		Progress: MemberProgress{ProgressInput: ProgressInput{Locator: json.RawMessage(`{}`)}},
	}, nil
}

func hydrateLibraryProgress(ctx context.Context, reader libraryProgressReader, userID string, items []LibraryItem) error {
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	progress, err := reader.GetMany(ctx, userID, ids)
	if err != nil {
		return err
	}
	for i := range items {
		if itemProgress, ok := progress[items[i].ID]; ok {
			items[i].Progress = itemProgress
		}
	}
	return nil
}

// ListItems reuses fetchGrimmoryBooks so one successful provider enumeration
// has exactly one Phase 1 reconciliation/backfill pass. Member progress is
// merged only after provider metadata has been canonicalized.
func (c *GrimmoryCatalog) ListItems(ctx context.Context, userID string) ([]LibraryItem, error) {
	books, err := fetchGrimmoryBooks(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]LibraryItem, 0, len(books))
	for _, book := range books {
		item, err := normalizedLibraryItem(book)
		if errors.Is(err, errUnsupportedLibraryKind) {
			continue
		}
		if err != nil {
			return nil, libraryCatalogError(err)
		}
		items = append(items, item)
	}
	reader, err := c.progressReader()
	if err != nil {
		return nil, libraryCatalogError(err)
	}
	if err := hydrateLibraryProgress(ctx, reader, userID, items); err != nil {
		return nil, libraryCatalogError(err)
	}
	return items, nil
}

// GetItem resolves a canonical or compatible legacy ID to its active Grimmory
// source, revalidates that source against the provider, and returns only the
// stable Telos identity to the member.
func (c *GrimmoryCatalog) GetItem(ctx context.Context, userID, rawID string) (LibraryItem, CatalogResolution, error) {
	var (
		resolution CatalogResolution
		err        error
	)
	if c != nil && c.repo != nil {
		resolution, err = c.repo.ResolveFor(ctx, rawID, SurfaceLibrary)
	} else {
		resolution, err = resolveCatalogIdentity(ctx, rawID, SurfaceLibrary)
	}
	if errors.Is(err, errCatalogNotFound) && !looksLikeUUID(rawID) {
		resolution, err = discoverLegacyGrimmoryBook(ctx, rawID)
	}
	if err != nil {
		if errors.Is(err, errLibraryProviderUnavailable) {
			return LibraryItem{}, CatalogResolution{}, err
		}
		if errors.Is(err, errCatalogNotFound) || errors.Is(err, errCatalogWrongSurface) || errors.Is(err, errCatalogInvalid) {
			return LibraryItem{}, CatalogResolution{}, errCatalogNotFound
		}
		return LibraryItem{}, CatalogResolution{}, libraryCatalogError(err)
	}
	if resolution.Provider != ProviderGrimmory || !resolution.Active || !resolution.Available {
		return LibraryItem{}, CatalogResolution{}, errCatalogNotFound
	}
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	book, err := fetchGrimmoryBook(requestCtx, resolution.UpstreamID)
	if err != nil {
		return LibraryItem{}, CatalogResolution{}, err
	}
	resolution, book, err = validateCurrentGrimmoryBook(ctx, resolution, book)
	if err != nil {
		return LibraryItem{}, CatalogResolution{}, err
	}
	item, err := normalizedLibraryItem(book)
	if err != nil {
		return LibraryItem{}, CatalogResolution{}, libraryCatalogError(err)
	}
	reader, err := c.progressReader()
	if err != nil {
		return LibraryItem{}, CatalogResolution{}, libraryCatalogError(err)
	}
	items := []LibraryItem{item}
	if err := hydrateLibraryProgress(ctx, reader, userID, items); err != nil {
		return LibraryItem{}, CatalogResolution{}, libraryCatalogError(err)
	}
	return items[0], resolution, nil
}

type LibraryAuthor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BookCount int    `json:"bookCount"`
}

type LibrarySeries struct {
	Name      string `json:"name"`
	BookCount int    `json:"bookCount"`
}

type grimmoryAuthorDTO struct {
	ID        json.RawMessage `json:"id"`
	Name      string          `json:"name"`
	BookCount int             `json:"bookCount"`
}

type grimmoryAuthorsPageDTO struct {
	Content []grimmoryAuthorDTO `json:"content"`
	HasNext bool                `json:"hasNext"`
}

type grimmorySeriesDTO struct {
	Name      string `json:"name"`
	BookCount int    `json:"bookCount"`
}

type grimmorySeriesPageDTO struct {
	Content []grimmorySeriesDTO `json:"content"`
	HasNext bool                `json:"hasNext"`
}

// grimmoryBooksPageDTO is Grimmory's paged envelope. Content is the raw
// upstream shape — decoding it into LibraryBook cannot work, because
// LibraryBook.ID became the canonical Telos UUID while upstream still sends a
// number.
type grimmoryBooksPageDTO struct {
	Content []grimmoryBook `json:"content"`
	HasNext bool           `json:"hasNext"`
}

// authorizedShelf turns a Grimmory discovery response into member-facing items.
//
// The discovery queries (recently-added, by author, by series) return provider
// records directly. They are used only for selection and ordering: the records
// themselves come from fetchGrimmoryBooks, which is the one path that applies
// the GRIMMORY_LIBRARY_IDS allowlist and reconciles stable catalog identity.
// Anything the shelf names but the authorized enumeration does not contain is
// out of scope for this node and is dropped, so a shelf can never widen what a
// member can reach.
func (c *GrimmoryCatalog) authorizedShelf(
	ctx context.Context, userID string, body []byte, limit int,
) ([]LibraryItem, error) {
	var raw []grimmoryBook
	if err := json.Unmarshal(body, &raw); err != nil {
		var page grimmoryBooksPageDTO
		if errPage := json.Unmarshal(body, &page); errPage != nil {
			return nil, libraryCatalogError(err)
		}
		raw = page.Content
	}

	authorized, err := fetchGrimmoryBooks(ctx)
	if err != nil {
		return nil, err
	}
	byUpstream := make(map[int64]LibraryBook, len(authorized))
	for _, book := range authorized {
		byUpstream[book.UpstreamID] = book
	}

	items := make([]LibraryItem, 0, len(raw))
	seen := make(map[int64]bool, len(raw))
	for _, entry := range raw {
		if limit > 0 && len(items) >= limit {
			break
		}
		if seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		book, ok := byUpstream[entry.ID]
		if !ok {
			continue
		}
		item, err := normalizedLibraryItem(book)
		if errors.Is(err, errUnsupportedLibraryKind) {
			continue
		}
		if err != nil {
			return nil, libraryCatalogError(err)
		}
		items = append(items, item)
	}

	reader, err := c.progressReader()
	if err != nil {
		return nil, libraryCatalogError(err)
	}
	if err := hydrateLibraryProgress(ctx, reader, userID, items); err != nil {
		return nil, libraryCatalogError(err)
	}
	return items, nil
}

func (c *GrimmoryCatalog) Continue(ctx context.Context, userID string) ([]LibraryItem, error) {
	if continuityRepo == nil {
		return []LibraryItem{}, nil
	}
	itemIDs, err := continuityRepo.Continue(ctx, userID, SurfaceLibrary, 20)
	if err != nil {
		return nil, libraryCatalogError(err)
	}
	items := make([]LibraryItem, 0, len(itemIDs))
	for _, id := range itemIDs {
		item, _, err := c.GetItem(ctx, userID, id)
		if err != nil {
			log.Printf("warn: library continue item %s resolution failed: %v", id, err)
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func (c *GrimmoryCatalog) Recent(ctx context.Context, userID string, limit int) ([]LibraryItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	resp, err := grimmoryGET(requestCtx, fmt.Sprintf("/api/v1/app/books/recently-added?limit=%d", limit))
	if err != nil {
		return nil, libraryProviderError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, libraryProviderError(fmt.Errorf("upstream status %d", resp.StatusCode))
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, libraryCatalogError(err)
	}
	return c.authorizedShelf(ctx, userID, bodyBytes, limit)
}

func (c *GrimmoryCatalog) Authors(ctx context.Context) ([]LibraryAuthor, error) {
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	resp, err := grimmoryGET(requestCtx, "/api/v1/app/authors")
	if err != nil {
		return nil, libraryProviderError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, libraryProviderError(fmt.Errorf("upstream status %d", resp.StatusCode))
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, libraryCatalogError(err)
	}
	var dtos []grimmoryAuthorDTO
	if err := json.Unmarshal(bodyBytes, &dtos); err != nil {
		var page grimmoryAuthorsPageDTO
		if errPage := json.Unmarshal(bodyBytes, &page); errPage == nil {
			dtos = page.Content
		} else {
			return nil, libraryCatalogError(err)
		}
	}
	authors := make([]LibraryAuthor, 0, len(dtos))
	for _, dto := range dtos {
		idStr := strings.Trim(string(dto.ID), `"`)
		if idStr == "" || idStr == "null" {
			continue
		}
		authors = append(authors, LibraryAuthor{
			ID:        idStr,
			Name:      dto.Name,
			BookCount: dto.BookCount,
		})
	}
	return authors, nil
}

func (c *GrimmoryCatalog) AuthorBooks(ctx context.Context, userID, authorID string) ([]LibraryItem, error) {
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	// First fetch author detail to get exact name
	respAuth, err := grimmoryGET(requestCtx, fmt.Sprintf("/api/v1/app/authors/%s", url.PathEscape(authorID)))
	if err != nil {
		return nil, libraryProviderError(err)
	}
	defer respAuth.Body.Close()
	if respAuth.StatusCode == http.StatusNotFound {
		return nil, errCatalogNotFound
	}
	if respAuth.StatusCode != http.StatusOK {
		return nil, libraryProviderError(fmt.Errorf("upstream status %d", respAuth.StatusCode))
	}
	var authorDTO grimmoryAuthorDTO
	if err := json.NewDecoder(respAuth.Body).Decode(&authorDTO); err != nil {
		return nil, libraryCatalogError(err)
	}

	// Fetch author's books
	resp, err := grimmoryGET(requestCtx, fmt.Sprintf("/api/v1/app/books?authors=%s", url.QueryEscape(authorDTO.Name)))
	if err != nil {
		return nil, libraryProviderError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, libraryProviderError(fmt.Errorf("upstream status %d", resp.StatusCode))
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, libraryCatalogError(err)
	}
	return c.authorizedShelf(ctx, userID, bodyBytes, 0)
}

func (c *GrimmoryCatalog) Series(ctx context.Context) ([]LibrarySeries, error) {
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	resp, err := grimmoryGET(requestCtx, "/api/v1/app/series")
	if err != nil {
		return nil, libraryProviderError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, libraryProviderError(fmt.Errorf("upstream status %d", resp.StatusCode))
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, libraryCatalogError(err)
	}
	var dtos []grimmorySeriesDTO
	if err := json.Unmarshal(bodyBytes, &dtos); err != nil {
		var page grimmorySeriesPageDTO
		if errPage := json.Unmarshal(bodyBytes, &page); errPage == nil {
			dtos = page.Content
		} else {
			return nil, libraryCatalogError(err)
		}
	}
	seriesList := make([]LibrarySeries, 0, len(dtos))
	for _, dto := range dtos {
		seriesList = append(seriesList, LibrarySeries{
			Name:      dto.Name,
			BookCount: dto.BookCount,
		})
	}
	return seriesList, nil
}

func (c *GrimmoryCatalog) SeriesBooks(ctx context.Context, userID, name string) ([]LibraryItem, error) {
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	resp, err := grimmoryGET(requestCtx, fmt.Sprintf("/api/v1/app/series/%s/books", url.PathEscape(name)))
	if err != nil {
		return nil, libraryProviderError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errCatalogNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, libraryProviderError(fmt.Errorf("upstream status %d", resp.StatusCode))
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, libraryCatalogError(err)
	}
	return c.authorizedShelf(ctx, userID, bodyBytes, 0)
}

