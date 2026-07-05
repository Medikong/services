package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	apperrors "github.com/Medikong/services/packages/go-platform/errors"
)

type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e Error) Error() string {
	return e.Message
}

func BadRequest(code, message string) Error {
	return Error{Status: http.StatusBadRequest, Code: code, Message: message}
}

func Unauthorized(code, message string) Error {
	return Error{Status: http.StatusUnauthorized, Code: code, Message: message}
}

func Forbidden(code, message string) Error {
	return Error{Status: http.StatusForbidden, Code: code, Message: message}
}

func Conflict(code, message string) Error {
	return Error{Status: http.StatusConflict, Code: code, Message: message}
}

func Unprocessable(code, message string) Error {
	return Error{Status: http.StatusUnprocessableEntity, Code: code, Message: message}
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
	var apiErr Error
	if !errors.As(err, &apiErr) {
		apiErr = Error{Status: http.StatusInternalServerError, Code: "common.internal", Message: "요청 처리 중 오류가 발생했습니다."}
	}
	requestID := r.Header.Get("X-Request-Id")
	WriteJSON(w, apiErr.Status, apperrors.ErrorResponse{
		Error: apperrors.ErrorBody{
			Code:    apiErr.Code,
			Message: apiErr.Message,
			Details: apiErr.Details,
		},
		RequestID:  requestID,
		OccurredAt: time.Now().UTC(),
	})
}
