package authentication

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/Medikong/services/services/auth-service/internal/platform/httpauth"
)

func TestSignInCompletedResponseUsesChannelSpecificOpenAPIShape(t *testing.T) {
	credentials := httpauth.New(config.AuthConfig{SessionCookieName: "__Host-dm_refresh", AuthFlowCookieName: "__Host-dm_auth"}, config.DevelopmentConfig{})
	controller := NewSignIn(credentials, nil, nil, nil)
	common := Completed{Issued: appsession.Issued{TokenSet: appsession.TokenSet{UserID: "user", SessionID: "session", AccessToken: "access", RefreshToken: "refresh", AccessTokenExpiresAt: time.Now().Add(time.Minute), RefreshTokenExpiresAt: time.Now().Add(time.Hour)}, CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}, NextPath: "/drops/one", IntentID: "intent"}

	t.Run("web", func(t *testing.T) {
		issued := common
		issued.WebCookie = "web-cookie"
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signins/email", nil)
		controller.writeIssued(response, request, issued)
		data := responseData(t, response)
		assertKeys(t, data, "access", "credentialDelivery", "next", "session", "userId")
		var access map[string]any
		if err := json.Unmarshal(data["access"], &access); err != nil {
			t.Fatalf("decode access: %v", err)
		}
		assertMapKeys(t, access, "accessToken", "accessTokenExpiresAt")
		var next map[string]any
		if err := json.Unmarshal(data["next"], &next); err != nil {
			t.Fatalf("decode next: %v", err)
		}
		if next["path"] != "/drops/one" || next["intentId"] != "intent" {
			t.Fatalf("next=%#v", next)
		}
	})

	t.Run("mobile", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signins/email", nil)
		controller.writeIssued(response, request, common)
		data := responseData(t, response)
		assertKeys(t, data, "credentialDelivery", "next", "session", "tokens", "userId")
		var tokens map[string]any
		if err := json.Unmarshal(data["tokens"], &tokens); err != nil {
			t.Fatalf("decode tokens: %v", err)
		}
		assertMapKeys(t, tokens, "accessToken", "accessTokenExpiresAt", "refreshToken", "refreshTokenExpiresAt")
	})
}

func responseData(t *testing.T, response *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusOK)
	}
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope.Data
}

func assertKeys(t *testing.T, data map[string]json.RawMessage, want ...string) {
	t.Helper()
	actual := make([]string, 0, len(data))
	for key := range data {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	sort.Strings(want)
	if len(actual) != len(want) {
		t.Fatalf("keys=%v, want %v", actual, want)
	}
	for index := range want {
		if actual[index] != want[index] {
			t.Fatalf("keys=%v, want %v", actual, want)
		}
	}
}

func assertMapKeys(t *testing.T, data map[string]any, want ...string) {
	t.Helper()
	actual := make([]string, 0, len(data))
	for key := range data {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	sort.Strings(want)
	if len(actual) != len(want) {
		t.Fatalf("keys=%v, want %v", actual, want)
	}
	for index := range want {
		if actual[index] != want[index] {
			t.Fatalf("keys=%v, want %v", actual, want)
		}
	}
}
