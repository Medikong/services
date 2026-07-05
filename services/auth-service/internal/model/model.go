package model

import "github.com/Medikong/services/packages/go-authz/principal"

type AuthResult struct {
	AuthAccountID   string              `json:"authAccountId"`
	UserID          string              `json:"userId"`
	AccessToken     string              `json:"accessToken"`
	RefreshToken    string              `json:"refreshToken"`
	Principal       principal.Principal `json:"principal"`
	PrincipalHeader string              `json:"principalHeader"`
}

type AccountCredential struct {
	AuthAccountID string
	UserID        string
	Email         string
	PasswordHash  string
	Roles         []string
}
