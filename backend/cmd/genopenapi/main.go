package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Route is one API registration discovered in the gateway's route table.
type Route struct {
	Method string
	Path   string
}

// parseRoutes walks sourceFile for mux.Handle and mux.HandleFunc calls and
// returns every API route registered. Patterns use the Go 1.22+ form
// "METHOD /path". The single unprefixed registration is the frontend
// catch-all and is deliberately excluded.
func parseRoutes(sourceFile string) ([]Route, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, sourceFile, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", sourceFile, err)
	}

	var routes []Route
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc" {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "mux" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		space := strings.IndexByte(pattern, ' ')
		if space <= 0 {
			return true // frontend catch-all
		}
		routes = append(routes, Route{
			Method: pattern[:space],
			Path:   strings.TrimSpace(pattern[space+1:]),
		})
		return true
	})

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes, nil
}

// operationID derives a stable, unique identifier from a route.
func operationID(r Route) string {
	trimmed := strings.TrimPrefix(r.Path, "/api/v1/")
	slug := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(trimmed)
	return strings.ToLower(r.Method) + "_" + slug
}

// buildDocument renders the discovered routes as an OpenAPI 3.1 document.
// Go 1.22 wildcards ({id}) are already OpenAPI-shaped.
func buildDocument(routes []Route) map[string]any {
	paths := map[string]any{}
	for _, r := range routes {
		item, _ := paths[r.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[r.Path] = item
		}
		item[strings.ToLower(r.Method)] = map[string]any{
			"operationId": operationID(r),
			"responses": map[string]any{
				"200": map[string]any{"description": "Success"},
			},
		}
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Telos Gateway API",
			"version":     "1.0.0",
			"description": "Generated from the mux route table in main.go. Do not edit by hand.",
		},
		"paths": paths,
	}
}

func main() {
	check := flag.Bool("check", false, "verify the committed document is current; write nothing")
	source := flag.String("source", "main.go", "route table source file")
	out := flag.String("out", filepath.Join("api", "openapi.json"), "output path")
	flag.Parse()

	routes, err := parseRoutes(*source)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Fail loudly rather than emitting an empty contract if the walk breaks.
	if len(routes) == 0 {
		fmt.Fprintf(os.Stderr, "no routes found in %s - refusing to write an empty contract\n", *source)
		os.Exit(1)
	}

	encoded, err := json.MarshalIndent(buildDocument(routes), "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded = append(encoded, '\n')

	if *check {
		existing, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", *out, err)
			os.Exit(1)
		}
		if string(existing) != string(encoded) {
			fmt.Fprintf(os.Stderr, "OpenAPI drift: %s is stale. Run: cd backend && go run ./cmd/genopenapi\n", *out)
			os.Exit(1)
		}
		fmt.Printf("OpenAPI contract is current (%d routes).\n", len(routes))
		return
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, encoded, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s (%d routes).\n", *out, len(routes))
}
