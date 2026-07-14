package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
	platformmiddleware "github.com/Medikong/services/packages/go-platform/httpmiddleware"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/user-service/internal/application"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
	"github.com/Medikong/services/services/user-service/internal/platform/observability"
	"github.com/Medikong/services/services/user-service/internal/security"
)

const (
	createUserPath         = "/users"
	getOwnProfilePath      = "/users/me/profile"
	profileImagePath       = "/users/me/profile-image"
	changeStatusPath       = "/operator/users/{userId}/status-transitions"
	getStatusPath          = "/operator/users/{userId}/status"
	csrfVerifiedHeader     = "X-Csrf-Verified"
	developmentTokenHeader = "X-Development-Token"
)

type RouteContract struct {
	Method string
	Path   string
}

var BusinessRoutes = []RouteContract{
	{Method: http.MethodPost, Path: "/api/v1" + createUserPath},
	{Method: http.MethodGet, Path: "/api/v1" + getOwnProfilePath},
	{Method: http.MethodPatch, Path: "/api/v1" + getOwnProfilePath},
	{Method: http.MethodPut, Path: "/api/v1" + profileImagePath},
	{Method: http.MethodPost, Path: "/api/v1" + changeStatusPath},
	{Method: http.MethodGet, Path: "/api/v1" + getStatusPath},
}

type RouterConfig struct {
	ServiceName    string
	RequestTimeout time.Duration
	AllowedOrigins map[string]struct{}
	Development    DevelopmentProofConfig
}

type DevelopmentProofConfig struct {
	Enabled     bool
	AccessToken string
	AuthSigner  security.Signer
	MediaSigner security.Signer
	ProofTTL    time.Duration
}

type Handler struct {
	service *application.Service
	metrics *observability.Metrics
	config  RouterConfig
}

type principalContextKey struct{}

func NewRouter(cfg RouterConfig, service *application.Service, health *operational.Handler, metrics *observability.Metrics) (http.Handler, error) {
	if service == nil || health == nil || metrics == nil || cfg.ServiceName == "" || cfg.RequestTimeout <= 0 {
		return nil, errors.New("router dependencies and request timeout are required")
	}
	handler := &Handler{service: service, metrics: metrics, config: cfg}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return platformmiddleware.Stack(platformmiddleware.Config{ServiceName: cfg.ServiceName, RoutePattern: RoutePattern}, next)
	})
	router.Use(platformmiddleware.Timeout(cfg.RequestTimeout))
	router.Use(health.RejectWhileDraining)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, r, httpapi.NotFound("common.not_found", "요청한 API를 찾을 수 없습니다."))
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, r, httpapi.MethodNotAllowed("common.method_not_allowed", "허용되지 않은 HTTP 메서드입니다."))
	})

	router.Route("/api/v1", func(api chi.Router) {
		api.With(handler.requireAllowedOrigin).Post(createUserPath, handler.CreateUser)
		api.Group(func(own chi.Router) {
			own.Use(requirePrincipal)
			own.Get(getOwnProfilePath, handler.GetOwnProfile)
			own.With(handler.requireAllowedOrigin).Patch(getOwnProfilePath, handler.UpdateOwnProfile)
			own.With(handler.requireAllowedOrigin).Put(profileImagePath, handler.UpdateOwnProfileImage)
		})
		api.Group(func(operator chi.Router) {
			operator.Use(requirePrincipal)
			operator.With(handler.requireOperator("user.account_status.change", true)).Post(changeStatusPath, handler.ChangeUserAccountStatus)
			operator.With(handler.requireOperator("user.account_status.read", false)).Get(getStatusPath, handler.GetUserAccountStatus)
		})
	})
	if cfg.Development.Enabled {
		if cfg.Development.AccessToken == "" || cfg.Development.ProofTTL <= 0 {
			return nil, errors.New("development proof routes require a token and positive proof TTL")
		}
		router.Route("/internal/dev/proofs", func(dev chi.Router) {
			dev.Use(handler.requireDevelopmentAccess)
			dev.Post("/registration", handler.IssueRegistrationProof)
			dev.Post("/media", handler.IssueMediaProof)
		})
	}
	return router, nil
}

func RoutePattern(r *http.Request) string {
	pattern := chi.RouteContext(r.Context()).RoutePattern()
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

func requirePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
		if err != nil || value.Type != principal.TypeUser || value.UserID == "" {
			httpapi.WriteError(w, r, httpapi.Unauthorized("USER_AUTHENTICATION_REQUIRED", "사용자 인증이 필요합니다."))
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, value)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestPrincipal(ctx context.Context) principal.Principal {
	value, _ := ctx.Value(principalContextKey{}).(principal.Principal)
	return value
}

func (h *Handler) requireAllowedOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.config.AllowedOrigins[strings.TrimSpace(r.Header.Get("Origin"))]; !ok {
			httpapi.WriteError(w, r, httpapi.Forbidden("USER_FORBIDDEN", "허용되지 않은 요청 출처입니다."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) requireOperator(permission string, mutation bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := requestPrincipal(r.Context())
			code := "USER_FORBIDDEN"
			if mutation {
				code = "USER_ACCOUNT_STATUS_PERMISSION_DENIED"
			}
			if value.SessionID == "" || value.AuthLevel != "strong" || !value.HasRole(permission) {
				httpapi.WriteError(w, r, httpapi.Forbidden(code, "계정 상태를 조회하거나 변경할 권한이 없습니다."))
				return
			}
			if mutation {
				if _, ok := h.config.AllowedOrigins[strings.TrimSpace(r.Header.Get("Origin"))]; !ok || !strings.EqualFold(strings.TrimSpace(r.Header.Get(csrfVerifiedHeader)), "true") {
					httpapi.WriteError(w, r, httpapi.Forbidden(code, "운영 요청의 출처 또는 CSRF 검증이 유효하지 않습니다."))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (h *Handler) requireDevelopmentAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get(developmentTokenHeader))
		want := []byte(h.config.Development.AccessToken)
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			httpapi.WriteError(w, r, httpapi.Forbidden("USER_DEVELOPMENT_ACCESS_DENIED", "개발 전용 API 접근이 거부되었습니다."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RegistrationID              string `json:"registrationId"`
		RegistrationCompletionProof string `json:"registrationCompletionProof"`
		Profile                     struct {
			PrivateName  string  `json:"privateName"`
			Nickname     string  `json:"nickname"`
			Introduction *string `json:"introduction"`
		} `json:"profile"`
		RequiredAgreements []struct {
			AgreementCode    string    `json:"agreementCode"`
			AgreementVersion string    `json:"agreementVersion"`
			AcceptedAt       time.Time `json:"acceptedAt"`
		} `json:"requiredAgreements"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.writeError(w, r, "create_user", err)
		return
	}
	agreements := make([]user.AgreementAcceptance, 0, len(request.RequiredAgreements))
	for _, agreement := range request.RequiredAgreements {
		agreements = append(agreements, user.AgreementAcceptance{Code: agreement.AgreementCode, Version: agreement.AgreementVersion, AcceptedAt: agreement.AcceptedAt})
	}
	result, err := h.service.CreateUser(r.Context(), application.CreateUserInput{
		RegistrationID:              request.RegistrationID,
		RegistrationCompletionProof: request.RegistrationCompletionProof,
		PrivateName:                 request.Profile.PrivateName,
		Nickname:                    request.Profile.Nickname,
		Introduction:                request.Profile.Introduction,
		RequiredAgreements:          agreements,
		IdempotencyKey:              r.Header.Get(headers.IdempotencyKey),
	})
	if err != nil {
		h.writeError(w, r, "create_user", err)
		return
	}
	status := http.StatusCreated
	metricResult := "created"
	if result.Replayed {
		status = http.StatusOK
		metricResult = "replayed"
	}
	h.metrics.RecordOperation("create_user", metricResult)
	httpapi.WriteJSON(w, status, map[string]any{"data": map[string]any{
		"userId":            result.UserID,
		"userVersion":       result.UserVersion,
		"createdAt":         result.CreatedAt,
		"userCreationProof": result.UserCreationProof,
	}})
}

func (h *Handler) GetOwnProfile(w http.ResponseWriter, r *http.Request) {
	id, err := principalUserID(r.Context())
	if err != nil {
		h.writeError(w, r, "get_own_profile", err)
		return
	}
	current, err := h.service.GetOwnProfile(r.Context(), id)
	if err != nil {
		h.writeError(w, r, "get_own_profile", err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.metrics.RecordOperation("get_own_profile", "success")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": profileResponse(current)})
}

func (h *Handler) UpdateOwnProfile(w http.ResponseWriter, r *http.Request) {
	id, err := principalUserID(r.Context())
	if err != nil {
		h.writeError(w, r, "update_own_profile", err)
		return
	}
	var request struct {
		ExpectedUserVersion int64          `json:"expectedUserVersion"`
		Nickname            optionalString `json:"nickname"`
		Introduction        optionalString `json:"introduction"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.writeError(w, r, "update_own_profile", err)
		return
	}
	patch := user.ProfilePatch{NicknameSet: request.Nickname.Set, IntroductionSet: request.Introduction.Set}
	if request.Nickname.Value != nil {
		patch.Nickname = *request.Nickname.Value
	}
	patch.Introduction = request.Introduction.Value
	result, err := h.service.UpdateOwnProfile(r.Context(), application.UpdateProfileInput{
		UserID: id, ExpectedUserVersion: request.ExpectedUserVersion, Patch: patch, IdempotencyKey: r.Header.Get(headers.IdempotencyKey),
	})
	if err != nil {
		h.writeError(w, r, "update_own_profile", err)
		return
	}
	h.recordReplay("update_own_profile", result.Replayed)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"userId":        result.UserID,
		"userVersion":   result.UserVersion,
		"changedFields": result.ChangedFields,
		"updatedAt":     result.UpdatedAt,
	}})
}

func (h *Handler) UpdateOwnProfileImage(w http.ResponseWriter, r *http.Request) {
	id, err := principalUserID(r.Context())
	if err != nil {
		h.writeError(w, r, "update_own_profile_image", err)
		return
	}
	var request struct {
		MediaAssetID        string `json:"mediaAssetId"`
		MediaAssetProof     string `json:"mediaAssetProof"`
		ExpectedUserVersion int64  `json:"expectedUserVersion"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.writeError(w, r, "update_own_profile_image", err)
		return
	}
	result, err := h.service.UpdateOwnProfileImage(r.Context(), application.UpdateProfileImageInput{
		UserID: id, MediaAssetID: request.MediaAssetID, MediaAssetProof: request.MediaAssetProof,
		ExpectedUserVersion: request.ExpectedUserVersion, IdempotencyKey: r.Header.Get(headers.IdempotencyKey),
	})
	if err != nil {
		h.writeError(w, r, "update_own_profile_image", err)
		return
	}
	h.recordReplay("update_own_profile_image", result.Replayed)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"userId":              result.UserID,
		"profileMediaAssetId": result.ProfileMediaAssetID,
		"userVersion":         result.UserVersion,
		"updatedAt":           result.UpdatedAt,
	}})
}

func (h *Handler) ChangeUserAccountStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		h.writeError(w, r, "change_user_account_status", application.NewProblem(http.StatusUnprocessableEntity, "USER_INPUT_INVALID", "userId 형식이 올바르지 않습니다.", err))
		return
	}
	var request struct {
		TargetStatus        string `json:"targetStatus"`
		ReasonCode          string `json:"reasonCode"`
		ExpectedUserVersion int64  `json:"expectedUserVersion"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.writeError(w, r, "change_user_account_status", err)
		return
	}
	actor := requestPrincipal(r.Context())
	result, err := h.service.ChangeUserAccountStatus(r.Context(), application.ChangeStatusInput{
		UserID: id, TargetStatus: request.TargetStatus, ReasonCode: request.ReasonCode,
		ExpectedUserVersion: request.ExpectedUserVersion, ChangedBy: actor.UserID, IdempotencyKey: r.Header.Get(headers.IdempotencyKey),
	})
	if err != nil {
		h.writeError(w, r, "change_user_account_status", err)
		return
	}
	h.recordReplay("change_user_account_status", result.Replayed)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"statusChangeId":        result.StatusChangeID,
		"userId":                result.UserID,
		"accountStatus":         result.AccountStatus,
		"userVersion":           result.UserVersion,
		"changedAt":             result.ChangedAt,
		"userStatusChangeProof": result.UserStatusChangeProof,
	}})
}

func (h *Handler) GetUserAccountStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		h.writeError(w, r, "get_user_account_status", application.NewProblem(http.StatusUnprocessableEntity, "USER_INPUT_INVALID", "userId 형식이 올바르지 않습니다.", err))
		return
	}
	current, err := h.service.GetAccountStatus(r.Context(), id)
	if err != nil {
		h.writeError(w, r, "get_user_account_status", err)
		return
	}
	h.metrics.RecordOperation("get_user_account_status", "success")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"userId":        current.ID,
		"accountStatus": current.AccountStatus,
		"userVersion":   current.Version,
		"updatedAt":     current.UpdatedAt,
	}})
}

func (h *Handler) IssueRegistrationProof(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RegistrationID string `json:"registrationId"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.writeError(w, r, "development_registration_proof", err)
		return
	}
	proof, err := h.config.Development.AuthSigner.Sign(security.ProofClaims{
		Audience: "user-service", Purpose: "create_user", RegistrationID: strings.TrimSpace(request.RegistrationID),
		EmailVerified: true, PhoneVerified: true, Nonce: uuid.NewString(),
	}, h.config.Development.ProofTTL)
	if err != nil {
		h.writeError(w, r, "development_registration_proof", err)
		return
	}
	h.metrics.RecordOperation("development_registration_proof", "success")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"registrationCompletionProof": proof}})
}

func (h *Handler) IssueMediaProof(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID       string `json:"userId"`
		MediaAssetID string `json:"mediaAssetId"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.writeError(w, r, "development_media_proof", err)
		return
	}
	if _, err := uuid.Parse(request.UserID); err != nil {
		h.writeError(w, r, "development_media_proof", application.NewProblem(http.StatusUnprocessableEntity, "USER_INPUT_INVALID", "userId 형식이 올바르지 않습니다.", err))
		return
	}
	proof, err := h.config.Development.MediaSigner.Sign(security.ProofClaims{
		Audience: "user-service", Purpose: "user_profile", UserID: request.UserID,
		MediaAssetID: strings.TrimSpace(request.MediaAssetID), ScanCompleted: true, Nonce: uuid.NewString(),
	}, h.config.Development.ProofTTL)
	if err != nil {
		h.writeError(w, r, "development_media_proof", err)
		return
	}
	h.metrics.RecordOperation("development_media_proof", "success")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"mediaAssetProof": proof}})
}

func profileResponse(current user.User) map[string]any {
	return map[string]any{
		"userId":              current.ID,
		"accountStatus":       current.AccountStatus,
		"nickname":            current.Nickname,
		"introduction":        current.Introduction,
		"profileMediaAssetId": current.ProfileMediaAssetID,
		"userVersion":         current.Version,
		"updatedAt":           current.UpdatedAt,
	}
}

func principalUserID(ctx context.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(requestPrincipal(ctx).UserID)
	if err != nil {
		return uuid.Nil, application.NewProblem(http.StatusUnauthorized, "USER_AUTHENTICATION_REQUIRED", "사용자 인증이 필요합니다.", err)
	}
	return id, nil
}

func decodeJSON(r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return application.NewProblem(http.StatusUnprocessableEntity, "USER_INPUT_INVALID", "요청 JSON을 해석할 수 없습니다.", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return application.NewProblem(http.StatusUnprocessableEntity, "USER_INPUT_INVALID", "요청 JSON 뒤에 불필요한 값이 있습니다.", err)
	}
	return nil
}

type optionalString struct {
	Set   bool
	Value *string
}

func (o *optionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	problem := application.AsProblem(err)
	h.metrics.RecordOperation(operation, "error")
	httpapi.WriteError(w, r, httpapi.NewError(problem.Status, problem.Code, problem.Message, nil))
}

func (h *Handler) recordReplay(operation string, replayed bool) {
	if replayed {
		h.metrics.RecordOperation(operation, "replayed")
		return
	}
	h.metrics.RecordOperation(operation, "success")
}
