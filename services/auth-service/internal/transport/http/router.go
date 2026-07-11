package http

import (
	"errors"
	stdhttp "net/http"

	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/auth-service/internal/application/actionresume"
	"github.com/Medikong/services/services/auth-service/internal/application/bootstrap"
	"github.com/Medikong/services/services/auth-service/internal/application/development"
	appidentity "github.com/Medikong/services/services/auth-service/internal/application/identitymanagement"
	appoperator "github.com/Medikong/services/services/auth-service/internal/application/operator"
	appreset "github.com/Medikong/services/services/auth-service/internal/application/passwordreset"
	appregistration "github.com/Medikong/services/services/auth-service/internal/application/registration"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/application/signin"
	"github.com/Medikong/services/services/auth-service/internal/domain/access"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/inbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	operatordomain "github.com/Medikong/services/services/auth-service/internal/domain/operator"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	resetdomain "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	"github.com/Medikong/services/services/auth-service/internal/domain/policy"
	"github.com/Medikong/services/services/auth-service/internal/domain/reauth"
	registrationdomain "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	sessiondomain "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/Medikong/services/services/auth-service/internal/platform/observability"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/Medikong/services/services/auth-service/internal/transport/http/controller"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter is the transport composition root. It creates application
// services from domain repositories; controllers never receive pgx pools.
func NewRouter(cfg config.ServerConfig, db *pgxpool.Pool, _ *operational.Handler, _ *observability.Metrics) (stdhttp.Handler, error) {
	if db == nil {
		return nil, errors.New("auth HTTP router requires a postgres pool")
	}
	keys := security.Keys{
		CredentialHMAC: []byte(cfg.Auth.CredentialHMACKey),
		ReplayKey:      []byte(cfg.Auth.ReplayEncryptionKey),
		JWTKey:         []byte(cfg.Auth.JWTSecret),
		JWTIssuer:      cfg.Auth.JWTIssuer,
		VirtualKey:     []byte(cfg.Development.VirtualMessageKey),
	}
	if err := keys.Validate(cfg.Development.VirtualAdaptersEnabled); err != nil {
		return nil, err
	}
	contract := httpcontract.NewContract(cfg.Auth, cfg.Development)
	intentRepository := intent.NewPostgresRepository(db)
	idempotencyRepository := idempotency.NewPostgresRepository(db)
	inboxRepository := inbox.NewPostgresRepository(db)
	bootstrapService := bootstrap.NewService(db, keys, bootstrap.Config{IntentTTL: cfg.Auth.IntentTTL}, intentRepository, idempotencyRepository)
	identityRepository := identity.NewPostgresRepository(db)
	accessRepository := access.NewPostgresRepository(db)
	registrationRepository := registrationdomain.NewPostgresRepository(db)
	challengeRepository := challenge.NewPostgresRepository(db, challenge.PostgresOptions{VirtualProjectionEnabled: cfg.Development.VirtualAdaptersEnabled})
	outboxRepository := outbox.NewPostgresRepository(db)
	sessionRepository := sessiondomain.NewPostgresRepository(db)
	sessionService := appsession.NewService(db, keys, appsession.Config{
		AccessTTL: cfg.Auth.AccessTTL, RefreshTTL: cfg.Auth.RefreshTTL, SessionTTL: cfg.Auth.SessionTTL, RecoveryTTL: cfg.Auth.RecoveryTTL,
	}, sessionRepository, accessRepository, idempotencyRepository, outboxRepository)
	emailSignInService := signin.NewEmailService(db, bootstrapService, identityRepository, intentRepository, sessionService)
	phoneSignInService := signin.NewPhoneService(db, keys, bootstrapService, intentRepository, identityRepository, challengeRepository, outboxRepository, sessionService, cfg.Development.VirtualAdaptersEnabled, cfg.Auth.ChallengeTTL)
	passwordResetRepository := resetdomain.NewPostgresRepository(db)
	passwordResetService := appreset.NewService(db, keys, appreset.Config{
		ResetTTL: cfg.Auth.ProofTTL, ChallengeTTL: cfg.Auth.ChallengeTTL, VirtualAdapterEnabled: cfg.Development.VirtualAdaptersEnabled,
	}, bootstrapService, passwordResetRepository, identityRepository, challengeRepository, idempotencyRepository, sessionRepository, outboxRepository)
	reauthService := appidentity.NewReauthService(db, keys, identityRepository, reauth.NewPostgresRepository(db), sessionRepository, idempotencyRepository, sessionService, cfg.Auth.ProofTTL, cfg.Auth.RecoveryTTL)
	identityLinkService := appidentity.NewLinkService(db, keys, reauthService, identityRepository, challengeRepository, sessionRepository, sessionService, idempotencyRepository, outboxRepository, cfg.Development.VirtualAdaptersEnabled, cfg.Auth.ChallengeTTL, cfg.Auth.RecoveryTTL)
	operatorService := appoperator.NewService(db, keys, operatordomain.NewPostgresRepository(db), policy.NewPostgresRepository(db), accessRepository, idempotencyRepository, outboxRepository, appoperator.Config{StrongAuthTTL: cfg.Auth.ProofTTL}, appoperator.DenyApprovalPort{})
	registrationService := appregistration.NewService(
		db, keys,
		appregistration.Config{
			RegistrationTTL:       cfg.Auth.RegistrationTTL,
			StatusTokenRetention:  cfg.Auth.ProofTTL,
			ChallengeTTL:          cfg.Auth.ChallengeTTL,
			VirtualAdapterEnabled: cfg.Development.VirtualAdaptersEnabled,
		},
		bootstrapService,
		registrationRepository, challengeRepository, identityRepository, idempotencyRepository, inboxRepository, outboxRepository, accessRepository, intentRepository, sessionService,
	)
	actionResumeService := actionresume.NewService(db, keys, intentRepository, idempotencyRepository)
	bootstrapController := controller.NewBootstrap(contract, bootstrapService)
	signInController := controller.NewSignIn(&contract, emailSignInService, phoneSignInService)
	sessionController := controller.NewSession(contract, sessionService)
	registrationController := controller.NewRegistration(contract, registrationService)
	passwordResetController := controller.NewPasswordReset(contract, passwordResetService)
	identityController := controller.NewIdentityManagement(contract, sessionService, reauthService, identityLinkService)
	operatorController := controller.NewOperator(contract, sessionService, operatorService)
	actionResumeController := controller.NewActionResume(contract, sessionService, actionResumeService)

	router := chi.NewRouter()
	router.Use(httpcontract.RequestIDMiddleware)
	router.Post("/api/v1/auth/intents", bootstrapController.CreateIntent)
	router.Get("/api/v1/auth/methods", bootstrapController.GetMethods)
	router.Post("/api/v1/auth/registrations", registrationController.Start)
	router.Post("/api/v1/auth/registrations/{registrationId}/challenges", registrationController.IssueChallenge)
	router.Post("/api/v1/auth/registrations/{registrationId}/challenges/{challengeId}/verify", registrationController.VerifyChallenge)
	router.Post("/api/v1/auth/registrations/{registrationId}/complete", registrationController.Complete)
	router.Post("/api/v1/auth/signins/email", signInController.Email)
	router.Post("/api/v1/auth/signins/phone/challenges", signInController.PhoneIssue)
	router.Post("/api/v1/auth/signins/phone/challenges/{challengeId}/verify", signInController.PhoneVerify)
	router.Post("/api/v1/auth/password-resets", passwordResetController.Start)
	router.Post("/api/v1/auth/password-resets/{passwordResetId}/challenges", passwordResetController.Issue)
	router.Post("/api/v1/auth/password-resets/{passwordResetId}/challenges/{challengeId}/verify", passwordResetController.Verify)
	router.Put("/api/v1/auth/password-resets/{passwordResetId}/password", passwordResetController.Complete)
	router.Post("/api/v1/auth/reauthentications/email", identityController.Reauthenticate)
	router.Post("/api/v1/auth/method-links", identityController.StartLink)
	router.Post("/api/v1/auth/method-links/{linkIntentId}/challenges", identityController.IssueLink)
	router.Post("/api/v1/auth/method-links/{linkIntentId}/complete", identityController.CompleteLink)
	router.Post("/api/v1/auth/phone-replacements", identityController.StartReplacement)
	router.Post("/api/v1/auth/phone-replacements/{replacementId}/challenges", identityController.IssueReplacement)
	router.Post("/api/v1/auth/phone-replacements/{replacementId}/complete", identityController.CompleteReplacement)
	router.Post("/api/v1/auth/sessions/refresh", sessionController.Refresh)
	router.Post("/api/v1/auth/sessions/logout", sessionController.Logout)
	router.Get("/api/v1/auth/context", sessionController.Context)
	router.Get("/api/v1/auth/registrations/{registrationId}", registrationController.Status)
	router.Post("/api/v1/auth/intents/{intentId}/action-resume", actionResumeController.Resume)
	router.Get("/api/v1/operator/auth/users/{userId}", operatorController.User)
	router.Get("/api/v1/operator/auth/policies", operatorController.Policies)
	router.Patch("/api/v1/operator/auth/policies/{policyName}", operatorController.UpdatePolicy)
	router.Post("/api/v1/operator/auth/manual-actions", operatorController.Manual)

	// Controllers are registered by their feature module as the corresponding
	// application service is composed here. Development paths are never mounted
	// unless the validated non-production route gate is enabled.
	if cfg.Development.RouteEnabled {
		virtualMessageService := development.NewVirtualMessageService(db, keys, bootstrapService, challengeRepository, registrationRepository, passwordResetRepository, identityRepository)
		developmentController := controller.NewDevelopment(contract, virtualMessageService, sessionService)
		router.Get("/api/v1/dev/auth/verification-messages/{challengeId}", developmentController.VirtualMessage)
	}
	return router, nil
}
