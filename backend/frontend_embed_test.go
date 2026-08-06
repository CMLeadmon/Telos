//go:build embedfrontend

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The counterpart to TestRegisterFrontendHeadlessMountsNothing. Built with
// -tags embedfrontend, the static export must actually be mounted at /, so a
// 404 here means the embed wiring regressed even though the tag was set.
func TestRegisterFrontendEmbedMountsStaticExport(t *testing.T) {
	mux := http.NewServeMux()
	registerFrontend(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code == http.StatusNotFound {
		t.Fatal("embedfrontend build did not mount the static export at /")
	}
}
