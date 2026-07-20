package architecture_test

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

const platformPackagePath = "github.com/Medikong/services/packages/go-platform"

var forbiddenTechnologyImports = []string{
	"database/sql",
	"net/http",
	"github.com/go-chi/chi",
	"github.com/golang-jwt/jwt",
	"github.com/jackc/pgx",
	"github.com/lestrrat-go/jwx",
	"github.com/pressly/goose",
	"github.com/redis/go-redis",
	"github.com/segmentio/kafka-go",
	"github.com/confluentinc/confluent-kafka-go",
	"github.com/twmb/franz-go",
	"golang.org/x/crypto",
}

func TestLayerDependencies(t *testing.T) {
	root := internalRoot(t)
	for _, sourceLayer := range []string{"domain", "application", "interface", "infrastructure"} {
		walkGoFiles(t, filepath.Join(root, sourceLayer), func(path string, file *ast.File) {
			for _, imported := range file.Imports {
				importPath := strings.Trim(imported.Path.Value, `"`)
				if targetLayer := internalLayer(importPath); targetLayer != "" && !allowedInternalImport(sourceLayer, targetLayer) {
					t.Errorf("%s imports forbidden internal layer %q: %s", relativePath(root, path), targetLayer, importPath)
				}
				if (sourceLayer == "domain" || sourceLayer == "application") && hasImportPrefix(importPath, platformPackagePath) {
					t.Errorf("%s imports forbidden platform package: %s", relativePath(root, path), importPath)
				}
				if sourceLayer == "domain" || sourceLayer == "application" {
					for _, forbidden := range forbiddenTechnologyImports {
						if hasImportPrefix(importPath, forbidden) {
							t.Errorf("%s imports forbidden driver or transport package: %s", relativePath(root, path), importPath)
							break
						}
					}
				}
			}
		})
	}
}

func TestInternalLayerLayout(t *testing.T) {
	root := internalRoot(t)
	for _, legacy := range []string{"auth", "security", "transport"} {
		entries, err := filepath.Glob(filepath.Join(root, legacy, "*.go"))
		if err != nil {
			t.Fatalf("inspect legacy layer %s: %v", legacy, err)
		}
		if len(entries) > 0 {
			t.Errorf("legacy internal layer %q must be removed", legacy)
		}
	}
	entries, err := fs.ReadDir(os.DirFS(root), "platform")
	if err != nil {
		t.Fatalf("read platform layer: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "config" && entry.Name() != "observability" {
			t.Errorf("platform may contain only config and observability: platform/%s", entry.Name())
		}
	}
}

func TestHTTPRouteOwnership(t *testing.T) {
	root := internalRoot(t)
	httpRoot := filepath.Join(root, "interface", "http")
	walkGoFiles(t, root, func(path string, file *ast.File) {
		if filepath.Dir(path) == httpRoot && isCentralRouteFile(filepath.Base(path)) {
			t.Errorf("central HTTP route registry is forbidden: %s", relativePath(root, path))
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.TypeSpec:
				if isCentralRegistryName(node.Name.Name) {
					t.Errorf("central controller or route registry type is forbidden: %s in %s", node.Name.Name, relativePath(root, path))
				}
			case *ast.ValueSpec:
				for _, name := range node.Names {
					if isCentralRegistryName(name.Name) {
						t.Errorf("central controller or route registry value is forbidden: %s in %s", name.Name, relativePath(root, path))
					}
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(node.Value)
				if err == nil && isAPIPath(value) && !isFeatureRoutesFile(httpRoot, path) {
					t.Errorf("API path %q must be declared in internal/interface/http/<feature>/routes.go: %s", value, relativePath(root, path))
				}
			}
			return true
		})
	})
}

func walkGoFiles(t *testing.T, root string, visit func(string, *ast.File)) {
	t.Helper()
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("inspect %s: %v", root, err)
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		visit(path, file)
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
}

func internalRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	return filepath.Dir(filepath.Dir(currentFile))
}

func internalLayer(importPath string) string {
	const marker = "/internal/"
	index := strings.Index(importPath, marker)
	if index == -1 {
		return ""
	}
	layer, _, _ := strings.Cut(importPath[index+len(marker):], "/")
	return layer
}

func allowedInternalImport(sourceLayer, targetLayer string) bool {
	switch sourceLayer {
	case "domain":
		return targetLayer == "domain"
	case "application":
		return targetLayer == "application" || targetLayer == "domain"
	case "interface":
		return targetLayer == "interface" || targetLayer == "application"
	case "infrastructure":
		return targetLayer == "infrastructure" || targetLayer == "application" || targetLayer == "domain"
	default:
		return false
	}
}

func hasImportPrefix(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

func isFeatureRoutesFile(httpRoot, path string) bool {
	relative, err := filepath.Rel(httpRoot, path)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	return len(parts) == 2 && parts[0] != "." && parts[1] == "routes.go"
}

func isCentralRouteFile(name string) bool {
	return name == "routes.go" || name == "router.go" || name == "registry.go"
}

func isCentralRegistryName(name string) bool {
	return name == "Controllers" || name == "RouteRegistry" || name == "RoutesRegistry"
}

func isAPIPath(value string) bool {
	return strings.HasPrefix(value, "/api/") || strings.HasPrefix(value, "/.well-known/")
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(relative)
}
