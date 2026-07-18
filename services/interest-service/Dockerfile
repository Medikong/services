# syntax=docker/dockerfile:1.7

FROM python:3.12-slim-bookworm

WORKDIR /workspace

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV UV_LINK_MODE=copy
ENV PATH="/workspace/services/interest-service/.venv/bin:${PATH}"

COPY --from=ghcr.io/astral-sh/uv:0.7.12 /uv /uvx /bin/

COPY packages/errors packages/errors
COPY packages/middleware packages/middleware
COPY packages/observability packages/observability
COPY services/interest-service services/interest-service

RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --frozen --project services/interest-service --no-install-project --no-dev

WORKDIR /workspace/services/interest-service

EXPOSE 8085

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8085"]
