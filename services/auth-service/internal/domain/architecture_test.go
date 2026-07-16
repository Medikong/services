package domain_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Domain packages may contain HTTP controllers, but only controller files may
// depend on HTTP credentials and utilities. Domain rules and persistence stay
// free of transport dependencies.
func TestDomainPackageBoundaries(t *testing.T) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate domain package")
	}
	root := filepath.Dir(currentFile)
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
			allowedHTTPImport := strings.HasSuffix(importPath, "/internal/transport/credential") ||
				strings.HasSuffix(importPath, "/internal/transport/httputil")
			if strings.Contains(importPath, "/internal/transport/") && (!controllerFile || !allowedHTTPImport) {
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
