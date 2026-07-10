package httpmiddleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTimeoutUsesStandardErrorResponse(t *testing.T) {
	handler := Timeout(time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", response.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "common.timeout" {
		t.Fatalf("error code = %q", body.Error.Code)
	}
}

func TestTimeoutDoesNotOverwriteStartedResponse(t *testing.T) {
	handler := Timeout(time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		<-r.Context().Done()
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if response.Code != http.StatusAccepted || response.Body.Len() != 0 {
		t.Fatalf("response = status:%d body:%s", response.Code, response.Body.String())
	}
}
