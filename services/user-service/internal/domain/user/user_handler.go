package user

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/services/user-service/internal/platform/observability"
)

type UserHandlerConfig struct {
	AllowedOrigins map[string]struct{}
}

type UserHandler struct {
	service        *UserService
	metrics        *observability.Metrics
	allowedOrigins map[string]struct{}
}

func NewUserHandler(service *UserService, metrics *observability.Metrics, cfg UserHandlerConfig) (*UserHandler, error) {
	if service == nil || metrics == nil {
		return nil, oops.In("user_handler").Code("user.handler_dependencies_required").
			New("user service and metrics are required")
	}
	return &UserHandler{service: service, metrics: metrics, allowedOrigins: cfg.AllowedOrigins}, nil
}

func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
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
		h.metrics.RecordOperation(operationCreateUser, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	agreements := make([]AgreementAcceptance, 0, len(request.RequiredAgreements))
	for _, agreement := range request.RequiredAgreements {
		agreements = append(agreements, AgreementAcceptance{Code: agreement.AgreementCode, Version: agreement.AgreementVersion, AcceptedAt: agreement.AcceptedAt})
	}
	result, err := h.service.CreateUser(r.Context(), CreateUserInput{
		RegistrationID:              request.RegistrationID,
		RegistrationCompletionProof: request.RegistrationCompletionProof,
		PrivateName:                 request.Profile.PrivateName,
		Nickname:                    request.Profile.Nickname,
		Introduction:                request.Profile.Introduction,
		RequiredAgreements:          agreements,
		IdempotencyKey:              r.Header.Get(headers.IdempotencyKey),
	})
	if err != nil {
		h.metrics.RecordOperation(operationCreateUser, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	status := http.StatusCreated
	metricResult := "created"
	if result.Replayed {
		status = http.StatusOK
		metricResult = "replayed"
	}
	h.metrics.RecordOperation(operationCreateUser, metricResult)
	httpapi.WriteJSON(w, status, map[string]any{"data": map[string]any{
		"userId":            result.UserID,
		"userVersion":       result.UserVersion,
		"createdAt":         result.CreatedAt,
		"userCreationProof": result.UserCreationProof,
	}})
}

func (h *UserHandler) GetOwnProfile(w http.ResponseWriter, r *http.Request) {
	id, err := principalUserID(r.Context())
	if err != nil {
		h.metrics.RecordOperation("get_own_profile", "error")
		httpapi.WriteError(w, r, err)
		return
	}
	current, err := h.service.GetOwnProfile(r.Context(), id)
	if err != nil {
		h.metrics.RecordOperation("get_own_profile", "error")
		httpapi.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.metrics.RecordOperation("get_own_profile", "success")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": profileResponse(current)})
}

func (h *UserHandler) UpdateOwnProfile(w http.ResponseWriter, r *http.Request) {
	id, err := principalUserID(r.Context())
	if err != nil {
		h.metrics.RecordOperation(operationProfile, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	var request struct {
		ExpectedUserVersion int64          `json:"expectedUserVersion"`
		Nickname            optionalString `json:"nickname"`
		Introduction        optionalString `json:"introduction"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.metrics.RecordOperation(operationProfile, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	patch := ProfilePatch{NicknameSet: request.Nickname.Set, IntroductionSet: request.Introduction.Set}
	if request.Nickname.Value != nil {
		patch.Nickname = *request.Nickname.Value
	}
	patch.Introduction = request.Introduction.Value
	result, err := h.service.UpdateOwnProfile(r.Context(), UpdateProfileInput{
		UserID: id, ExpectedUserVersion: request.ExpectedUserVersion, Patch: patch, IdempotencyKey: r.Header.Get(headers.IdempotencyKey),
	})
	if err != nil {
		h.metrics.RecordOperation(operationProfile, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	h.recordReplay(operationProfile, result.Replayed)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"userId":        result.UserID,
		"userVersion":   result.UserVersion,
		"changedFields": result.ChangedFields,
		"updatedAt":     result.UpdatedAt,
	}})
}

func (h *UserHandler) UpdateOwnProfileImage(w http.ResponseWriter, r *http.Request) {
	id, err := principalUserID(r.Context())
	if err != nil {
		h.metrics.RecordOperation(operationProfileImage, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	var request struct {
		MediaAssetID        string `json:"mediaAssetId"`
		MediaAssetProof     string `json:"mediaAssetProof"`
		ExpectedUserVersion int64  `json:"expectedUserVersion"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.metrics.RecordOperation(operationProfileImage, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.service.UpdateOwnProfileImage(r.Context(), UpdateProfileImageInput{
		UserID: id, MediaAssetID: request.MediaAssetID, MediaAssetProof: request.MediaAssetProof,
		ExpectedUserVersion: request.ExpectedUserVersion, IdempotencyKey: r.Header.Get(headers.IdempotencyKey),
	})
	if err != nil {
		h.metrics.RecordOperation(operationProfileImage, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	h.recordReplay(operationProfileImage, result.Replayed)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"userId":              result.UserID,
		"profileMediaAssetId": result.ProfileMediaAssetID,
		"userVersion":         result.UserVersion,
		"updatedAt":           result.UpdatedAt,
	}})
}

func (h *UserHandler) ChangeUserAccountStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		h.metrics.RecordOperation(operationStatus, "error")
		httpapi.WriteError(w, r, httpapi.Unprocessable("USER_INPUT_INVALID").
			Public("userId 형식이 올바르지 않습니다.").
			Wrap(err))
		return
	}
	var request struct {
		TargetStatus        string `json:"targetStatus"`
		ReasonCode          string `json:"reasonCode"`
		ExpectedUserVersion int64  `json:"expectedUserVersion"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.metrics.RecordOperation(operationStatus, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	actor := requestPrincipal(r.Context())
	result, err := h.service.ChangeUserAccountStatus(r.Context(), ChangeStatusInput{
		UserID: id, TargetStatus: request.TargetStatus, ReasonCode: request.ReasonCode,
		ExpectedUserVersion: request.ExpectedUserVersion, ChangedBy: actor.UserID, IdempotencyKey: r.Header.Get(headers.IdempotencyKey),
	})
	if err != nil {
		h.metrics.RecordOperation(operationStatus, "error")
		httpapi.WriteError(w, r, err)
		return
	}
	h.recordReplay(operationStatus, result.Replayed)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"statusChangeId":        result.StatusChangeID,
		"userId":                result.UserID,
		"accountStatus":         result.AccountStatus,
		"userVersion":           result.UserVersion,
		"changedAt":             result.ChangedAt,
		"userStatusChangeProof": result.UserStatusChangeProof,
	}})
}

func (h *UserHandler) GetUserAccountStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		h.metrics.RecordOperation("get_user_account_status", "error")
		httpapi.WriteError(w, r, httpapi.Unprocessable("USER_INPUT_INVALID").
			Public("userId 형식이 올바르지 않습니다.").
			Wrap(err))
		return
	}
	current, err := h.service.GetAccountStatus(r.Context(), id)
	if err != nil {
		h.metrics.RecordOperation("get_user_account_status", "error")
		httpapi.WriteError(w, r, err)
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

func profileResponse(current User) map[string]any {
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
		return uuid.Nil, httpapi.Unauthorized("USER_AUTHENTICATION_REQUIRED").
			Public("사용자 인증이 필요합니다.").
			Wrap(err)
	}
	return id, nil
}

func decodeJSON(r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return httpapi.Unprocessable("USER_INPUT_INVALID").
			Public("요청 JSON을 해석할 수 없습니다.").
			Wrap(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return httpapi.Unprocessable("USER_INPUT_INVALID").
			Public("요청 JSON 뒤에 불필요한 값이 있습니다.").
			New("request JSON contains a trailing value")
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

func (h *UserHandler) recordReplay(operation string, replayed bool) {
	if replayed {
		h.metrics.RecordOperation(operation, "replayed")
		return
	}
	h.metrics.RecordOperation(operation, "success")
}
