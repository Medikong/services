package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	apperrors "github.com/Medikong/services/packages/go-platform/errors"
	"github.com/Medikong/services/packages/go-platform/requestcontext"
	"github.com/samber/oops"
)

const (
	OopsHTTPStatusCodeKey = "http.status_code"
	OopsDetailsKey        = "http.response.details"
)

func NewError(statusCode int, code, message string, details map[string]any) error {
	builder := oops.
		Code(code).
		Public(message).
		With(OopsHTTPStatusCodeKey, statusCode)
	if len(details) > 0 {
		builder = builder.With(OopsDetailsKey, cloneDetails(details))
	}
	return builder.New(message)
}

func WrapError(err error, statusCode int, code, message string, details map[string]any) error {
	if err == nil {
		return NewError(statusCode, code, message, details)
	}
	builder := oops.
		Code(code).
		Public(message).
		With(OopsHTTPStatusCodeKey, statusCode)
	if len(details) > 0 {
		builder = builder.With(OopsDetailsKey, cloneDetails(details))
	}
	return builder.Wrap(err)
}

func BadRequest(code, message string) error {
	return NewError(http.StatusBadRequest, code, message, nil)
}

func Unauthorized(code, message string) error {
	return NewError(http.StatusUnauthorized, code, message, nil)
}

func Forbidden(code, message string) error {
	return NewError(http.StatusForbidden, code, message, nil)
}

func Conflict(code, message string) error {
	return NewError(http.StatusConflict, code, message, nil)
}

func Unprocessable(code, message string) error {
	return NewError(http.StatusUnprocessableEntity, code, message, nil)
}

func Internal(err error) error {
	return WrapError(err, http.StatusInternalServerError, "common.internal", "요청 처리 중 오류가 발생했습니다.", nil)
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
		return BadRequest("common.invalid_json", "요청 JSON을 해석할 수 없습니다.")
	}
	return nil
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if oopsErr, ok := oops.AsOops(err); ok {
		writeOops(w, r, oopsErr)
		return
	}

	if oopsErr, ok := oops.AsOops(Internal(err)); ok {
		writeOops(w, r, oopsErr)
		return
	}
}

func writeOops(w http.ResponseWriter, r *http.Request, err oops.OopsError) {
	ctx := err.Context()
	statusCode := statusCodeFromContext(ctx)
	code := ""
	if value := err.Code(); value != nil {
		code = fmt.Sprint(value)
	}
	if code == "" {
		code = "common.internal"
	}
	message := err.Public()
	if message == "" {
		message = "요청 처리 중 오류가 발생했습니다."
	}
	requestID := requestcontext.RequestID(r.Context())
	if requestID == "" {
		requestID = r.Header.Get(requestcontext.RequestIDHeader)
	}
	WriteJSON(w, statusCode, apperrors.ErrorResponse{
		Error: apperrors.ErrorBody{
			Code:    code,
			Message: message,
			Details: detailsFromContext(ctx),
		},
		RequestID:  requestID,
		OccurredAt: time.Now().UTC(),
	})
}

func OopsDetails(kv ...any) map[string]any {
	if len(kv) == 0 {
		return nil
	}
	details := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok || key == "" {
			continue
		}
		if i+1 >= len(kv) {
			details[key] = nil
			continue
		}
		details[key] = kv[i+1]
	}
	return details
}

func cloneDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	out := make(map[string]any, len(details))
	for key, value := range details {
		out[key] = value
	}
	return out
}

func ErrorCode(err error) string {
	if oopsErr, ok := oops.AsOops(err); ok {
		return fmt.Sprint(oopsErr.Code())
	}
	return ""
}

func statusCodeFromContext(ctx map[string]any) int {
	value, ok := ctx[OopsHTTPStatusCodeKey]
	if !ok {
		return http.StatusInternalServerError
	}
	switch typed := value.(type) {
	case int:
		if typed != 0 {
			return typed
		}
	case int64:
		if typed != 0 {
			return int(typed)
		}
	case float64:
		if typed != 0 {
			return int(typed)
		}
	}
	return http.StatusInternalServerError
}

func detailsFromContext(ctx map[string]any) map[string]any {
	value, ok := ctx[OopsDetailsKey]
	if !ok {
		return nil
	}
	details, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return cloneDetails(details)
}
