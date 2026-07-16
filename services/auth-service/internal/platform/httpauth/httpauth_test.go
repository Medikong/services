package httpauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

func TestExtractionRejectsAmbiguousCredentials(t *testing.T) {
	credentials := testCredentials()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-dm_auth", Value: "web-flow"})
	request.Header.Set(authFlowTokenHeader, "mobile-flow")
	if _, err := credentials.PreAuth(request); err == nil || err.Kind != Multiple {
		t.Fatalf("pre-auth error = %#v", err)
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-dm_refresh", Value: "web-refresh"})
	request.Header.Set("Authorization", "Bearer mobile-jwt")
	if _, err := credentials.Session(request); err == nil || err.Kind != Multiple {
		t.Fatalf("session error = %#v", err)
	}
}

func TestExtractionKeepsChannelAndRejectsMalformedBearer(t *testing.T) {
	credentials := testCredentials()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set(authFlowTokenHeader, "mobile-flow")
	preAuth, err := credentials.PreAuth(request)
	if err != nil || preAuth.Channel != Mobile || preAuth.Token != "mobile-flow" {
		t.Fatalf("pre-auth = %#v, error = %#v", preAuth, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Basic token")
	if _, err = credentials.Session(request); err == nil || err.Kind != Malformed {
		t.Fatalf("malformed bearer error = %#v", err)
	}
}

func TestSessionCookieDeliveryAndAuthFlowClear(t *testing.T) {
	credentials := testCredentials()
	response := httptest.NewRecorder()
	credentials.SetSessionCookie(response, "session-value", 3600)
	credentials.ClearAuthFlowCookie(response)

	cookies := response.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookie count = %d", len(cookies))
	}
	if cookies[0].Name != "__Host-dm_refresh" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Path != "/api/v1/auth/sessions" || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != 3600 {
		t.Fatal("session cookie attributes are invalid")
	}
	if cookies[1].Name != "__Host-dm_auth" || cookies[1].MaxAge >= 0 {
		t.Fatal("auth-flow cookie was not cleared")
	}
}

func TestDevelopmentTokenIsGated(t *testing.T) {
	credentials := testCredentials()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(developmentAccessTokenHeader, "development-token")
	if err := credentials.DevelopmentToken(request); err != nil {
		t.Fatalf("DevelopmentToken() error = %#v", err)
	}
	request.Header.Set(developmentAccessTokenHeader, "wrong-token")
	if err := credentials.DevelopmentToken(request); err == nil || err.Kind != Rejected {
		t.Fatalf("rejected development token = %#v", err)
	}
}

func testCredentials() *Credentials {
	return New(config.AuthConfig{
		SessionCookieName:  "__Host-dm_refresh",
		AuthFlowCookieName: "__Host-dm_auth",
		CookieSecure:       true,
	}, config.DevelopmentConfig{
		Enabled:      true,
		RouteEnabled: true,
		AccessToken:  "development-token",
	})
}
