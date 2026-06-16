from enum import StrEnum


class AuthAction(StrEnum):
    LOGIN = "login"
    REFRESH = "refresh"
    LOGOUT = "logout"
    ME = "me"


class AuthErrorCode(StrEnum):
    NONE = "none"
    INVALID_CREDENTIALS = "auth.invalid_credentials"
    INACTIVE_ACCOUNT = "auth.inactive_account"
    INVALID_TOKEN = "auth.invalid_token"
    FORBIDDEN = "auth.forbidden"
    USER_NOT_FOUND = "auth.user_not_found"
    INTERNAL_ERROR = "auth.internal_error"


class AuthTokenType(StrEnum):
    ACCESS = "access"
    REFRESH = "refresh"


class AuthRevocationReason(StrEnum):
    LOGOUT = "logout"
    REFRESH_ROTATION = "refresh_rotation"


class AuditEventType(StrEnum):
    SIGNUP_FAILED = "signup_failed"
    SIGNUP_SUCCEEDED = "signup_succeeded"
    LOGIN_FAILED = "login_failed"
    LOGIN_SUCCEEDED = "login_succeeded"
    ME_VIEWED = "me_viewed"
    TOKEN_REFRESHED = "token_refreshed"
    LOGOUT = "logout"
    AUDIT_LOG_VIEW_DENIED = "audit_log_view_denied"
    AUDIT_LOG_VIEWED = "audit_log_viewed"
    OTHER = "other"


class AuditOutcome(StrEnum):
    ALLOW = "allow"
    DENIED = "denied"
    OTHER = "other"


def audit_event_type_label(value: str) -> AuditEventType:
    """감사 이벤트 타입을 auth-service metric label 값으로 정규화한다."""
    normalized = value.lower()
    for event_type in AuditEventType:
        if normalized == event_type.value:
            return event_type
    return AuditEventType.OTHER


def audit_outcome_label(value: str) -> AuditOutcome:
    """감사 이벤트 결과를 auth-service metric label 값으로 정규화한다."""
    normalized = value.lower()
    for outcome in AuditOutcome:
        if normalized == outcome.value:
            return outcome
    return AuditOutcome.OTHER
