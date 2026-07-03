//go:build benchmark

package benchmark_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Medikong/services/services/user-service/internal/handler"
)

func BenchmarkHealthzRoute(b *testing.B) {
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	b.ReportAllocs()
	for range b.N {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			b.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
		}
	}
}
