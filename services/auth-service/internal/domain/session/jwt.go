package session

import (
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/packages/go-authz/rbac"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type TokenConfig struct {
	Issuer          string
	Secret          string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

type TokenManager struct {
	issuer          string
	secret          []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

type AccessTokenInput struct {
	Subject   string
	Role      string
	JTI       string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type AccessTokenClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

func NewTokenManager(cfg TokenConfig) (TokenManager, error) {
	issuer := strings.TrimSpace(cfg.Issuer)
	if issuer == "" {
		issuer = "auth-service"
	}
	secret := strings.TrimSpace(cfg.Secret)
	if secret == "" {
		return TokenManager{}, ErrInvalidTokenConfig.With("field", "JWT_SECRET").New("JWT_SECRET is required")
	}
	if cfg.AccessTokenTTL <= 0 {
		return TokenManager{}, ErrInvalidTokenConfig.With("field", "AUTH_TOKEN_TTL_SECONDS").New("AUTH_TOKEN_TTL_SECONDS must be greater than 0")
	}
	if cfg.RefreshTokenTTL <= 0 {
		return TokenManager{}, ErrInvalidTokenConfig.With("field", "AUTH_REFRESH_TOKEN_TTL_SECONDS").New("AUTH_REFRESH_TOKEN_TTL_SECONDS must be greater than 0")
	}
	return TokenManager{
		issuer:          issuer,
		secret:          []byte(secret),
		accessTokenTTL:  cfg.AccessTokenTTL,
		refreshTokenTTL: cfg.RefreshTokenTTL,
	}, nil
}

func (m TokenManager) AccessTokenTTL() time.Duration {
	return m.accessTokenTTL
}

func (m TokenManager) RefreshTokenTTL() time.Duration {
	return m.refreshTokenTTL
}

func (m TokenManager) NewJTI() string {
	return uuid.NewString()
}

func (m TokenManager) Issue(input AccessTokenInput) (string, error) {
	if strings.TrimSpace(input.Subject) == "" || strings.TrimSpace(input.JTI) == "" {
		return "", ErrInvalidToken.New("missing required access token claim")
	}
	role, ok := rbac.Canonical(input.Role)
	if !ok {
		return "", ErrInvalidRole.With("role", input.Role).New("invalid JWT role")
	}
	claims := AccessTokenClaims{
		Role: string(role),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   strings.TrimSpace(input.Subject),
			ID:        strings.TrimSpace(input.JTI),
			IssuedAt:  jwt.NewNumericDate(input.IssuedAt),
			ExpiresAt: jwt.NewNumericDate(input.ExpiresAt),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

func (m TokenManager) Verify(token string) (AccessTokenClaims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return AccessTokenClaims{}, ErrInvalidToken.New("empty access token")
	}
	claims := AccessTokenClaims{}
	parsed, err := jwt.ParseWithClaims(token, &claims, func(parsed *jwt.Token) (any, error) {
		if parsed.Method != jwt.SigningMethodHS256 {
			return nil, ErrInvalidToken.New("unexpected JWT signing method")
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return AccessTokenClaims{}, ErrTokenExpired.Wrap(err)
		}
		return AccessTokenClaims{}, ErrInvalidToken.Wrap(err)
	}
	if parsed == nil || !parsed.Valid {
		return AccessTokenClaims{}, ErrInvalidToken.New("invalid access token")
	}
	if strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.ID) == "" {
		return AccessTokenClaims{}, ErrInvalidToken.New("missing required access token claim")
	}
	if _, ok := rbac.Canonical(claims.Role); !ok {
		return AccessTokenClaims{}, ErrInvalidToken.New("invalid access token role")
	}
	return claims, nil
}

func NewRefreshToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto random failed: " + err.Error())
	}
	return "rtk_" + hexToken(buf)
}

func hexToken(buf []byte) string {
	const alphabet = "0123456789abcdef"
	out := make([]byte, len(buf)*2)
	for i, b := range buf {
		out[i*2] = alphabet[b>>4]
		out[i*2+1] = alphabet[b&0x0f]
	}
	return string(out)
}
