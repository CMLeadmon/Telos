package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A book Grimmory does not have is a 404; a Grimmory that cannot answer is a 503
// the client can retry. The two were both "Not found.", so during a Grimmory outage
// every book looked deleted.
func TestGrimmoryOutageIsUnavailableNotNotFound(t *testing.T) {
	status := http.StatusOK
	cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	})
	defer cleanup()
	resolution := CatalogResolution{UpstreamID: "7", Kind: "ebook"}

	status = http.StatusNotFound
	if _, _, err := currentGrimmoryBook(context.Background(), resolution); !errors.Is(err, errBookNotAuthorized) {
		t.Fatalf("a book Grimmory does not know: err = %v, want errBookNotAuthorized", err)
	}
	for _, code := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		status = code
		if _, _, err := currentGrimmoryBook(context.Background(), resolution); !errors.Is(err, errLibraryProviderUnavailable) {
			t.Fatalf("Grimmory answering %d: err = %v, want errLibraryProviderUnavailable", code, err)
		}
	}

	for _, tc := range []struct {
		err  error
		want int
	}{
		{libraryProviderError(io.EOF), http.StatusServiceUnavailable},
		{errBookNotAuthorized, http.StatusNotFound},
		{errGrimmoryBookNotFound, http.StatusNotFound},
		{errBookAmbiguous, http.StatusNotFound},
	} {
		recorder := httptest.NewRecorder()
		writeBookResolutionError(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/library/books/x/content", nil), tc.err)
		if recorder.Code != tc.want {
			t.Errorf("%v answered %d, want %d", tc.err, recorder.Code, tc.want)
		}
	}
}
