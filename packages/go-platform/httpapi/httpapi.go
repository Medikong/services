package httpapi

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Medikong/services/packages/go-platform/requestcontext"
	"github.com/samber/oops"
)

const (
	OopsHTTPStatusCodeKey = "http.status_code"
)

const (
	internalErrorCode    = "common.internal"
	internalErrorMessage = "요청 처리 중 오류가 발생했습니다."
)

type ErrorResponse struct {
	Error      ErrorBody `json:"error"`
	RequestID  string    `json:"requestId"`
	OccurredAt time.Time `json:"occurredAt"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Error(statusCode int, code string) oops.OopsErrorBuilder {
	return oops.Code(code).With(OopsHTTPStatusCodeKey, statusCode)
}

func BadRequest(code string) oops.OopsErrorBuilder {
	return Error(http.StatusBadRequest, code)
}

func Unauthorized(code string) oops.OopsErrorBuilder {
	return Error(http.StatusUnauthorized, code)
}

func Forbidden(code string) oops.OopsErrorBuilder {
	return Error(http.StatusForbidden, code)
}

func NotFound(code string) oops.OopsErrorBuilder {
	return Error(http.StatusNotFound, code)
}

func MethodNotAllowed(code string) oops.OopsErrorBuilder {
	return Error(http.StatusMethodNotAllowed, code)
}

func Conflict(code string) oops.OopsErrorBuilder {
	return Error(http.StatusConflict, code)
}

func Unprocessable(code string) oops.OopsErrorBuilder {
	return Error(http.StatusUnprocessableEntity, code)
}

func GatewayTimeout(code string) oops.OopsErrorBuilder {
	return Error(http.StatusGatewayTimeout, code)
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func DecodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return BadRequest("common.invalid_json").
			Public("요청 JSON을 해석할 수 없습니다.").
			Wrap(err)
	}
	return nil
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	statusCode, code, message := publicError(err)
	ReportError(r.Context(), err, statusCode, code)
	writeErrorResponse(w, r, statusCode, code, message)
}

func publicError(err error) (int, string, string) {
	if oopsErr, ok := oops.AsOops(err); ok {
		for _, layer := range oopsErr.Layers() {
			statusCode, ok := statusCodeFromContext(layer.Context)
			if !ok {
				continue
			}
			code := layerCode(layer.Code)
			if code == "" {
				code = internalErrorCode
			}
			message := strings.TrimSpace(layer.Public)
			if message == "" {
				message = internalErrorMessage
			}
			return statusCode, code, message
		}
	}
	return http.StatusInternalServerError, internalErrorCode, internalErrorMessage
}

func layerCode(value any) string {
	code, _ := value.(string)
	return strings.TrimSpace(code)
}

func writeErrorResponse(w http.ResponseWriter, r *http.Request, statusCode int, code, message string) {
	requestID := requestcontext.RequestID(r.Context())
	if requestID == "" {
		requestID = r.Header.Get(requestcontext.RequestIDHeader)
	}
	WriteJSON(w, statusCode, ErrorResponse{
		Error: ErrorBody{
			Code:    code,
			Message: message,
		},
		RequestID:  requestID,
		OccurredAt: time.Now().UTC(),
	})
}

func statusCodeFromContext(ctx map[string]any) (int, bool) {
	value, ok := ctx[OopsHTTPStatusCodeKey]
	if !ok {
		return 0, false
	}
	var statusCode int
	switch typed := value.(type) {
	case int:
		statusCode = typed
	case int64:
		statusCode = int(typed)
	case float64:
		if math.Trunc(typed) != typed {
			return 0, false
		}
		statusCode = int(typed)
	default:
		return 0, false
	}
	if statusCode < 400 || statusCode > 599 {
		return 0, false
	}
	return statusCode, true
}

type ErrorReporter func(error, int, string)

type errorReporterContextKey struct{}

func WithErrorReporter(ctx context.Context, reporter ErrorReporter) context.Context {
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, errorReporterContextKey{}, reporter)
}

func ReportError(ctx context.Context, err error, statusCode int, code string) {
	reporter, ok := ctx.Value(errorReporterContextKey{}).(ErrorReporter)
	if ok {
		reporter(err, statusCode, code)
	}
}
