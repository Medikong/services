# syntax=docker/dockerfile:1.7

FROM python:3.12-slim-bookworm

WORKDIR /workspace

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV UV_LINK_MODE=copy
ENV PATH="/workspace/services/payment-service/.venv/bin:${PATH}"

COPY --from=ghcr.io/astral-sh/uv:0.7.12 /uv /uvx /bin/

COPY packages/contracts packages/contracts
COPY packages/errors packages/errors
COPY packages/kafka-utils packages/kafka-utils
COPY packages/middleware packages/middleware
COPY packages/observability packages/observability
COPY services/payment-service services/payment-service

RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --frozen --project services/payment-service --no-install-project --no-dev

RUN useradd --create-home --uid 10001 --shell /usr/sbin/nologin appuser \
    && chown -R appuser:appuser /workspace/services/payment-service

WORKDIR /workspace/services/payment-service

USER appuser

EXPOSE 8083

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8083"]
