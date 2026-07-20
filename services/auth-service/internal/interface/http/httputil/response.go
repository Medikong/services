package httputil

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httpauth"
	"github.com/samber/oops"
)

const (
	cacheControlHeader   = "Cache-Control"
	cacheControlNoStore  = "no-store"
	internalErrorCode    = "AUTH_SERVICE_UNAVAILABLE"
	internalErrorMessage = "잠시 뒤 다시 시도해주세요."
)

type Meta struct {
	RequestID string `json:"requestId"`
}

type Envelope struct {
	Data any  `json:"data"`
	Meta Meta `json:"meta"`
}

type Error struct {
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

func WriteJSON(w http.ResponseWriter, r *http.Request, status int, data any) {
	if status == http.StatusNoContent {
		WriteNoContent(w, r)
		return
	}
	requestID := ID(r)
	setCommonHeaders(w, requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Data: data, Meta: Meta{RequestID: requestID}})
}

func WriteNoContent(w http.ResponseWriter, r *http.Request) {
	setCommonHeaders(w, ID(r))
	w.WriteHeader(http.StatusNoContent)
}

func VaryCredentials(w http.ResponseWriter) {
	w.Header().Set("Vary", "Cookie, Authorization")
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := publicError(err)
	if r != nil {
		httpapi.ReportError(r.Context(), err, status, code)
	}
	requestID := ID(r)
	setCommonHeaders(w, requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error{Status: status, Code: code, Message: message, RequestID: requestID})
}

func WriteCredentialError(w http.ResponseWriter, r *http.Request, err *httpauth.Error) {
	if err != nil && err.Kind == httpauth.Multiple {
		WriteError(w, r, failure.Wrap(
			failure.KindInvalid,
			"AUTH_MULTIPLE_CREDENTIALS",
			"인증 정보를 확인한 뒤 다시 시도해주세요.",
			err,
		))
		return
	}
	credentialFailure := failure.Unauthenticated(
		"AUTH_SESSION_REQUIRED",
		"인증 정보를 확인한 뒤 다시 시도해주세요.",
	)
	if err == nil {
		WriteError(w, r, credentialFailure)
		return
	}
	WriteError(w, r, failure.Wrap(
		credentialFailure.Kind,
		credentialFailure.Code,
		credentialFailure.PublicMessage,
		err,
	))
}

func publicError(err error) (int, string, string) {
	var failureErr *failure.Error
	if errors.As(err, &failureErr) {
		code := strings.TrimSpace(failureErr.Code)
		message := strings.TrimSpace(failureErr.PublicMessage)
		status, ok := failureStatus(failureErr.Kind, code)
		if ok && code != "" && message != "" {
			return status, code, message
		}
		return http.StatusServiceUnavailable, internalErrorCode, internalErrorMessage
	}
	if oopsErr, ok := oops.AsOops(err); ok {
		for _, layer := range oopsErr.Layers() {
			status, ok := statusCode(layer.Context)
			if !ok {
				continue
			}
			code, _ := layer.Code.(string)
			code = strings.TrimSpace(code)
			if code == "" {
				continue
			}
			message := strings.TrimSpace(layer.Public)
			if message == "" {
				message = internalErrorMessage
			}
			return status, code, message
		}
	}
	return http.StatusServiceUnavailable, internalErrorCode, internalErrorMessage
}

func failureStatus(kind failure.Kind, code string) (int, bool) {
	switch {
	case strings.HasSuffix(code, "_EXPIRED"):
		return http.StatusGone, true
	case code == "AUTH_VIRTUAL_MESSAGE_UNAVAILABLE", code == "AUTH_REAUTHENTICATION_PROOF_INVALID":
		return http.StatusGone, true
	case strings.HasPrefix(code, "AUTH_") && strings.HasSuffix(code, "_PRECONDITION_FAILED"):
		return http.StatusPreconditionFailed, true
	case code == "AUTH_PASSWORD_POLICY_NOT_MET":
		return http.StatusUnprocessableEntity, true
	case code == "AUTH_ACCOUNT_LOCKED":
		return http.StatusLocked, true
	}
	switch kind {
	case failure.KindInvalid:
		return http.StatusBadRequest, true
	case failure.KindUnauthenticated:
		return http.StatusUnauthorized, true
	case failure.KindForbidden:
		return http.StatusForbidden, true
	case failure.KindNotFound:
		return http.StatusNotFound, true
	case failure.KindConflict:
		return http.StatusConflict, true
	case failure.KindUnavailable:
		return http.StatusServiceUnavailable, true
	default:
		return 0, false
	}
}

func statusCode(context map[string]any) (int, bool) {
	value, ok := context[httpapi.OopsHTTPStatusCodeKey]
	if !ok {
		return 0, false
	}
	var status int
	switch value := value.(type) {
	case int:
		status = value
	case int64:
		status = int(value)
	case float64:
		if math.Trunc(value) != value {
			return 0, false
		}
		status = int(value)
	default:
		return 0, false
	}
	return status, status >= http.StatusBadRequest && status <= 599
}

func setCommonHeaders(w http.ResponseWriter, requestID string) {
	w.Header().Set(IDHeader, requestID)
	w.Header().Set(cacheControlHeader, cacheControlNoStore)
}
