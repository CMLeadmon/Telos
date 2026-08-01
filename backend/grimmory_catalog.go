package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Kind          CatalogKind `json:"kind"`
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
