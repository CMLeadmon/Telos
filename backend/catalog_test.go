package main

import (
	"errors"
	"strings"
	"testing"
)

func TestCatalogValidation(t *testing.T) {
	if err := (CatalogObservation{Provider: "grimmory", UpstreamID: "7", LibraryID: "1", Surface: "library", Kind: "audiobook"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (CatalogObservation{Provider: "jellyfin", UpstreamID: "", LibraryID: "lib", Surface: "stream", Kind: "video"}).Validate(); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestCatalogValidationRejectsInvalidEnumsAndUpstreamStrings(t *testing.T) {
	valid := CatalogObservation{
		Provider:   ProviderGrimmory,
		UpstreamID: "book-7",
		LibraryID:  "library-1",
		Surface:    SurfaceLibrary,
		Kind:       CatalogKind("epub"),
	}
	tests := []struct {
		name   string
		mutate func(*CatalogObservation)
	}{
		{"provider", func(in *CatalogObservation) { in.Provider = "filesystem" }},
		{"surface", func(in *CatalogObservation) { in.Surface = "admin" }},
		{"kind", func(in *CatalogObservation) { in.Kind = "playlist" }},
		{"empty library", func(in *CatalogObservation) { in.LibraryID = "" }},
		{"upstream too long", func(in *CatalogObservation) { in.UpstreamID = strings.Repeat("x", 513) }},
		{"library too long", func(in *CatalogObservation) { in.LibraryID = strings.Repeat("x", 513) }},
		{"upstream control", func(in *CatalogObservation) { in.UpstreamID = "book\n7" }},
		{"library control", func(in *CatalogObservation) { in.LibraryID = "library\u007f1" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := valid
			tt.mutate(&in)
			if err := in.Validate(); !errors.Is(err, errCatalogInvalid) {
				t.Fatalf("Validate() error = %v, want errCatalogInvalid", err)
			}
		})
	}
}

func TestCatalogValidationAcceptsEverySchemaKindIncludingFolder(t *testing.T) {
	for _, kind := range []CatalogKind{"epub", "pdf", "audiobook", "video", "audio", "folder"} {
		in := CatalogObservation{
			Provider: ProviderJellyfin, UpstreamID: "item", LibraryID: "library",
			Surface: SurfaceStream, Kind: kind,
		}
		if err := in.Validate(); err != nil {
			t.Errorf("kind %q: %v", kind, err)
		}
	}
}

func TestCatalogCompleteScanInputValidation(t *testing.T) {
	repo := NewCatalogRepository(nil)
	if _, err := repo.CompleteScan(t.Context(), "unknown", "library", nil); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("invalid provider error = %v", err)
	}
	if _, err := repo.CompleteScan(t.Context(), ProviderGrimmory, "", nil); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("empty library error = %v", err)
	}
	if _, err := repo.CompleteScan(t.Context(), ProviderGrimmory, "library", make([]string, 100001)); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("oversized scan error = %v", err)
	}
}
