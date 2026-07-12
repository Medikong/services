package httpcontract

import (
	"encoding/json"
	"net/http"
	"strings"
)

const problemContentType = "application/problem+json"

// FieldViolation contains a public request validation reason. It must never
// contain input values, credentials, or other sensitive data.
type FieldViolation struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// ProblemDetails is the OpenAPI ProblemDetails contract for every error
// response emitted by the authentication API.
type ProblemDetails struct {
	Type       string           `json:"type"`
	Title      string           `json:"title"`
	Status     int              `json:"status"`
	Code       string           `json:"code"`
	Detail     string           `json:"detail"`
	Retryable  bool             `json:"retryable"`
	RequestID  string           `json:"requestId"`
	Violations []FieldViolation `json:"violations,omitempty"`
}

// ContractError carries only data that is safe to expose through
// ProblemDetails. Database errors and credential values must be mapped before
// reaching this type.
type ContractError struct {
	Status     int
	Code       string
	Title      string
	Detail     string
	Retryable  bool
	Violations []FieldViolation
}

func (e *ContractError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *ContractError) problem(requestID string) ProblemDetails {
	status := e.Status
	code := strings.TrimSpace(e.Code)
	title := strings.TrimSpace(e.Title)
	detail := strings.TrimSpace(e.Detail)
	if status < http.StatusBadRequest || status > 599 || code == "" || title == "" || detail == "" {
		status = http.StatusServiceUnavailable
		code = "AUTH_SERVICE_UNAVAILABLE"
		title = "인증 서비스를 일시적으로 사용할 수 없습니다."
		detail = "잠시 뒤 다시 시도해주세요."
	}
	return ProblemDetails{
		Type:       problemType(code),
		Title:      title,
		Status:     status,
		Code:       code,
		Detail:     detail,
		Retryable:  e.Retryable,
		RequestID:  requestID,
		Violations: e.Violations,
	}
}

// NewContractError creates a public error that follows the API contract.
func NewContractError(status int, code, title, detail string, retryable bool, violations ...FieldViolation) *ContractError {
	return &ContractError{
		Status:     status,
		Code:       code,
		Title:      title,
		Detail:     detail,
		Retryable:  retryable,
		Violations: violations,
	}
}

func inputInvalid(reason string) *ContractError {
	return NewContractError(
		http.StatusBadRequest,
		"AUTH_INPUT_INVALID",
		"요청 형식이 올바르지 않습니다.",
		"입력값을 확인한 뒤 다시 시도해주세요.",
		false,
		FieldViolation{Field: "body", Reason: reason},
	)
}

func csrfInvalid() *ContractError {
	return NewContractError(
		http.StatusForbidden,
		"AUTH_CSRF_INVALID",
		"요청을 검증할 수 없습니다.",
		"인증 화면을 새로 연 뒤 다시 시도해주세요.",
		false,
	)
}

func problemType(code string) string {
	slug := strings.ToLower(strings.ReplaceAll(code, "_", "-"))
	return "https://api.dropmong.example/problems/" + slug
}

// WriteProblem writes an application/problem+json body together with the
// required no-store and request-ID headers.
func WriteProblem(w http.ResponseWriter, r *http.Request, problem *ContractError) {
	if problem == nil {
		problem = NewContractError(
			http.StatusServiceUnavailable,
			"AUTH_SERVICE_UNAVAILABLE",
			"인증 서비스를 일시적으로 사용할 수 없습니다.",
			"잠시 뒤 다시 시도해주세요.",
			true,
		)
	}
	requestID := requestIDFor(r)
	setCommonResponseHeaders(w, requestID)
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(problem.problem(requestID).Status)
	_ = json.NewEncoder(w).Encode(problem.problem(requestID))
}
