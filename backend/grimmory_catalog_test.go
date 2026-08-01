package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type recordingLibraryProgressReader struct {
	calls   int
	userID  string
	itemIDs []string
}

func (r *recordingLibraryProgressReader) GetMany(_ context.Context, userID string, itemIDs []string) (map[string]MemberProgress, error) {
	r.calls++
	r.userID = userID
	r.itemIDs = append([]string(nil), itemIDs...)
	if len(itemIDs) == 0 {
		return map[string]MemberProgress{}, nil
	}
	return map[string]MemberProgress{
		itemIDs[0]: {
			ProgressInput: ProgressInput{
				Locator:    json.RawMessage(`{"trackIndex":2}`),
				PositionMS: 125_000,
				DurationMS: 3_600_000,
				Percent:    0.034,
			},
		},
	}, nil
}

func TestLibraryKindFromGrimmory(t *testing.T) {
	cases := map[string]CatalogKind{
		"EPUB":      "epub",
		" pdf ":     "pdf",
		"audiobook": "audiobook",
	}
	for format, want := range cases {
		got, err := libraryKindFromGrimmory(format)
		if err != nil || got != want {
			t.Errorf("%q: got=%q err=%v, want=%q", format, got, err, want)
		}
	}
	for _, format := range []string{"CBX", "MOBI", "", "PHYSICAL"} {
		if got, err := libraryKindFromGrimmory(format); got != "" || !errors.Is(err, errUnsupportedLibraryKind) {
			t.Errorf("unsupported %q: got=%q err=%v", format, got, err)
		}
	}
}

func TestGrimmoryCatalogListItemsNormalizesAudiobookAndBatchesProgress(t *testing.T) {
	var logs bytes.Buffer
	oldLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldLogOutput) })

	installCatalogIdentityTest(t)
	oldAuthorizer := grimmoryAuthorizer
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"1"})
	t.Cleanup(func() { grimmoryAuthorizer = oldAuthorizer })

	installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[
			{
				"id":73,
				"libraryId":1,
				"libraryName":"Spoken Books",
				"addedOn":"2026-07-30T15:04:05Z",
				"metadata":{
					"title":"Braiding Sweetgrass",
					"subtitle":"Indigenous Wisdom",
					"authors":["Robin Wall Kimmerer"],
					"narrator":"Robin Wall Kimmerer",
					"categories":["Nature","History"],
					"description":"A meeting of science and story.",
					"language":"en",
					"seriesName":"Earth Stories",
					"seriesNumber":2.5,
					"publishedDate":"2020-01-01",
					"audiobookMetadata":{"durationSeconds":3600}
				},
				"primaryFile":{"bookType":"AUDIOBOOK","fileSizeKb":42100}
			},
			{
				"id":74,
				"libraryId":1,
				"addedOn":"2026-07-29T10:00:00Z",
				"metadata":{"title":"Untitled Lists"},
				"primaryFile":{"bookType":"EPUB"}
			},
			{
				"id":75,
				"libraryId":1,
				"metadata":{"title":"Deferred Comic"},
				"primaryFile":{"bookType":"CBX"}
			}
		]`)
	})

	progress := &recordingLibraryProgressReader{}
	catalog := &GrimmoryCatalog{continuity: progress}
	items, err := catalog.ListItems(t.Context(), "00000000-0000-4000-8000-000000000099")
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%+v, want supported audiobook and EPUB only", items)
	}

	audio := items[0]
	if !looksLikeUUID(audio.ID) || audio.ID == "73" {
		t.Fatalf("public id=%q, want canonical UUID", audio.ID)
	}
	if audio.Kind != "audiobook" || audio.Title != "Braiding Sweetgrass" || audio.Subtitle != "Indigenous Wisdom" {
		t.Fatalf("audiobook identity fields=%+v", audio)
	}
	if got := strings.Join(audio.Authors, ","); got != "Robin Wall Kimmerer" || audio.Narrator != "Robin Wall Kimmerer" {
		t.Fatalf("audiobook people authors=%q narrator=%q", got, audio.Narrator)
	}
	if got := strings.Join(audio.Categories, ","); got != "Nature,History" {
		t.Fatalf("categories=%q", got)
	}
	if audio.Description != "A meeting of science and story." || audio.Language != "en" ||
		audio.SeriesName != "Earth Stories" || audio.SeriesNumber == nil || *audio.SeriesNumber != 2.5 ||
		audio.PublishedDate != "2020-01-01" || audio.AddedOn != "2026-07-30T15:04:05Z" {
		t.Fatalf("audiobook descriptive fields=%+v", audio)
	}
	if audio.DurationMS != 3_600_000 {
		t.Fatalf("durationMs=%d, want 3600000", audio.DurationMS)
	}
	wantCover := "/api/v1/library/books/" + audio.ID + "/cover"
	if audio.CoverURL != wantCover {
		t.Fatalf("coverUrl=%q, want=%q", audio.CoverURL, wantCover)
	}
	if audio.Progress.PositionMS != 125_000 || audio.Progress.DurationMS != 3_600_000 {
		t.Fatalf("progress=%+v", audio.Progress)
	}
	if items[1].Authors == nil || items[1].Categories == nil {
		t.Fatalf("nil provider lists were not normalized: %+v", items[1])
	}

	if progress.calls != 1 || progress.userID != "00000000-0000-4000-8000-000000000099" {
		t.Fatalf("GetMany calls=%d user=%q", progress.calls, progress.userID)
	}
	if len(progress.itemIDs) != 2 || progress.itemIDs[0] != audio.ID || progress.itemIDs[1] != items[1].ID {
		t.Fatalf("GetMany item IDs=%v, want canonical page IDs", progress.itemIDs)
	}

	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"id":"73"`) || strings.Contains(string(raw), "upstream") || strings.Contains(string(raw), "Spoken Books") {
		t.Fatalf("provider identity leaked in public payload: %s", raw)
	}
	if got := strings.Count(logs.String(), "unsupported=1"); got != 1 {
		t.Fatalf("bounded unsupported-format observations=%d log=%q, want one aggregate count", got, logs.String())
	}
}

func catalogEnumerationSamples(t *testing.T, result CatalogResult) uint64 {
	t.Helper()
	observer := CatalogReconcileDuration.WithLabelValues(
		string(ProviderGrimmory), string(CatalogOperationEnumerate), string(result),
	)
	metric, ok := observer.(prometheus.Metric)
	if !ok {
		t.Fatal("catalog enumeration observer does not expose a metric")
	}
	var out dto.Metric
	if err := metric.Write(&out); err != nil {
		t.Fatal(err)
	}
	return out.GetHistogram().GetSampleCount()
}

func TestGrimmoryCatalogRecordsEnumerationOutcome(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		installCatalogIdentityTest(t)
		before := catalogEnumerationSamples(t, CatalogResultSuccess)
		installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `[{"id":91,"libraryId":1,"metadata":{"title":"Metric Book"},"primaryFile":{"bookType":"PDF"}}]`)
		})
		if _, err := fetchGrimmoryBooks(t.Context()); err != nil {
			t.Fatalf("successful enumeration: %v", err)
		}
		if got := catalogEnumerationSamples(t, CatalogResultSuccess); got != before+1 {
			t.Fatalf("success enumeration samples=%d, want=%d", got, before+1)
		}
	})

	t.Run("upstream failure", func(t *testing.T) {
		before := catalogEnumerationSamples(t, CatalogResultFailed)
		installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusBadGateway)
		})
		if _, err := fetchGrimmoryBooks(t.Context()); err == nil {
			t.Fatal("failed enumeration unexpectedly succeeded")
		}
		if got := catalogEnumerationSamples(t, CatalogResultFailed); got != before+1 {
			t.Fatalf("failed enumeration samples=%d, want=%d", got, before+1)
		}
	})
}

func TestGrimmoryCatalogListItemsHydrates501ItemsWithOneBatch(t *testing.T) {
	installCatalogIdentityTest(t)
	installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		books := make([]map[string]any, 501)
		for i := range books {
			books[i] = map[string]any{
				"id":        i + 1,
				"libraryId": 1,
				"metadata": map[string]any{
					"title": fmt.Sprintf("Book %d", i+1),
				},
				"primaryFile": map[string]any{"bookType": "EPUB"},
			}
		}
		_ = json.NewEncoder(w).Encode(books)
	})

	progress := &recordingLibraryProgressReader{}
	items, err := (&GrimmoryCatalog{continuity: progress}).ListItems(
		t.Context(), "00000000-0000-4000-8000-000000000099",
	)
	if err != nil {
		t.Fatalf("ListItems with 501 records: %v", err)
	}
	if len(items) != 501 || progress.calls != 1 || len(progress.itemIDs) != 501 {
		t.Fatalf("items=%d GetMany calls=%d ids=%d, want 501/1/501", len(items), progress.calls, len(progress.itemIDs))
	}
}

func TestGrimmoryCatalogGetItemUsesCanonicalIdentityAndMemberProgress(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000081"
	cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/81" {
			_, _ = fmt.Fprint(w, `{
				"id":81,"libraryId":1,"addedOn":"2026-07-31T10:00:00Z",
				"metadata":{"title":"A Single Book","authors":["A. Writer"]},
				"primaryFile":{"bookType":"PDF"}
			}`)
			return
		}
		http.NotFound(w, r)
	})
	defer cleanup()

	oldObserve, oldResolve := observeCatalogIdentity, resolveCatalogIdentity
	observeCatalogIdentity = func(_ context.Context, in CatalogObservation) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceLibrary, Kind: in.Kind, Provider: ProviderGrimmory,
			UpstreamID: in.UpstreamID, LibraryID: in.LibraryID, Active: true, Available: true, Revision: 3,
		}, nil
	}
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		if rawID != canonicalID || surface != SurfaceLibrary {
			return CatalogResolution{}, errCatalogNotFound
		}
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceLibrary, Kind: "pdf", Provider: ProviderGrimmory,
			UpstreamID: "81", LibraryID: "1", Active: true, Available: true, Revision: 3,
		}, nil
	}
	t.Cleanup(func() { observeCatalogIdentity, resolveCatalogIdentity = oldObserve, oldResolve })

	progress := &recordingLibraryProgressReader{}
	catalog := &GrimmoryCatalog{continuity: progress}
	item, resolution, err := catalog.GetItem(t.Context(), "00000000-0000-4000-8000-000000000099", canonicalID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if resolution.ID != canonicalID || item.ID != canonicalID || item.Kind != "pdf" || item.Title != "A Single Book" {
		t.Fatalf("item=%+v resolution=%+v", item, resolution)
	}
	if progress.calls != 1 || len(progress.itemIDs) != 1 || progress.itemIDs[0] != canonicalID {
		t.Fatalf("GetMany calls=%d ids=%v", progress.calls, progress.itemIDs)
	}
}
