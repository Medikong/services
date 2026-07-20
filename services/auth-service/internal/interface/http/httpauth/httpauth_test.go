package httpauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractionRejectsAmbiguousCredentials(t *testing.T) {
	credentials := testCredentials(t)
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-dm_auth", Value: "web-flow"})
	request.Header.Set(authFlowTokenHeader, "mobile-flow")
	if _, err := credentials.PreAuth(request); err == nil || err.Kind != Multiple {
		t.Fatalf("pre-auth error = %#v", err)
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "__Secure-dm_refresh", Value: "web-refresh"})
	request.Header.Set("Authorization", "Bearer mobile-jwt")
	if _, err := credentials.Session(request); err == nil || err.Kind != Multiple {
		t.Fatalf("session error = %#v", err)
	}
}

func TestExtractionKeepsChannelAndRejectsMalformedBearer(t *testing.T) {
	credentials := testCredentials(t)
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
	credentials := testCredentials(t)
	response := httptest.NewRecorder()
	credentials.SetSessionCookie(response, "session-value", 3600)
	credentials.ClearAuthFlowCookie(response)

	cookies := response.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookie count = %d", len(cookies))
	}
	if cookies[0].Name != "__Secure-dm_refresh" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Path != "/api/v1/auth/sessions" || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != 3600 {
		t.Fatal("session cookie attributes are invalid")
	}
	if cookies[1].Name != "__Host-dm_auth" || cookies[1].MaxAge >= 0 {
		t.Fatal("auth-flow cookie was not cleared")
	}
}

func TestDevelopmentTokenIsGated(t *testing.T) {
	credentials := testCredentials(t)
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

func TestNewRejectsEmptySessionCookiePath(t *testing.T) {
	credentials, err := New(Config{}, " ")
	if err == nil || credentials != nil {
		t.Fatalf("New() = (%#v, %#v), want nil credentials and error", credentials, err)
	}
}

func TestNewValidatesBrowserCookiePrefixes(t *testing.T) {
	tests := []struct {
		name              string
		config            Config
		sessionCookiePath string
		wantError         bool
	}{
		{
			name:              "secure prefix requires Secure",
			config:            Config{SessionCookieName: "__Secure-dm_refresh", AuthFlowCookieName: "dm_auth"},
			sessionCookiePath: "/api/v1/auth/sessions",
			wantError:         true,
		},
		{
			name:              "host prefix requires root path",
			config:            Config{SessionCookieName: "__Host-test_refresh", AuthFlowCookieName: "__Host-dm_auth", CookieSecure: true},
			sessionCookiePath: "/api/v1/auth/sessions",
			wantError:         true,
		},
		{
			name:              "auth flow host prefix requires Secure",
			config:            Config{SessionCookieName: "dm_refresh", AuthFlowCookieName: "__Host-dm_auth"},
			sessionCookiePath: "/api/v1/auth/sessions",
			wantError:         true,
		},
		{
			name:              "scoped secure refresh and host auth flow",
			config:            Config{SessionCookieName: "__Secure-dm_refresh", AuthFlowCookieName: "__Host-dm_auth", CookieSecure: true},
			sessionCookiePath: "/api/v1/auth/sessions",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credentials, err := New(test.config, test.sessionCookiePath)
			if test.wantError {
				if err == nil || credentials != nil {
					t.Fatalf("New() = (%#v, %#v), want nil credentials and error", credentials, err)
				}
				return
			}
			if err != nil || credentials == nil {
				t.Fatalf("New() = (%#v, %#v), want credentials and nil error", credentials, err)
			}
		})
	}
}

func testCredentials(t *testing.T) *Credentials {
	t.Helper()
	credentials, err := New(Config{
		SessionCookieName:  "__Secure-dm_refresh",
		AuthFlowCookieName: "__Host-dm_auth",
		CookieSecure:       true,
		DevelopmentEnabled: true,
		DevelopmentRoute:   true,
		DevelopmentToken:   "development-token",
	}, "/api/v1/auth/sessions")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return credentials
}
