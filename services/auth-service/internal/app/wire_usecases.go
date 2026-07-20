package app

import (
	applicationauthentication "github.com/Medikong/services/services/auth-service/internal/application/authentication"
	applicationdevelopment "github.com/Medikong/services/services/auth-service/internal/application/development"
	applicationidentity "github.com/Medikong/services/services/auth-service/internal/application/identity"
	applicationintent "github.com/Medikong/services/services/auth-service/internal/application/intent"
	applicationoperator "github.com/Medikong/services/services/auth-service/internal/application/operator"
	applicationpasswordreset "github.com/Medikong/services/services/auth-service/internal/application/passwordreset"
	applicationreauth "github.com/Medikong/services/services/auth-service/internal/application/reauth"
	applicationregistration "github.com/Medikong/services/services/auth-service/internal/application/registration"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	applicationuserauthstate "github.com/Medikong/services/services/auth-service/internal/application/userauthstate"
	"github.com/Medikong/services/services/auth-service/internal/infrastructure/cryptography"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

type ServerOptions struct {
	ApprovalPort              applicationoperator.ApprovalPort
	AuthorizationDecisionPort applicationuserauthstate.AuthorizationDecisionPort
}

type serverUseCases struct {
	keys cryptography.Keys

	bootstrap        *applicationintent.BootstrapService
	actionResume     *applicationintent.ActionResumeService
	sessions         *applicationsession.Service
	sessionStatus    *applicationsession.StatusService
	emailSignIn      *applicationauthentication.EmailService
	phoneSignIn      *applicationauthentication.PhoneService
	registration     *applicationregistration.Service
	passwordReset    *applicationpasswordreset.Service
	reauthentication *applicationreauth.Service
	identity         *applicationidentity.Service
	operator         *applicationoperator.Service
	userAuthState    *applicationuserauthstate.Service
	development      *applicationdevelopment.Service
}

func wireUseCases(cfg config.ServerConfig, options ServerOptions, adapters serverAdapters) (serverUseCases, error) {
	userProofVerifier, err := cryptography.NewUserProofVerifier(
		cfg.Auth.UserProofIssuer,
		cfg.Auth.UserProofKeyID,
		cfg.Auth.UserProofPublicKey,
		cfg.Auth.ProofClockSkew,
		nil,
	)
	if err != nil {
		return serverUseCases{}, err
	}
	authProofSigner, err := cryptography.NewUserProofSigner(
		config.ServiceName,
		cfg.Auth.AuthProofKeyID,
		cfg.Auth.AuthProofPrivateKey,
		nil,
	)
	if err != nil {
		return serverUseCases{}, err
	}

	sessionCryptography := cryptography.NewSession(adapters.keys)
	sessionProjectionWriters := make([]applicationsession.StatusProjectionWriter, 0, 1)
	userStateProjectionWriters := make([]applicationuserauthstate.StatusProjectionWriter, 0, 1)
	if adapters.sessionProjection != nil {
		sessionProjectionWriters = append(sessionProjectionWriters, adapters.sessionProjection)
		userStateProjectionWriters = append(userStateProjectionWriters, adapters.sessionProjection)
	}

	bootstrap := applicationintent.NewBootstrapService(
		adapters.intentTransactions,
		adapters.keys,
		adapters.clock,
		applicationintent.BootstrapConfig{IntentTTL: cfg.Auth.IntentTTL},
	)
	sessions := applicationsession.NewService(
		adapters.sessionTransactions,
		sessionCryptography,
		adapters.clock,
		applicationsession.Config{
			AccessTTL: cfg.Auth.AccessTTL, RefreshTTL: cfg.Auth.RefreshTTL,
			SessionTTL: cfg.Auth.SessionTTL, RememberMeSessionTTL: cfg.Auth.RememberMeSessionTTL,
			RecoveryTTL: cfg.Auth.RecoveryTTL,
		},
		adapters.sessions,
		sessionProjectionWriters...,
	)

	authenticationCryptography := cryptography.NewAuthentication(adapters.keys)
	emailSignIn := applicationauthentication.NewEmailService(
		adapters.authenticationTransactions, bootstrap, authenticationCryptography, sessions,
	)
	phoneSignIn := applicationauthentication.NewPhoneService(
		adapters.authenticationTransactions, bootstrap, authenticationCryptography, adapters.clock, sessions,
		applicationauthentication.Config{
			VirtualAdapterEnabled: cfg.Development.VirtualAdaptersEnabled,
			ChallengeTTL:          cfg.Auth.ChallengeTTL,
		},
	)

	passwordReset := applicationpasswordreset.NewService(
		adapters.passwordResetTransactions,
		cryptography.NewPasswordReset(adapters.keys),
		bootstrap,
		adapters.clock,
		applicationpasswordreset.Config{
			ResetTTL: cfg.Auth.ProofTTL, ChallengeTTL: cfg.Auth.ChallengeTTL,
			PasswordMinLength:     cfg.Auth.PasswordMinLength,
			VirtualAdapterEnabled: cfg.Development.VirtualAdaptersEnabled,
		},
	)
	reauthentication := applicationreauth.NewService(
		adapters.reauthenticationTx,
		cryptography.NewReauthentication(adapters.keys),
		adapters.clock,
		sessions,
		applicationreauth.Config{ProofTTL: cfg.Auth.ProofTTL, RecoveryTTL: cfg.Auth.RecoveryTTL},
	)
	identity := applicationidentity.NewService(
		adapters.identityTransactions,
		cryptography.NewIdentity(adapters.keys),
		adapters.clock,
		reauthentication,
		sessions,
		applicationidentity.Config{
			Virtual: cfg.Development.VirtualAdaptersEnabled,
			LinkTTL: cfg.Auth.ChallengeTTL, RecoveryTTL: cfg.Auth.RecoveryTTL,
		},
	)
	registration := applicationregistration.NewService(
		adapters.registrationTransactions,
		adapters.keys,
		cryptography.RegistrationPasswordHasher{},
		adapters.clock,
		applicationregistration.Config{
			RegistrationTTL: cfg.Auth.RegistrationTTL, StatusTokenRetention: cfg.Auth.ProofTTL,
			ChallengeTTL: cfg.Auth.ChallengeTTL, PasswordMinLength: cfg.Auth.PasswordMinLength,
			VirtualAdapterEnabled: cfg.Development.VirtualAdaptersEnabled,
		},
		bootstrap,
		sessions,
		authProofSigner,
		cryptography.NewRegistrationProofVerifier(userProofVerifier),
	)
	operator := applicationoperator.NewService(
		adapters.operatorTransactions,
		cryptography.NewOperatorCryptography(adapters.keys),
		applicationoperator.Config{StrongAuthTTL: cfg.Auth.ProofTTL},
		options.ApprovalPort,
		options.AuthorizationDecisionPort,
		adapters.clock,
	)
	userAuthState := applicationuserauthstate.NewService(
		adapters.userAuthStateTransactions,
		cryptography.NewUserAuthStateProofVerifier(userProofVerifier),
		options.AuthorizationDecisionPort,
		applicationuserauthstate.Config{StrongAuthTTL: cfg.Auth.ProofTTL},
		adapters.clock,
		userStateProjectionWriters...,
	)
	if adapters.sessionProjection != nil {
		sessions.UseSessionRevocation(adapters.sessionProjection)
		passwordReset.UseSessionRevocation(adapters.sessionProjection)
		identity.UseSessionRevocation(adapters.sessionProjection)
		operator.UseSessionRevocation(adapters.sessionProjection)
		userAuthState.UseSessionRevocation(adapters.sessionProjection)
	}
	actionResume := applicationintent.NewActionResumeService(adapters.intentTransactions, adapters.keys, adapters.clock)

	var sessionStatus *applicationsession.StatusService
	if adapters.sessionProjection != nil {
		sessionStatus = applicationsession.NewStatusService(sessionCryptography, adapters.sessionProjection)
	}
	var development *applicationdevelopment.Service
	if cfg.Development.RouteEnabled {
		development = applicationdevelopment.NewService(
			adapters.developmentTransactions,
			cryptography.NewDevelopment(adapters.keys),
			bootstrap,
			adapters.clock,
		)
	}

	return serverUseCases{
		keys:      adapters.keys,
		bootstrap: bootstrap, actionResume: actionResume, sessions: sessions, sessionStatus: sessionStatus,
		emailSignIn: emailSignIn, phoneSignIn: phoneSignIn, registration: registration,
		passwordReset: passwordReset, reauthentication: reauthentication, identity: identity,
		operator: operator, userAuthState: userAuthState, development: development,
	}, nil
}
