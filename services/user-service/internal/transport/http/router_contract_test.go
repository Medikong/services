package http

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestBusinessRoutesMatchOpenAPI(t *testing.T) {
	t.Parallel()
	document, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pathPattern := regexp.MustCompile(`^  (/api/v1.*):$`)
	methodPattern := regexp.MustCompile(`^    (get|post|patch|put|delete):$`)
	documented := make(map[string]struct{})
	currentPath := ""
	scanner := bufio.NewScanner(strings.NewReader(string(document)))
	for scanner.Scan() {
		line := scanner.Text()
		if match := pathPattern.FindStringSubmatch(line); match != nil {
			currentPath = match[1]
			continue
		}
		if match := methodPattern.FindStringSubmatch(line); match != nil && currentPath != "" {
			documented[strings.ToUpper(match[1])+" "+currentPath] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	implemented := make(map[string]struct{}, len(BusinessRoutes))
	for _, route := range BusinessRoutes {
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
