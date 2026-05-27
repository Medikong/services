FROM python:3.12-slim

WORKDIR /workspace

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PIP_DISABLE_PIP_VERSION_CHECK=1

COPY services/auth-service/requirements.txt services/auth-service/requirements.txt
COPY services/auth-service/requirements-test.txt services/auth-service/requirements-test.txt
COPY services/patient-service/requirements.txt services/patient-service/requirements.txt
COPY services/patient-service/requirements-test.txt services/patient-service/requirements-test.txt
COPY services/appointment-service/requirements.txt services/appointment-service/requirements.txt
COPY services/appointment-service/requirements-test.txt services/appointment-service/requirements-test.txt
COPY services/prescription-service/requirements.txt services/prescription-service/requirements.txt
COPY services/prescription-service/requirements-test.txt services/prescription-service/requirements-test.txt
COPY services/notification-service/requirements.txt services/notification-service/requirements.txt
COPY services/notification-service/requirements-test.txt services/notification-service/requirements-test.txt

RUN python -m pip install --no-cache-dir --root-user-action=ignore --upgrade pip \
    && python -m pip install --no-cache-dir --root-user-action=ignore \
      -r services/auth-service/requirements.txt \
      -r services/auth-service/requirements-test.txt \
      -r services/patient-service/requirements.txt \
      -r services/patient-service/requirements-test.txt \
      -r services/appointment-service/requirements.txt \
      -r services/appointment-service/requirements-test.txt \
      -r services/prescription-service/requirements.txt \
      -r services/prescription-service/requirements-test.txt \
      -r services/notification-service/requirements.txt \
      -r services/notification-service/requirements-test.txt
