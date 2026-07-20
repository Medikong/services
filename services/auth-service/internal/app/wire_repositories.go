package app

import (
	"crypto/rsa"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"

	clockinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/clock"
	"github.com/Medikong/services/services/auth-service/internal/infrastructure/cryptography"
	postgresinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/postgres"
	redisinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/redis"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

type serverAdapters struct {
	keys  cryptography.Keys
	clock clockinfra.System

	intentTransactions         *postgresinfra.IntentTransactor
	sessionTransactions        *postgresinfra.SessionTransactor
	authenticationTransactions *postgresinfra.AuthenticationTransactor
	registrationTransactions   *postgresinfra.RegistrationTransactor
	passwordResetTransactions  *postgresinfra.PasswordResetTransactor
	reauthenticationTx         *postgresinfra.ReauthenticationTransactor
	identityTransactions       *postgresinfra.IdentityTransactor
	operatorTransactions       *postgresinfra.OperatorTransactor
	userAuthStateTransactions  *postgresinfra.UserAuthStateTransactor
	developmentTransactions    *postgresinfra.DevelopmentTransactor

	sessions          *postgresinfra.SessionRepository
	sessionProjection *redisinfra.SessionProjection
}

func wireRepositories(cfg config.ServerConfig, db *pgxpool.Pool, redisClient *goredis.Client) (serverAdapters, error) {
	retiringKeys := make(map[string]*rsa.PublicKey, len(cfg.Auth.JWTRetiringPublicKeys))
	for keyID, encoded := range cfg.Auth.JWTRetiringPublicKeys {
		publicKey, err := cryptography.ParseRSAPublicKeyPEM([]byte(encoded))
		if err != nil {
			return serverAdapters{}, oops.In("auth_server").Code("server.jwt_retiring_key_invalid").
				With("key_id", keyID).Wrap(err)
		}
		retiringKeys[keyID] = publicKey
	}
	keys := cryptography.Keys{
		CredentialHMAC: []byte(cfg.Auth.CredentialHMACKey),
		ReplayKey:      []byte(cfg.Auth.ReplayEncryptionKey),
		JWTKey:         []byte(cfg.Auth.JWTPrivateKeyPEM),
		JWTKeyID:       cfg.Auth.JWTKeyID,
		JWTIssuer:      cfg.Auth.JWTIssuer,
		JWTAudiences:   cfg.Auth.JWTAudiences,
		JWTVerifyKeys:  retiringKeys,
		VirtualKey:     []byte(cfg.Development.VirtualMessageKey),
	}
	if err := keys.Validate(cfg.Development.VirtualAdaptersEnabled); err != nil {
		return serverAdapters{}, err
	}

	sessions := postgresinfra.NewSessionRepository(db)
	var sessionProjection *redisinfra.SessionProjection
	if redisClient != nil {
		var err error
		sessionProjection, err = redisinfra.NewSessionProjection(
			sessions,
			redisClient,
			cfg.SessionStatus.Timeout,
			cfg.SessionStatus.DBFallbackTimeout,
			cfg.SessionStatus.CacheTTL,
			cfg.SessionStatus.TombstoneTTL,
			cfg.SessionStatus.MaxDBLookups,
		)
		if err != nil {
			return serverAdapters{}, err
		}
	}

	virtual := cfg.Development.VirtualAdaptersEnabled
	return serverAdapters{
		keys:                       keys,
		clock:                      clockinfra.System{},
		intentTransactions:         postgresinfra.NewIntentTransactor(db),
		sessionTransactions:        postgresinfra.NewSessionTransactor(db),
		authenticationTransactions: postgresinfra.NewAuthenticationTransactor(db, virtual),
		registrationTransactions:   postgresinfra.NewRegistrationTransactor(db, virtual),
		passwordResetTransactions:  postgresinfra.NewPasswordResetTransactor(db, virtual),
		reauthenticationTx:         postgresinfra.NewReauthenticationTransactor(db),
		identityTransactions:       postgresinfra.NewIdentityTransactor(db, virtual),
		operatorTransactions:       postgresinfra.NewOperatorTransactor(db),
		userAuthStateTransactions:  postgresinfra.NewUserAuthStateTransactor(db),
		developmentTransactions:    postgresinfra.NewDevelopmentTransactor(db),
		sessions:                   sessions,
		sessionProjection:          sessionProjection,
	}, nil
}
