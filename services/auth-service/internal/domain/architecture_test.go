package domain_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Domain packages may contain HTTP controllers, but only controller files may
// depend on HTTP credentials and utilities. Domain rules and persistence stay
// free of transport dependencies.
func TestDomainPackageBoundaries(t *testing.T) {
	root := domainRoot(t)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "store" || entry.Name() == "memory" {
				t.Fatalf("generic domain persistence directory is forbidden: %s", path)
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(filepath.Base(path), "memory") {
			t.Fatalf("in-memory domain repository is forbidden: %s", path)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		controllerFile := strings.HasSuffix(filepath.Base(path), "controller.go")
		for _, imported := range parsed.Imports {
			importPath := strings.Trim(imported.Path.Value, "\"")
			allowedHTTPImport := strings.HasSuffix(importPath, "/internal/platform/httpauth") ||
				strings.HasSuffix(importPath, "/internal/transport/httputil")
			httpBoundaryImport := strings.Contains(importPath, "/internal/transport/") ||
				strings.HasSuffix(importPath, "/internal/platform/httpauth")
			if httpBoundaryImport && (!controllerFile || !allowedHTTPImport) {
				t.Fatalf("domain package %s imports forbidden transport package %s", filepath.Base(path), importPath)
			}
			if controllerFile && strings.Contains(importPath, "jackc/pgx") {
				t.Fatalf("domain controller %s imports postgres package %s", filepath.Base(path), importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan domain imports: %v", err)
	}
}

func TestHTTPRouteOwnership(t *testing.T) {
	internalRoot := filepath.Dir(domainRoot(t))
	if _, err := os.Stat(filepath.Join(internalRoot, "transport", "router.go")); !os.IsNotExist(err) {
		t.Fatal("central transport/router.go must not exist")
	}

	httpMethods := map[string]bool{
		"Get": true, "Post": true, "Put": true, "Patch": true, "Delete": true,
		"Method": true, "Handle": true, "HandleFunc": true,
	}
	err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			importPath := strings.Trim(imported.Path.Value, "\"")
			if strings.HasPrefix(path, filepath.Join(internalRoot, "transport")+string(filepath.Separator)) &&
				strings.Contains(importPath, "/internal/domain/") {
				t.Fatalf("transport package %s imports domain package %s", path, importPath)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.TypeSpec:
				if node.Name.Name == "Controllers" {
					t.Fatalf("central Controllers type is forbidden: %s", path)
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING || !strings.HasPrefix(path, filepath.Join(internalRoot, "app")+string(filepath.Separator)) {
					return true
				}
				value, unquoteErr := strconv.Unquote(node.Value)
				if unquoteErr == nil && isAPIPath(value) {
					t.Fatalf("app package declares API path %q in %s", value, path)
				}
			case *ast.CallExpr:
				selector, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || !httpMethods[selector.Sel.Name] || len(node.Args) == 0 {
					return true
				}
				literal, ok := node.Args[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr != nil || !isAPIPath(value) {
					return true
				}
				domainPrefix := filepath.Join(internalRoot, "domain") + string(filepath.Separator)
				if !strings.HasPrefix(path, domainPrefix) || filepath.Base(path) != "routes.go" {
					t.Fatalf("API route %q must be declared in a domain routes.go: %s", value, path)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan HTTP route ownership: %v", err)
	}
}

func domainRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate domain package")
	}
	return filepath.Dir(currentFile)
}

func isAPIPath(value string) bool {
	return strings.HasPrefix(value, "/api/") || strings.HasPrefix(value, "/.well-known/")
}
