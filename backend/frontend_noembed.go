//go:build !embedfrontend

package main

import "net/http"

func registerFrontend(mux *http.ServeMux) {
	// Headless mode: zero frontend asset embedding or static asset serving.
}
