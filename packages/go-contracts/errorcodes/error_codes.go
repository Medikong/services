package errorcodes

const (
	AuthMissingToken               = "auth.missing_token"
	AuthInvalidAuthorizationHeader = "auth.invalid_authorization_header"
	AuthInvalidToken               = "auth.invalid_token"
	AuthForbidden                  = "auth.forbidden"
	CommonInternal                 = "common.internal"
	CommonInvalidRequest           = "common.invalid_request"
)
