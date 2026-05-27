SHELL := /bin/bash

.DEFAULT_GOAL := help

VENV_DIR ?= .venv
VENV_BOOTSTRAP_PYTHON ?= python3.13
VENV_PYTHON := $(CURDIR)/$(VENV_DIR)/bin/python

TEST_RUNNER_IMAGE ?= medical-platform-python-test-runner:local
PYTEST_ARGS ?= -q -s -p no:cacheprovider

DOCKER_COMPOSE ?= docker compose
E2E_COMPOSE_FILE ?= tests/e2e/docker-compose.yml
E2E_COMPOSE_PROJECT ?= medical-platform-e2e
E2E_NETWORK ?= $(E2E_COMPOSE_PROJECT)_default
CURL_IMAGE ?= curlimages/curl:8.7.1
NEWMAN_IMAGE ?= postman/newman:6-alpine
E2E_PATIENT_SERVICE_URL ?= http://patient-service:8081
E2E_APPOINTMENT_SERVICE_URL ?= http://appointment-service:8082
E2E_PRESCRIPTION_SERVICE_URL ?= http://prescription-service:8083
E2E_NOTIFICATION_SERVICE_URL ?= http://notification-service:8084

LOCAL_REGISTRY_HOST ?= 10.10.10.10
LOCAL_REGISTRY_PORT ?= 5000
IMAGE_REGISTRY ?= $(LOCAL_REGISTRY_HOST):$(LOCAL_REGISTRY_PORT)
IMAGE_NAMESPACE ?=
IMAGE_TAG ?= latest
DEV_IMAGE_REGISTRY ?= localhost:5001
DEV_IMAGE_NAMESPACE ?=
DEV_IMAGE_TAG ?= dev
IMAGE_REPOSITORY_PREFIX := $(IMAGE_REGISTRY)
ifneq ($(strip $(IMAGE_NAMESPACE)),)
IMAGE_REPOSITORY_PREFIX := $(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)
endif

APP_SERVICES := \
	auth-service \
	patient-service \
	appointment-service \
	prescription-service \
	notification-service

SERVICE_DIRS := $(addprefix services/,$(APP_SERVICES))
DASHBOARD_SERVICE ?= dashboard

.PHONY: help list install test-runner-build test-unit test test-all test-e2e e2e-up e2e-wait e2e-newman e2e-down app-images-build app-images-push dev-images-build dev-images-push

help:
	@printf '%s\n' 'Medical Platform service commands'
	@printf '%s\n' ''
	@printf '%s\n' 'Setup'
	@printf '  %-30s %s\n' 'make install' 'Create a Python venv and install service dependencies.'
	@printf '%s\n' ''
	@printf '%s\n' 'Tests'
	@printf '  %-30s %s\n' 'make test-unit' 'Run FastAPI service pytest in the Docker Python runner.'
	@printf '  %-30s %s\n' 'make test' 'Run the default test target, same as make test-unit.'
	@printf '  %-30s %s\n' 'make test-all' 'Run unit tests and Docker Compose E2E tests.'
	@printf '  %-30s %s\n' 'make test-e2e' 'Run PostgreSQL/MongoDB/Kafka E2E scenarios with Docker Compose.'
	@printf '  %-30s %s\n' 'make e2e-up' 'Start the E2E Docker Compose stack.'
	@printf '  %-30s %s\n' 'make e2e-wait' 'Wait for E2E services with the Docker curl container.'
	@printf '  %-30s %s\n' 'make e2e-newman' 'Run the E2E collection with the Docker Newman container.'
	@printf '  %-30s %s\n' 'make e2e-down' 'Stop and remove the E2E Docker Compose stack.'
	@printf '%s\n' ''
	@printf '%s\n' 'Images'
	@printf '  %-30s %s\n' 'make app-images-build' 'Build service and dashboard Docker images.'
	@printf '  %-30s %s\n' 'make app-images-push' 'Build images and push them to the registry.'
	@printf '  %-30s %s\n' 'make dev-images-build' 'Build Docker Desktop dev images with DEV_IMAGE_* variables.'
	@printf '  %-30s %s\n' 'make dev-images-push' 'Push Docker Desktop dev images with DEV_IMAGE_* variables.'
	@printf '%s\n' ''
	@printf '%s\n' 'Image variable examples'
	@printf '  %-30s %s\n' 'IMAGE_TAG=dev make app-images-build' 'Build with the dev tag for the default local registry.'
	@printf '  %-30s %s\n' 'DEV_IMAGE_TAG=dev make dev-images-push' 'Build and push to the Docker Desktop dev registry.'
	@printf '  %-30s %s\n' 'IMAGE_REGISTRY=ghcr.io IMAGE_NAMESPACE=org/repo make app-images-push' 'Push to an external registry.'

list: help

install:
	@set -e; \
	if ! command -v $(VENV_BOOTSTRAP_PYTHON) >/dev/null 2>&1; then \
		printf '%s\n' '$(VENV_BOOTSTRAP_PYTHON) command not found. Set VENV_BOOTSTRAP_PYTHON=python3 to use another interpreter.' >&2; \
		exit 1; \
	fi; \
	$(VENV_BOOTSTRAP_PYTHON) -m venv $(VENV_DIR); \
	$(VENV_PYTHON) -m pip install --upgrade pip; \
	$(VENV_PYTHON) -m pip install \
		-r services/auth-service/requirements.txt \
		-r services/auth-service/requirements-test.txt \
		-r services/patient-service/requirements.txt \
		-r services/patient-service/requirements-test.txt \
		-r services/appointment-service/requirements.txt \
		-r services/appointment-service/requirements-test.txt \
		-r services/prescription-service/requirements.txt \
		-r services/prescription-service/requirements-test.txt \
		-r services/notification-service/requirements.txt \
		-r services/notification-service/requirements-test.txt; \
	printf '%s\n' 'Python venv is ready at $(VENV_DIR).'

test-runner-build:
	docker build -f tests/docker/Dockerfile -t $(TEST_RUNNER_IMAGE) .

test-unit: test-runner-build
	docker run --rm -v "$(CURDIR)":/workspace -w /workspace $(TEST_RUNNER_IMAGE) sh -c 'set -e; for service in $(SERVICE_DIRS); do printf "%s\n" "Running pytest for $$service"; (cd "$$service" && PYTHONPATH=. python -m pytest $(PYTEST_ARGS)); done'

test: test-unit

test-all: test-unit test-e2e

test-e2e:
	@set -e; \
	trap '$(DOCKER_COMPOSE) -p $(E2E_COMPOSE_PROJECT) -f $(E2E_COMPOSE_FILE) down -v --remove-orphans' EXIT INT TERM; \
	$(MAKE) e2e-up; \
	$(MAKE) e2e-wait; \
	$(MAKE) e2e-newman

e2e-up:
	$(DOCKER_COMPOSE) -p $(E2E_COMPOSE_PROJECT) -f $(E2E_COMPOSE_FILE) up -d --build

e2e-wait:
	docker run --rm --network $(E2E_NETWORK) \
		-v "$(CURDIR)/tests/e2e/scripts":/scripts:ro \
		-e E2E_PATIENT_SERVICE_URL="$(E2E_PATIENT_SERVICE_URL)" \
		-e E2E_APPOINTMENT_SERVICE_URL="$(E2E_APPOINTMENT_SERVICE_URL)" \
		-e E2E_PRESCRIPTION_SERVICE_URL="$(E2E_PRESCRIPTION_SERVICE_URL)" \
		-e E2E_NOTIFICATION_SERVICE_URL="$(E2E_NOTIFICATION_SERVICE_URL)" \
		-e E2E_WAIT_TIMEOUT_SECONDS \
		-e E2E_WAIT_SLEEP_SECONDS \
		$(CURL_IMAGE) sh /scripts/wait-for-services.sh

e2e-newman:
	mkdir -p tests/e2e/newman/reports
	docker run --rm --network $(E2E_NETWORK) -v "$(CURDIR)/tests/e2e":/etc/newman -w /etc/newman $(NEWMAN_IMAGE) run postman/medical-platform.postman_collection.json \
		-e newman/docker.postman_environment.json \
		--env-var patientServiceUrl="$(E2E_PATIENT_SERVICE_URL)" \
		--env-var appointmentServiceUrl="$(E2E_APPOINTMENT_SERVICE_URL)" \
		--env-var prescriptionServiceUrl="$(E2E_PRESCRIPTION_SERVICE_URL)" \
		--env-var notificationServiceUrl="$(E2E_NOTIFICATION_SERVICE_URL)" \
		--reporters cli,junit \
		--delay-request 1000 \
		--reporter-junit-export newman/reports/e2e.xml

e2e-down:
	$(DOCKER_COMPOSE) -p $(E2E_COMPOSE_PROJECT) -f $(E2E_COMPOSE_FILE) down -v --remove-orphans

app-images-build:
	@set -euo pipefail; \
	if [ -z "$(strip $(IMAGE_REGISTRY))" ]; then \
		printf '%s\n' 'IMAGE_REGISTRY is required, for example: make app-images-build IMAGE_REGISTRY=localhost:5001 IMAGE_TAG=dev' >&2; \
		exit 2; \
	fi; \
	if [ -z "$(strip $(IMAGE_TAG))" ]; then \
		printf '%s\n' 'IMAGE_TAG is required, for example: make app-images-build IMAGE_REGISTRY=localhost:5001 IMAGE_TAG=dev' >&2; \
		exit 2; \
	fi; \
	for service in $(APP_SERVICES); do \
		printf 'building %s/%s:%s\n' '$(IMAGE_REPOSITORY_PREFIX)' "$$service" '$(IMAGE_TAG)'; \
		docker build -t "$(IMAGE_REPOSITORY_PREFIX)/$$service:$(IMAGE_TAG)" "services/$$service"; \
	done; \
	printf 'building %s/%s:%s\n' '$(IMAGE_REPOSITORY_PREFIX)' '$(DASHBOARD_SERVICE)' '$(IMAGE_TAG)'; \
	docker build -t "$(IMAGE_REPOSITORY_PREFIX)/$(DASHBOARD_SERVICE):$(IMAGE_TAG)" "$(DASHBOARD_SERVICE)"

app-images-push: app-images-build
	@set -euo pipefail; \
	for service in $(APP_SERVICES) $(DASHBOARD_SERVICE); do \
		printf 'pushing %s/%s:%s\n' '$(IMAGE_REPOSITORY_PREFIX)' "$$service" '$(IMAGE_TAG)'; \
		docker push "$(IMAGE_REPOSITORY_PREFIX)/$$service:$(IMAGE_TAG)"; \
	done

dev-images-build:
	$(MAKE) app-images-build IMAGE_REGISTRY="$(DEV_IMAGE_REGISTRY)" IMAGE_NAMESPACE="$(DEV_IMAGE_NAMESPACE)" IMAGE_TAG="$(DEV_IMAGE_TAG)"

dev-images-push:
	$(MAKE) app-images-push IMAGE_REGISTRY="$(DEV_IMAGE_REGISTRY)" IMAGE_NAMESPACE="$(DEV_IMAGE_NAMESPACE)" IMAGE_TAG="$(DEV_IMAGE_TAG)"
