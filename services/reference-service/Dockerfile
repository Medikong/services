FROM python:3.12-slim AS builder

WORKDIR /build

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV UV_COMPILE_BYTECODE=1
ENV UV_LINK_MODE=copy
ENV UV_PROJECT_ENVIRONMENT=/opt/venv
ENV PATH="/opt/venv/bin:${PATH}"

COPY --from=ghcr.io/astral-sh/uv:0.7.12 /uv /uvx /bin/

COPY services/concert-service/pyproject.toml services/concert-service/uv.lock ./services/concert-service/
COPY packages/errors ./packages/errors
COPY packages/metrics ./packages/metrics
COPY packages/middleware ./packages/middleware
COPY packages/observability ./packages/observability
COPY packages/server ./packages/server
WORKDIR /build/services/concert-service
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --frozen --no-dev --no-install-project --no-editable

FROM python:3.12-slim AS runtime

WORKDIR /app

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PYTHONPATH=/app
ENV PATH="/opt/venv/bin:${PATH}"

COPY --from=builder /opt/venv /opt/venv
COPY services/concert-service/app ./app
COPY services/concert-service/cmd ./cmd
RUN groupadd --system --gid 10001 app \
    && useradd --system --uid 10001 --gid app --home-dir /app --shell /usr/sbin/nologin app \
    && chown -R app:app /app
USER 10001:10001

EXPOSE 8082
CMD ["python", "cmd/server/main.py"]
