package httputil

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/google/uuid"
)

const (
	IDHeader         = "X-Request-Id"
	csrfHeader       = "X-CSRF-Token"
	maxJSONBodyBytes = 1 << 20
)

type idContextKey struct{}

func IDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := ID(r)
		w.Header().Set(IDHeader, requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), idContextKey{}, requestID)))
	})
}

func ID(r *http.Request) string {
	if r != nil {
		if requestID, ok := r.Context().Value(idContextKey{}).(string); ok && requestID != "" {
			return requestID
		}
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		if parsed, err := uuid.Parse(strings.TrimSpace(r.Header.Get(IDHeader))); err == nil {
			requestID := parsed.String()
			r.Header.Set(IDHeader, requestID)
			return requestID
		}
		requestID := uuid.NewString()
		r.Header.Set(IDHeader, requestID)
		return requestID
	}
	return uuid.NewString()
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if target == nil || r == nil || r.Body == nil {
		return inputInvalid("missing_body")
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return inputInvalid("unsupported_media_type")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return decodeError(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return inputInvalid("trailing_data")
		}
		return decodeError(err)
	}
	return nil
}

func decodeError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return inputInvalid("body_too_large")
	}
	if errors.Is(err, io.EOF) {
		return inputInvalid("missing_body")
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		return inputInvalid("invalid_type")
	}
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return inputInvalid("additional_property")
	}
	return inputInvalid("invalid_json")
}

type CSRF struct {
	allowedOrigins map[string]struct{}
}

func NewCSRF(allowedOrigins []string) *CSRF {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}
	return &CSRF{allowedOrigins: allowed}
}

func (c *CSRF) Token(r *http.Request) (string, error) {
	if err := c.requireOrigin(r); err != nil {
		return "", err
	}
	tokens := r.Header.Values(csrfHeader)
	if len(tokens) != 1 || strings.TrimSpace(tokens[0]) == "" {
		return "", csrfInvalid()
	}
	return tokens[0], nil
}

func (c *CSRF) Verify(r *http.Request, verify func(string) bool) error {
	token, err := c.Token(r)
	if err != nil {
		return err
	}
	if verify == nil || !verify(token) {
		return csrfInvalid()
	}
	return nil
}

func (c *CSRF) requireOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	parsed, err := url.Parse(origin)
	if origin == "" || err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || origin != parsed.Scheme+"://"+parsed.Host {
		return csrfInvalid()
	}
	if _, ok := c.allowedOrigins[origin]; !ok {
		return csrfInvalid()
	}
	return nil
}

func inputInvalid(reason string) error {
	return httpapi.BadRequest("AUTH_INPUT_INVALID").
		Public("입력값을 확인한 뒤 다시 시도해주세요.").
		With("reason", reason).
		New("invalid request")
}

func csrfInvalid() error {
	return httpapi.Forbidden("AUTH_CSRF_INVALID").
		Public("인증 화면을 새로 연 뒤 다시 시도해주세요.").
		New("invalid CSRF token or origin")
}
