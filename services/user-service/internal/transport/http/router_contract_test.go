package http

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Medikong/services/services/user-service/internal/development"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
)

func TestHTTPRoutesMatchOpenAPI(t *testing.T) {
	t.Parallel()
	documented := make(map[string]struct{})
	for _, name := range []string{"openapi.yaml", "development.openapi.yaml"} {
		readOpenAPIRoutes(t, "../../../api/"+name, documented)
	}
	implemented := make(map[string]struct{}, len(user.BusinessRoutes)+len(development.ProofRoutes))
	for _, route := range user.BusinessRoutes {
		implemented[route.Method+" "+route.Path] = struct{}{}
	}
	for _, route := range development.ProofRoutes {
		implemented[route.Method+" "+route.Path] = struct{}{}
	}
	if len(documented) != len(implemented) {
		t.Fatalf("documented routes = %v, implemented routes = %v", documented, implemented)
	}
	for route := range implemented {
		if _, ok := documented[route]; !ok {
			t.Fatalf("implemented route %q is missing from OpenAPI: documented=%v", route, documented)
		}
	}
}

func readOpenAPIRoutes(t *testing.T, path string, routes map[string]struct{}) {
	t.Helper()
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pathPattern := regexp.MustCompile(`^  (/.*):$`)
	methodPattern := regexp.MustCompile(`^    (get|post|patch|put|delete):$`)
	currentPath := ""
	scanner := bufio.NewScanner(strings.NewReader(string(document)))
	for scanner.Scan() {
		line := scanner.Text()
		if match := pathPattern.FindStringSubmatch(line); match != nil {
			currentPath = match[1]
			continue
		}
		if match := methodPattern.FindStringSubmatch(line); match != nil && currentPath != "" {
			routes[strings.ToUpper(match[1])+" "+currentPath] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
