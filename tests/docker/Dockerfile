FROM python:3.12-slim

WORKDIR /workspace

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PIP_DISABLE_PIP_VERSION_CHECK=1

COPY --from=ghcr.io/astral-sh/uv:0.7.12 /uv /uvx /bin/

COPY packages/server/pyproject.toml packages/server/pyproject.toml
COPY packages/server/src packages/server/src
COPY packages/contracts/pyproject.toml packages/contracts/pyproject.toml
COPY packages/contracts/src packages/contracts/src
COPY services/auth-service/requirements.txt services/auth-service/requirements.txt
COPY services/auth-service/requirements-test.txt services/auth-service/requirements-test.txt
COPY services/concert-service/pyproject.toml services/concert-service/uv.lock services/concert-service/
COPY services/notification-service/requirements.txt services/notification-service/requirements.txt
COPY services/notification-service/requirements-test.txt services/notification-service/requirements-test.txt
COPY services/payment-service/requirements.txt services/payment-service/requirements.txt
COPY services/payment-service/requirements-test.txt services/payment-service/requirements-test.txt
COPY services/reservation-service/pyproject.toml services/reservation-service/uv.lock services/reservation-service/
COPY services/ticket-service/requirements.txt services/ticket-service/requirements.txt
COPY services/ticket-service/requirements-test.txt services/ticket-service/requirements-test.txt

RUN python -m pip install --no-cache-dir --root-user-action=ignore --upgrade pip \
    && install_requirements_service() { \
      service="$1"; \
      python -m venv "/opt/test-envs/${service}"; \
      "/opt/test-envs/${service}/bin/python" -m pip install --no-cache-dir --upgrade pip; \
      "/opt/test-envs/${service}/bin/python" -m pip install --no-cache-dir --root-user-action=ignore \
        ./packages/contracts \
        -r "services/${service}/requirements.txt" \
        -r "services/${service}/requirements-test.txt"; \
    } \
    && install_uv_service() { \
      service="$1"; \
      UV_PROJECT_ENVIRONMENT="/opt/test-envs/${service}" \
        uv sync --frozen --project "services/${service}" --group test --no-install-project; \
    } \
    && install_pytest_cov() { \
      service="$1"; \
      uv pip install --python "/opt/test-envs/${service}/bin/python" pytest-cov; \
    } \
    && install_requirements_service auth-service \
    && install_pytest_cov auth-service \
    && install_uv_service concert-service \
    && install_pytest_cov concert-service \
    && install_requirements_service notification-service \
    && install_pytest_cov notification-service \
    && install_requirements_service payment-service \
    && install_pytest_cov payment-service \
    && install_uv_service reservation-service \
    && install_pytest_cov reservation-service \
    && install_requirements_service ticket-service \
    && install_pytest_cov ticket-service
