package httpcontract

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-authz/principal"
	contractheaders "github.com/Medikong/services/packages/go-contracts/headers"
)

const (
	RequestIDHeader    = "X-Request-Id"
	TraceparentHeader  = "traceparent"
	CSRFHeader         = "X-CSRF-Token"
	ApprovalRefHeader  = "X-Approval-Ref"
	CaseRefHeader      = "X-Case-Ref"
	CacheControlHeader = "Cache-Control"
	CacheControlValue  = "private, no-store"
	ProblemContentType = "application/problem+json"
	MaxJSONBodyBytes   = 1 << 20
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

type Boundary string

const (
	BoundaryPublic   Boundary = "public"
	BoundaryWorkload Boundary = "workload"
)

type principalContextKey struct{}
type requestIDContextKey struct{}

type Contract struct {
	allowedOrigins map[string]struct{}
}

func New(allowedOrigins []string) (Contract, error) {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, candidate := range allowedOrigins {
		origin := strings.TrimSpace(candidate)
		if origin == "" {
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || origin != parsed.Scheme+"://"+parsed.Host {
			return Contract{}, oops.Errorf("coupon HTTP allowed origin is invalid: %q", candidate)
		}
		origins[origin] = struct{}{}
	}
	return Contract{allowedOrigins: origins}, nil
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(RequestIDHeader))
		parsed, err := uuid.Parse(requestID)
		if err != nil {
			requestID = uuid.NewString()
		} else {
			requestID = parsed.String()
		}
		r.Header.Set(RequestIDHeader, requestID)
		w.Header().Set(RequestIDHeader, requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKey{}, requestID)))
	})
}

func RequestID(r *http.Request) string {
	if r != nil {
		if value, ok := r.Context().Value(requestIDContextKey{}).(string); ok && value != "" {
			return value
		}
		if parsed, err := uuid.Parse(strings.TrimSpace(r.Header.Get(RequestIDHeader))); err == nil {
			return parsed.String()
		}
	}
	return uuid.NewString()
}

func (c Contract) Authenticate(boundary Boundary, mutation bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header, problem := requiredUniqueHeader(r, contractheaders.Principal, 16*1024)
			if problem != nil {
				WriteProblem(w, r, authenticationRequired())
				return
			}
			value, err := principal.DecodeHeader(header)
			if err != nil {
				WriteProblem(w, r, authenticationRequired())
				return
			}
			switch boundary {
			case BoundaryPublic:
				if value.Type != principal.TypeUser || strings.TrimSpace(value.UserID) == "" || strings.TrimSpace(value.ServiceID) != "" {
					WriteProblem(w, r, authenticationRequired())
					return
				}
				// An explicit mobile principal is the only public mutation channel
				// that does not require the web CSRF and Origin headers.
				if mutation && value.ClientType != "mobile" {
					if problem := c.requireWebMutation(r); problem != nil {
						WriteProblem(w, r, problem)
						return
					}
				}
			case BoundaryWorkload:
				if value.Type != principal.TypeService || strings.TrimSpace(value.ServiceID) == "" || strings.TrimSpace(value.UserID) != "" {
					WriteProblem(w, r, authenticationRequired())
					return
				}
			default:
				WriteProblem(w, r, internalProblem(nil))
				return
			}
			ctx := context.WithValue(r.Context(), principalContextKey{}, value)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func Principal(ctx context.Context) principal.Principal {
	value, _ := ctx.Value(principalContextKey{}).(principal.Principal)
	return value
}

func (c Contract) requireWebMutation(r *http.Request) *Error {
	origin, problem := requiredUniqueHeader(r, "Origin", 2048)
	if problem != nil {
		return forbidden("origin_required")
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || origin != parsed.Scheme+"://"+parsed.Host {
		return forbidden("origin_invalid")
	}
	if _, allowed := c.allowedOrigins[origin]; !allowed {
		return forbidden("origin_forbidden")
	}
	if _, problem := requiredUniqueHeader(r, CSRFHeader, 2048); problem != nil {
		return forbidden("csrf_required")
	}
	return nil
}

type HeaderRules struct {
	Idempotency bool
	Approval    bool
	Case        bool
}

type Headers struct {
	RequestID      string
	Traceparent    string
	IdempotencyKey string
	ApprovalRef    string
	CaseRef        string
}

func ReadHeaders(r *http.Request, rules HeaderRules) (Headers, *Error) {
	result := Headers{RequestID: RequestID(r)}
	traceparent, problem := optionalUniqueHeader(r, TraceparentHeader, 128)
	if problem != nil {
		return Headers{}, problem
	}
	result.Traceparent = traceparent

	idempotencyKey, problem := optionalUniqueHeader(r, contractheaders.IdempotencyKey, 128)
	if problem != nil {
		return Headers{}, problem
	}
	if rules.Idempotency {
		if len(idempotencyKey) < 16 || !idempotencyKeyPattern.MatchString(idempotencyKey) {
			return Headers{}, inputInvalid("Idempotency-Key", "invalid_header")
		}
	} else if idempotencyKey != "" {
		return Headers{}, inputInvalid("Idempotency-Key", "unexpected_header")
	}
	result.IdempotencyKey = idempotencyKey

	approvalRef, problem := optionalUniqueHeader(r, ApprovalRefHeader, 160)
	if problem != nil {
		return Headers{}, problem
	}
	if rules.Approval && approvalRef == "" {
		return Headers{}, inputInvalid(ApprovalRefHeader, "required_header")
	}
	result.ApprovalRef = approvalRef

	caseRef, problem := optionalUniqueHeader(r, CaseRefHeader, 160)
	if problem != nil {
		return Headers{}, problem
	}
	if rules.Case && caseRef == "" {
		return Headers{}, inputInvalid(CaseRefHeader, "required_header")
	}
	result.CaseRef = caseRef
	return result, nil
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, target any) *Error {
	if target == nil || r == nil || r.Body == nil {
		return inputInvalid("body", "missing_body")
	}
	values := r.Header.Values("Content-Type")
	if len(values) != 1 {
		return inputInvalid("Content-Type", "unsupported_media_type")
	}
	mediaType, _, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return inputInvalid("Content-Type", "unsupported_media_type")
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return decodeProblem(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return inputInvalid("body", "trailing_data")
		}
		return decodeProblem(err)
	}
	return nil
}

func decodeProblem(err error) *Error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return inputInvalid("body", "body_too_large")
	}
	if errors.Is(err, io.EOF) {
		return inputInvalid("body", "missing_body")
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		return inputInvalid(typeError.Field, "invalid_type")
	}
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return inputInvalid("body", "additional_property")
	}
	return inputInvalid("body", "invalid_json")
}

type Violation struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type ProblemDetails struct {
	Type       string      `json:"type"`
	Title      string      `json:"title"`
	Status     int         `json:"status"`
	Code       string      `json:"code"`
	Detail     string      `json:"detail,omitempty"`
	Retryable  bool        `json:"retryable"`
	RequestID  string      `json:"requestId"`
	Violations []Violation `json:"violations,omitempty"`
}

type Error struct {
	Status            int
	Code              string
	Title             string
	Detail            string
	Retryable         bool
	RetryAfterSeconds int
	Violations        []Violation
	Cause             error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Detail
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type ProblemSource interface {
	HTTPProblem() *Error
}

func FromError(err error) *Error {
	if err == nil {
		return nil
	}
	var source ProblemSource
	if errors.As(err, &source) {
		if problem := source.HTTPProblem(); problem != nil {
			return problem
		}
	}
	var contractError *Error
	if errors.As(err, &contractError) {
		return contractError
	}
	return internalProblem(err)
}

type ResponseMeta struct {
	RequestID string `json:"requestId"`
	AsOf      string `json:"asOf,omitempty"`
}

type Envelope struct {
	Data any          `json:"data"`
	Meta ResponseMeta `json:"meta"`
}

func WriteJSON(w http.ResponseWriter, r *http.Request, status int, data any, asOf string) {
	requestID := RequestID(r)
	setCommonHeaders(w, requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Data: data, Meta: ResponseMeta{RequestID: requestID, AsOf: asOf}})
}

func WriteProblem(w http.ResponseWriter, r *http.Request, problem *Error) {
	if problem == nil {
		problem = internalProblem(nil)
	}
	requestID := RequestID(r)
	setCommonHeaders(w, requestID)
	if problem.Retryable {
		retryAfter := problem.RetryAfterSeconds
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(problem.Status)
	_ = json.NewEncoder(w).Encode(ProblemDetails{
		Type:       problemType(problem.Code),
		Title:      problem.Title,
		Status:     problem.Status,
		Code:       problem.Code,
		Detail:     problem.Detail,
		Retryable:  problem.Retryable,
		RequestID:  requestID,
		Violations: problem.Violations,
	})
}

// Timeout keeps deadline failures inside the coupon ProblemDetails contract.
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			recorder := &deadlineResponseWriter{ResponseWriter: w}
			next.ServeHTTP(recorder, r.WithContext(ctx))
			if ctx.Err() == context.DeadlineExceeded && !recorder.wroteHeader {
				WriteProblem(w, r.WithContext(ctx), internalProblem(ctx.Err()))
			}
		})
	}
}

type deadlineResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *deadlineResponseWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *deadlineResponseWriter) Write(body []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(body)
}

func (w *deadlineResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func setCommonHeaders(w http.ResponseWriter, requestID string) {
	w.Header().Set(RequestIDHeader, requestID)
	w.Header().Set(CacheControlHeader, CacheControlValue)
}

func inputInvalid(field, reason string) *Error {
	return &Error{
		Status:     http.StatusBadRequest,
		Code:       "COUPON_INPUT_INVALID",
		Title:      "요청이 올바르지 않습니다.",
		Detail:     "요청 형식과 필수 값을 확인해 주세요.",
		Violations: []Violation{{Field: field, Reason: reason}},
	}
}

func InputInvalid(field, reason string) *Error {
	return inputInvalid(field, reason)
}

func NotFound() *Error {
	return &Error{
		Status: http.StatusNotFound,
		Code:   "COUPON_NOT_FOUND",
		Title:  "요청한 API를 찾을 수 없습니다.",
		Detail: "요청 경로를 확인해 주세요.",
	}
}

func MethodNotAllowed() *Error {
	return &Error{
		Status: http.StatusMethodNotAllowed,
		Code:   "COUPON_METHOD_NOT_ALLOWED",
		Title:  "허용되지 않은 HTTP 메서드입니다.",
		Detail: "OpenAPI에 정의된 메서드를 사용해 주세요.",
	}
}

func Internal(cause error) *Error {
	return internalProblem(cause)
}

func authenticationRequired() *Error {
	return &Error{
		Status: http.StatusUnauthorized,
		Code:   "COUPON_AUTHENTICATION_REQUIRED",
		Title:  "인증이 필요합니다.",
		Detail: "Gateway가 검증한 Principal 정보가 필요합니다.",
	}
}

func forbidden(reason string) *Error {
	return &Error{
		Status:     http.StatusForbidden,
		Code:       "COUPON_FORBIDDEN",
		Title:      "요청 권한이 없습니다.",
		Detail:     "요청의 보안 조건을 확인해 주세요.",
		Violations: []Violation{{Field: "security", Reason: reason}},
	}
}

func internalProblem(cause error) *Error {
	return &Error{
		Status:    http.StatusServiceUnavailable,
		Code:      "COUPON_DEPENDENCY_UNAVAILABLE",
		Title:     "필수 의존성을 사용할 수 없습니다.",
		Detail:    "잠시 뒤 다시 시도해 주세요.",
		Retryable: true,
		Cause:     cause,
	}
}

func problemType(code string) string {
	slug := strings.ToLower(strings.ReplaceAll(code, "_", "-"))
	return "https://api.dropmong.example/problems/" + slug
}

func requiredUniqueHeader(r *http.Request, name string, maxLength int) (string, *Error) {
	value, problem := optionalUniqueHeader(r, name, maxLength)
	if problem != nil {
		return "", problem
	}
	if value == "" {
		return "", inputInvalid(name, "required_header")
	}
	return value, nil
}

func optionalUniqueHeader(r *http.Request, name string, maxLength int) (string, *Error) {
	if r == nil {
		return "", inputInvalid(name, "missing_request")
	}
	values := r.Header.Values(name)
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 {
		return "", inputInvalid(name, "multiple_header_values")
	}
	value := strings.TrimSpace(values[0])
	if value == "" || len(value) > maxLength || strings.ContainsAny(value, "\r\n") {
		return "", inputInvalid(name, "invalid_header")
	}
	return value, nil
}
