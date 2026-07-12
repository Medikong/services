package catalog

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthoritativeElementCountsAndIDs(t *testing.T) {
	tests := []struct {
		prefix string
		items  []Element
		count  int
	}{
		{prefix: "AGG.A.19-", items: Aggregates, count: 8},
		{prefix: "CMD.A.19-", items: Commands, count: 34},
		{prefix: "EVT.A.19-", items: Events, count: 41},
		{prefix: "POLICY.A.19-", items: Policies, count: 22},
	}
	for _, test := range tests {
		t.Run(test.prefix, func(t *testing.T) {
			require.Len(t, test.items, test.count)
			seen := make(map[string]struct{}, test.count)
			for index, item := range test.items {
				require.Equal(t, fmt.Sprintf("%s%02d", test.prefix, index+1), item.ID)
				require.NotEmpty(t, item.Name)
				require.NotEmpty(t, item.Handler)
				_, duplicated := seen[item.ID]
				require.False(t, duplicated, "duplicate catalog ID %s", item.ID)
				seen[item.ID] = struct{}{}
			}
		})
	}
}

func TestEventCatalogMatchesProductionLiterals(t *testing.T) {
	t.Parallel()

	productionFiles := productionStringLiterals(t)
	for _, event := range Events {
		event := event
		t.Run(event.ID, func(t *testing.T) {
			t.Parallel()

			for _, literals := range productionFiles {
				_, hasDocumentID := literals[event.ID]
				_, hasEventType := literals[event.Handler]
				if hasDocumentID && hasEventType {
					return
				}
			}
			t.Fatalf("catalog event %s (%s) has no matching production literals", event.ID, event.Handler)
		})
	}
}

func productionStringLiterals(t *testing.T) map[string]map[string]struct{} {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	catalogDir := filepath.Dir(currentFile)
	internalDir := filepath.Clean(filepath.Join(catalogDir, "..", ".."))
	files := make(map[string]map[string]struct{})
	err := filepath.WalkDir(internalDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == catalogDir {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		literals := make(map[string]struct{})
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil {
				literals[value] = struct{}{}
			}
			return true
		})
		files[path] = literals
		return nil
	})
	require.NoError(t, err)
	return files
}
