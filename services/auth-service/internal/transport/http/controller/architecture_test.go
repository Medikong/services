package controller

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Controllers translate HTTP only. Domain choices and PostgreSQL access must
// remain behind an application-service boundary so an API change cannot alter
// an aggregate directly.
func TestControllersDoNotDependOnDomainOrPostgres(t *testing.T) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate controller package")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*.go"))
	if err != nil {
		t.Fatalf("list controller files: %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imported := range parsed.Imports {
			path := strings.Trim(imported.Path.Value, "\"")
			if strings.Contains(path, "/internal/domain/") || strings.Contains(path, "jackc/pgx") {
				t.Fatalf("controller %s imports forbidden lower-layer package %s", filepath.Base(name), path)
			}
		}
	}
}
