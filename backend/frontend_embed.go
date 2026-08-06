//go:build embedfrontend

package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
)

//go:embed all:out
var frontendFS embed.FS

func registerFrontend(mux *http.ServeMux) {
	subFS, err := fs.Sub(frontendFS, "out")
	if err != nil {
		log.Fatalf("Failed to locate frontend out/ directory: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))
	mux.Handle("/", themedFrontend(fileServer))
}
