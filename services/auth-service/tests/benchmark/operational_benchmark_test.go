//go:build benchmark

package benchmark_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	authhttp "github.com/Medikong/services/services/auth-service/internal/transport/http"
)

func BenchmarkHealthzRoute(b *testing.B) {
	mux := http.NewServeMux()
	authhttp.RegisterRoutes(mux, authhttp.Services{}, nil)
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
