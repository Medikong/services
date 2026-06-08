FROM python:3.12-slim AS builder

WORKDIR /build

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV UV_COMPILE_BYTECODE=1
ENV UV_LINK_MODE=copy
ENV UV_PROJECT_ENVIRONMENT=/opt/venv
ENV PATH="/opt/venv/bin:${PATH}"

COPY --from=ghcr.io/astral-sh/uv:0.7.12 /uv /uvx /bin/

COPY services/payment-service/pyproject.toml services/payment-service/uv.lock ./services/payment-service/
COPY packages/errors ./packages/errors
COPY packages/kafka-utils ./packages/kafka-utils
COPY packages/middleware ./packages/middleware
COPY packages/observability ./packages/observability
COPY packages/server ./packages/server
WORKDIR /build/services/payment-service
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --frozen --no-dev --no-install-project --no-editable

FROM python:3.12-slim AS runtime

WORKDIR /app

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PYTHONPATH=/app
ENV PATH="/opt/venv/bin:${PATH}"

COPY --from=builder /opt/venv /opt/venv
COPY services/payment-service/app ./app
RUN groupadd --system --gid 10001 app \
    && useradd --system --uid 10001 --gid app --home-dir /app --shell /usr/sbin/nologin app \
    && chown -R app:app /app
USER 10001:10001

EXPOSE 8080
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8080"]
