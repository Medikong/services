package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type headerOnlyWriter struct {
	header http.Header
}

func (w *headerOnlyWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*headerOnlyWriter) Write(body []byte) (int, error) { return len(body), nil }
func (*headerOnlyWriter) WriteHeader(int)                {}

func TestResponseRecorderPreservesOptionalInterfaces(t *testing.T) {
	unsupported := newResponseRecorder(&headerOnlyWriter{}).Writer()
	if _, ok := unsupported.(http.Flusher); ok {
		t.Fatal("recorder advertises http.Flusher when the underlying writer does not support it")
	}

	supported := newResponseRecorder(httptest.NewRecorder()).Writer()
	if _, ok := supported.(http.Flusher); !ok {
		t.Fatal("recorder does not preserve http.Flusher")
	}
}

func TestRecoveryDoesNotAppendErrorAfterFlush(t *testing.T) {
	response := httptest.NewRecorder()
	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		panic("after flush")
	}))

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("panic was not re-raised")
		}
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
		if response.Body.Len() != 0 {
			t.Fatalf("body appended after flush: %s", response.Body.String())
		}
	}()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/flush", nil))
}
