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

// Aggregate repositories sit at the bottom of the service. They may use pgx
// directly, but must not acquire application or HTTP dependencies.
func TestDomainDoesNotDependOnUpperLayersOrGenericStores(t *testing.T) {
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
		for _, imported := range parsed.Imports {
			importPath := strings.Trim(imported.Path.Value, "\"")
			if strings.Contains(importPath, "/internal/application/") || strings.Contains(importPath, "/internal/transport/") {
				t.Fatalf("domain package %s imports forbidden upper-layer package %s", filepath.Base(path), importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan domain imports: %v", err)
	}
}
