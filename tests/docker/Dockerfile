# syntax=docker/dockerfile:1.7

FROM python:3.12-slim

ARG SERVICE

WORKDIR /workspace

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV UV_LINK_MODE=copy
ENV UV_PROJECT_ENVIRONMENT=/opt/test-env
ENV PATH="/opt/test-env/bin:${PATH}"

COPY --from=ghcr.io/astral-sh/uv:0.7.12 /uv /uvx /bin/

COPY packages/contracts packages/contracts
COPY packages/errors packages/errors
COPY packages/kafka-utils packages/kafka-utils
COPY packages/metrics packages/metrics
COPY packages/middleware packages/middleware
COPY packages/observability packages/observability
COPY packages/server packages/server
COPY services/${SERVICE}/pyproject.toml services/${SERVICE}/uv.lock services/${SERVICE}/

RUN --mount=type=cache,target=/root/.cache/uv \
    test -n "${SERVICE}" \
    && uv sync --frozen --project "services/${SERVICE}" --group test --no-install-project \
    && uv pip install --python /opt/test-env/bin/python pytest-cov
