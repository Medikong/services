ARG PAYMENT_FAILURE_IDEMPOTENCY_SMOKE_IMAGE=python:3.12-slim
FROM ${PAYMENT_FAILURE_IDEMPOTENCY_SMOKE_IMAGE}

WORKDIR /app

RUN python -m pip install --no-cache-dir \
    "aiokafka==0.12.0" \
    "anyio==4.14.1" \
    "pydantic==2.13.4"

COPY packages/contracts/src/contracts /app/contracts
COPY tests/e2e/scripts/payment-failure-idempotency-smoke.py /app/payment-failure-idempotency-smoke.py
COPY tests/e2e/scripts/payment_failure_idempotency_support.py /app/payment_failure_idempotency_support.py

USER 65532:65532

CMD ["python", "/app/payment-failure-idempotency-smoke.py"]
