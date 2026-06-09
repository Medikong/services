from enum import StrEnum


class PaymentMethod(StrEnum):
    MOCK = "mock"
    OTHER = "other"


class PaymentErrorCode(StrEnum):
    NONE = "none"
    FAILED = "payment.failed"
    DELAYED = "payment.delayed"
    INVALID_SIMULATION = "payment.invalid_simulation"
    INTERNAL_ERROR = "payment.internal_error"


class PaymentEventType(StrEnum):
    APPROVED = "payment-approved"
    FAILED = "payment-failed"


def payment_method_label(value: str) -> PaymentMethod:
    """결제 method 값을 metric label용 저카디널리티 값으로 정규화한다."""
    if value == PaymentMethod.MOCK.value:
        return PaymentMethod.MOCK
    return PaymentMethod.OTHER
