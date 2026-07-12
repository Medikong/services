package application_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Application services coordinate use cases, but HTTP concerns must remain in
// transport adapters. This keeps a use case callable from worker or tests
// without leaking request, response, cookie, or router details downward.
func TestApplicationDoesNotDependOnTransport(t *testing.T) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate application package")
	}
	assertNoImportsBelow(t, filepath.Dir(currentFile), "/internal/transport/")
}

func assertNoImportsBelow(t *testing.T, root, forbidden string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			if importPath := strings.Trim(imported.Path.Value, "\""); strings.Contains(importPath, forbidden) {
				t.Fatalf("application service %s imports forbidden transport package %s", filepath.Base(path), importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan application imports: %v", err)
	}
}
