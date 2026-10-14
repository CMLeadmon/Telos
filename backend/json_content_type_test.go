package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// A handler that encodes JSON into its ResponseWriter must say so. Without a
// Content-Type, Go sniffs text/plain, and a client or probe that checks the type
// before parsing rejects a perfectly good answer (login, bootstrap and invite
// acceptance all shipped like that). The guard is structural: any function that
// calls json.NewEncoder(w).Encode must also mention Content-Type.
func TestHandlersThatEncodeJSONSetAContentType(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			encodes, setsType := false, false
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.CallExpr:
					selector, ok := value.Fun.(*ast.SelectorExpr)
					if ok && selector.Sel.Name == "Encode" {
						if inner, ok := selector.X.(*ast.CallExpr); ok {
							if innerSelector, ok := inner.Fun.(*ast.SelectorExpr); ok && innerSelector.Sel.Name == "NewEncoder" && len(inner.Args) == 1 {
								if writer, ok := inner.Args[0].(*ast.Ident); ok && (writer.Name == "w" || writer.Name == "rw") {
									encodes = true
								}
							}
						}
					}
				case *ast.BasicLit:
					if value.Kind == token.STRING && strings.Contains(value.Value, "Content-Type") {
						setsType = true
					}
				}
				return true
			})
			if encodes && !setsType {
				t.Errorf("%s: %s encodes JSON to the response without setting Content-Type", file, function.Name.Name)
			}
		}
	}
}

// The content type must be set before WriteHeader or it is silently dropped, so a
// WriteHeader immediately followed by writeJSON (which sets it) is the same defect
// as having no Content-Type at all.
func TestWriteJSONIsNeverPrecededByWriteHeader(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			block, ok := node.(*ast.BlockStmt)
			if !ok {
				return true
			}
			for i := 0; i+1 < len(block.List); i++ {
				if callName(block.List[i]) == "WriteHeader" && callName(block.List[i+1]) == "writeJSON" {
					t.Errorf("%s: %s sets the status before writeJSON sets the content type, so the type is dropped",
						file, fset.Position(block.List[i].Pos()))
				}
			}
			return true
		})
	}
}

func callName(statement ast.Stmt) string {
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return ""
	}
	call, ok := expression.X.(*ast.CallExpr)
	if !ok {
		return ""
	}
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return ""
}
