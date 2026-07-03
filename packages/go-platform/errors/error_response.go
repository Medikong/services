package apperrors

import "time"

type ErrorResponse struct {
	Error      ErrorBody `json:"error"`
	RequestID  string    `json:"requestId"`
	OccurredAt time.Time `json:"occurredAt"`
}

type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}
