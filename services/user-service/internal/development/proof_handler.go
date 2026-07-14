package development

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/services/user-service/internal/platform/observability"
	"github.com/Medikong/services/services/user-service/internal/security"
)

const (
	developmentTokenHeader = "X-Development-Token"
	registrationProofPath  = "/internal/dev/proofs/registration"
	mediaProofPath         = "/internal/dev/proofs/media"
)

type ProofRouteContract struct {
	Method string
	Path   string
}

var ProofRoutes = []ProofRouteContract{
	{Method: http.MethodPost, Path: registrationProofPath},
	{Method: http.MethodPost, Path: mediaProofPath},
}

type ProofHandlerConfig struct {
	AccessToken string
	AuthSigner  security.Signer
	MediaSigner security.Signer
	ProofTTL    time.Duration
}

type ProofHandler struct {
	config  ProofHandlerConfig
	metrics *observability.Metrics
}

func NewProofHandler(cfg ProofHandlerConfig, metrics *observability.Metrics) (*ProofHandler, error) {
	if cfg.AccessToken == "" || cfg.ProofTTL <= 0 || metrics == nil {
		return nil, oops.In("development_proof_handler").Code("development.config_invalid").
			New("development proof routes require a token, positive proof TTL, and metrics")
	}
	return &ProofHandler{config: cfg, metrics: metrics}, nil
}

func RegisterRoutes(router chi.Router, handler *ProofHandler) {
	router.With(handler.requireAccess).Post(registrationProofPath, handler.IssueRegistrationProof)
	router.With(handler.requireAccess).Post(mediaProofPath, handler.IssueMediaProof)
}

func (h *ProofHandler) IssueRegistrationProof(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RegistrationID string `json:"registrationId"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.metrics.RecordOperation("development_registration_proof", "error")
		httpapi.WriteError(w, r, err)
		return
	}
	proof, err := h.config.AuthSigner.Sign(security.ProofClaims{
		Audience: "user-service", Purpose: "create_user", RegistrationID: strings.TrimSpace(request.RegistrationID),
		EmailVerified: true, PhoneVerified: true, Nonce: uuid.NewString(),
	}, h.config.ProofTTL)
	if err != nil {
		h.metrics.RecordOperation("development_registration_proof", "error")
		httpapi.WriteError(w, r, err)
		return
	}
	h.metrics.RecordOperation("development_registration_proof", "success")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"registrationCompletionProof": proof}})
}

func (h *ProofHandler) IssueMediaProof(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID       string `json:"userId"`
		MediaAssetID string `json:"mediaAssetId"`
	}
	if err := decodeJSON(r, &request); err != nil {
		h.metrics.RecordOperation("development_media_proof", "error")
		httpapi.WriteError(w, r, err)
		return
	}
	if _, err := uuid.Parse(request.UserID); err != nil {
		h.metrics.RecordOperation("development_media_proof", "error")
		httpapi.WriteError(w, r, httpapi.Unprocessable("USER_INPUT_INVALID").
			Public("userId 형식이 올바르지 않습니다.").
			Wrap(err))
		return
	}
	proof, err := h.config.MediaSigner.Sign(security.ProofClaims{
		Audience: "user-service", Purpose: "user_profile", UserID: request.UserID,
		MediaAssetID: strings.TrimSpace(request.MediaAssetID), ScanCompleted: true, Nonce: uuid.NewString(),
	}, h.config.ProofTTL)
	if err != nil {
		h.metrics.RecordOperation("development_media_proof", "error")
		httpapi.WriteError(w, r, err)
		return
	}
	h.metrics.RecordOperation("development_media_proof", "success")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"mediaAssetProof": proof}})
}

func (h *ProofHandler) requireAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get(developmentTokenHeader))
		want := []byte(h.config.AccessToken)
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			httpapi.WriteError(w, r, httpapi.Forbidden("USER_DEVELOPMENT_ACCESS_DENIED").
				Public("개발 전용 API 접근이 거부되었습니다.").
				New("development API access denied"))
			return
		}
		next.ServeHTTP(w, r)
	})
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
