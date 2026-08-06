//go:build !embedfrontend

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The default build target is headless. If this test ever sees anything other
// than a 404, the embed machinery has leaked back into the default build and
// `go build` will start failing wherever backend/out/ is absent.
func TestRegisterFrontendHeadlessMountsNothing(t *testing.T) {
	mux := http.NewServeMux()
	registerFrontend(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("headless build served / with status %d; want 404", rr.Code)
	}
}
