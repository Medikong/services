package httpmiddleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samber/oops"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	nooptrace "go.opentelemetry.io/otel/trace/noop"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func TestErrorLogRecordsEachFailureOnce(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
	})

	tests := []struct {
		name      string
		wantLevel string
		handler   http.Handler
	}{
		{
			name:      "standard error",
			wantLevel: "ERROR",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpapi.WriteError(w, r, errors.New("plain failure"))
			}),
		},
		{
			name:      "client error",
			wantLevel: "WARN",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpapi.WriteError(w, r, httpapi.BadRequest("common.invalid").Public("잘못된 요청입니다.").New("invalid request"))
			}),
		},
		{
			name:      "server error",
			wantLevel: "ERROR",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpapi.WriteError(w, r, httpapi.Error(http.StatusServiceUnavailable, "common.unavailable").Public("잠시 후 다시 시도해 주세요.").New("backend unavailable"))
			}),
		},
		{
			name:      "panic",
			wantLevel: "ERROR",
			handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic("boom")
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger.Configure(&logs, "test-service")
			handler := Stack(Config{
				ServiceName: "test-service",
				RoutePattern: func(*http.Request) string {
					return "/test"
				},
			}, test.handler)
			request := httptest.NewRequest(http.MethodGet, "/test", nil)
			request.Header.Set(requestcontext.RequestIDHeader, "req-1")

			handler.ServeHTTP(httptest.NewRecorder(), request)

			events := decodeLogEvents(t, logs.Bytes())
			errorEvents := eventsWithMessage(events, "http.request.failed")
			if len(errorEvents) != 1 {
				t.Fatalf("error log count = %d, want 1; logs=%s", len(errorEvents), logs.String())
			}
			event := errorEvents[0]
			if event["level"] != test.wantLevel {
				t.Fatalf("level = %v, want %s", event["level"], test.wantLevel)
			}
			if event["request_id"] != "req-1" || event["trace_id"] == "" || event["span_id"] == "" {
				t.Fatalf("correlation fields = %#v", event)
			}
			if len(eventsWithMessage(events, "http.request.completed")) != 1 {
				t.Fatalf("access log count is not 1: %s", logs.String())
			}
		})
	}
}

func TestErrorLogMarksOnlyServerErrorsOnSpan(t *testing.T) {
	tests := []struct {
		name            string
		statusCode      int
		wantStatus      codes.Code
		wantErrorEvents int
	}{
		{name: "client error", statusCode: http.StatusBadRequest, wantStatus: codes.Unset},
		{name: "server error", statusCode: http.StatusInternalServerError, wantStatus: codes.Error, wantErrorEvents: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			otel.SetTracerProvider(provider)
			logger.Configure(io.Discard, "test-service")
			handler := Stack(Config{
				ServiceName: "test-service",
				RoutePattern: func(*http.Request) string {
					return "/test"
				},
			}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpapi.WriteError(w, r, httpapi.Error(test.statusCode, "test.failure").Public("요청을 처리하지 못했습니다.").New("test failure"))
			}))

			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil))
			spans := exporter.GetSpans()
			_ = provider.Shutdown(context.Background())
			otel.SetTracerProvider(nooptrace.NewTracerProvider())

			if len(spans) != 1 {
				t.Fatalf("span count = %d, want 1", len(spans))
			}
			if spans[0].Status.Code != test.wantStatus {
				t.Fatalf("span status = %v, want %v", spans[0].Status.Code, test.wantStatus)
			}
			errorEvents := 0
			errorMessage := ""
			for _, event := range spans[0].Events {
				if event.Name == "exception" {
					errorEvents++
					for _, attr := range event.Attributes {
						if string(attr.Key) == "exception.message" {
							errorMessage = attr.Value.AsString()
						}
					}
				}
			}
			if errorEvents != test.wantErrorEvents {
				t.Fatalf("span error events = %d, want %d", errorEvents, test.wantErrorEvents)
			}
			if test.wantErrorEvents > 0 && errorMessage != "http request failed" {
				t.Fatalf("span error message = %q, want safe message", errorMessage)
			}
		})
	}
}

func TestErrorLogRedactsSensitiveNestedContextWithoutLoggingRequest(t *testing.T) {
	const (
		rawToken = "token-that-must-not-leak"
		rawProof = "proof-that-must-not-leak"
		rawAuth  = "Bearer authorization-that-must-not-leak"
		rawBody  = "request-body-that-must-not-leak"
	)
	var logs bytes.Buffer
	logger.Configure(&logs, "test-service")
	handler := Stack(Config{ServiceName: "test-service"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := oops.
			In("repository").
			Code("repository.query_failed").
			With("auth", map[string]any{
				"token": rawToken,
				"items": []any{map[string]any{"registration_proof": rawProof}},
			}).
			New("query failed with " + rawToken + " and " + rawProof)
		err := httpapi.Error(http.StatusInternalServerError, "common.internal").
			Public("요청 처리 중 오류가 발생했습니다.").
			Wrap(inner)
		httpapi.WriteError(w, r, err)
	}))
	request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(rawBody))
	request.Header.Set("Authorization", rawAuth)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	for _, forbidden := range []string{rawToken, rawProof, rawAuth, rawBody, "repository.query_failed"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, response.Body.String())
		}
	}
	for _, forbidden := range []string{rawToken, rawProof, rawAuth, rawBody} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("logs exposed %q: %s", forbidden, logs.String())
		}
	}
	events := eventsWithMessage(decodeLogEvents(t, logs.Bytes()), "http.request.failed")
	if len(events) != 1 {
		t.Fatalf("error log count = %d, want 1", len(events))
	}
	errorValue := events[0]["error"].(map[string]any)
	if errorValue["code"] != "repository.query_failed" {
		t.Fatalf("oops code = %v", errorValue["code"])
	}
	ctx := errorValue["context"].(map[string]any)
	auth := ctx["auth"].(map[string]any)
	if auth["token"] != "[REDACTED]" {
		t.Fatalf("token = %v, want redacted", auth["token"])
	}
}

func decodeLogEvents(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var events []map[string]any
	for {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return events
			}
			t.Fatalf("decode log: %v", err)
		}
		events = append(events, event)
	}
}

func eventsWithMessage(events []map[string]any, message string) []map[string]any {
	var matching []map[string]any
	for _, event := range events {
		if event["msg"] == message {
			matching = append(matching, event)
		}
	}
	return matching
}
