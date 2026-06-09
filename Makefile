SHELL := /bin/bash

.DEFAULT_GOAL := help

UV ?= uv
UV_PYTHON ?= python3.13
DEV_VENV_DIR ?= .venv

TEST_RUNNER_IMAGE ?= ticketing-python-test-runner:local
PYTEST_ARGS ?= -q -s -p no:cacheprovider

DOCKER_COMPOSE ?= docker compose
E2E_COMPOSE_FILE ?= tests/e2e/docker-compose.yml
E2E_COMPOSE_PROJECT ?= ticketing-e2e
E2E_NETWORK ?= $(E2E_COMPOSE_PROJECT)_default
CURL_IMAGE ?= curlimages/curl:8.7.1
NEWMAN_IMAGE ?= postman/newman:6-alpine
E2E_CONCERT_SERVICE_URL ?= http://concert-service:8082
E2E_RESERVATION_SERVICE_URL ?= http://reservation-service:8083
E2E_PAYMENT_SERVICE_URL ?= http://payment-service:8080
E2E_TICKET_SERVICE_URL ?= http://ticket-service:8085
E2E_NOTIFICATION_SERVICE_URL ?= http://notification-service:8084
E2E_DEFAULT_SCENARIOS ?= 01-concert-seat-setup 02-reservation-create 03-ticket-issue
E2E_WAIT_SERVICES ?= concert-service reservation-service payment-service ticket-service notification-service

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
	concert-service \
	reservation-service \
	payment-service \
	ticket-service \
	notification-service

APP_IMAGE_SERVICES := \
	auth-service \
	concert-service \
	reservation-service \
	payment-service \
	ticket-service \
	notification-service

SERVICE_DIRS := $(addprefix services/,$(APP_SERVICES))
.PHONY: help list install test-runner-build test-unit test test-all test-e2e e2e-scenario e2e-up e2e-wait e2e-newman e2e-down app-images-build app-images-push dev-images-build dev-images-push

help:
	@printf '%s\n' 'Ticketing service commands'
	@printf '%s\n' ''
	@printf '%s\n' 'Setup'
	@printf '  %-30s %s\n' 'make install' 'Sync the unified uv development environment.'
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
	@printf '  %-30s %s\n' 'make app-images-build' 'Build service Docker images.'
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
	if ! command -v $(UV) >/dev/null 2>&1; then \
		printf '%s\n' '$(UV) command not found. Install uv or set UV=/path/to/uv.' >&2; \
		exit 1; \
	fi; \
	UV_PROJECT_ENVIRONMENT="$(DEV_VENV_DIR)" $(UV) sync --frozen --group test --python "$(UV_PYTHON)"; \
	printf '%s\n' 'Unified uv development environment is ready at $(DEV_VENV_DIR).'

test-runner-build:
	docker build -f tests/docker/Dockerfile -t $(TEST_RUNNER_IMAGE) .

test-unit: test-runner-build
	docker run --rm -v "$(CURDIR)":/workspace -w /workspace $(TEST_RUNNER_IMAGE) sh -c 'set -e; for service in $(SERVICE_DIRS); do printf "%s\n" "Running pytest for $$service"; (cd "$$service" && PYTHONPATH=. python -m pytest $(PYTEST_ARGS)); done'

test: test-unit

test-all: test-unit test-e2e

test-e2e:
	@set -e; \
	for scenario in $(E2E_DEFAULT_SCENARIOS); do \
		$(MAKE) e2e-scenario SCENARIO=$$scenario; \
	done

e2e-scenario:
	@set -e; \
	case "$(SCENARIO)" in \
		01-concert-seat-setup) compose_services="postgres concert-service"; wait_services="concert-service" ;; \
		02-reservation-create) compose_services="postgres reservation-service"; wait_services="reservation-service" ;; \
		03-ticket-issue) compose_services="postgres kafka kafka-init ticket-service"; wait_services="ticket-service" ;; \
		*) printf 'Unknown SCENARIO=%s\n' "$(SCENARIO)" >&2; exit 2 ;; \
	esac; \
	trap '$(DOCKER_COMPOSE) -p $(E2E_COMPOSE_PROJECT) -f $(E2E_COMPOSE_FILE) down -v --remove-orphans' EXIT; \
	$(DOCKER_COMPOSE) -p $(E2E_COMPOSE_PROJECT) -f $(E2E_COMPOSE_FILE) up -d --build $$compose_services; \
	$(MAKE) e2e-wait E2E_WAIT_SERVICES="$$wait_services"; \
	$(MAKE) e2e-newman SCENARIO="$(SCENARIO)"

e2e-up:
	$(DOCKER_COMPOSE) -p $(E2E_COMPOSE_PROJECT) -f $(E2E_COMPOSE_FILE) up -d --build

e2e-wait:
	docker run --rm --network $(E2E_NETWORK) \
		-v "$(CURDIR)/tests/e2e/scripts":/scripts:ro \
		-e E2E_CONCERT_SERVICE_URL="$(E2E_CONCERT_SERVICE_URL)" \
		-e E2E_RESERVATION_SERVICE_URL="$(E2E_RESERVATION_SERVICE_URL)" \
		-e E2E_PAYMENT_SERVICE_URL="$(E2E_PAYMENT_SERVICE_URL)" \
		-e E2E_TICKET_SERVICE_URL="$(E2E_TICKET_SERVICE_URL)" \
		-e E2E_NOTIFICATION_SERVICE_URL="$(E2E_NOTIFICATION_SERVICE_URL)" \
		-e E2E_WAIT_SERVICES="$(E2E_WAIT_SERVICES)" \
		-e E2E_WAIT_TIMEOUT_SECONDS \
		-e E2E_WAIT_SLEEP_SECONDS \
		$(CURL_IMAGE) sh /scripts/wait-for-services.sh

e2e-newman:
	mkdir -p tests/e2e/newman/reports
	docker run --rm --network $(E2E_NETWORK) -v "$(CURDIR)/tests/e2e":/etc/newman -w /etc/newman $(NEWMAN_IMAGE) run scenarios/$(SCENARIO).postman_collection.json \
		-e newman/docker.postman_environment.json \
		--env-var concertServiceUrl="$(E2E_CONCERT_SERVICE_URL)" \
		--env-var reservationServiceUrl="$(E2E_RESERVATION_SERVICE_URL)" \
		--env-var paymentServiceUrl="$(E2E_PAYMENT_SERVICE_URL)" \
		--env-var ticketServiceUrl="$(E2E_TICKET_SERVICE_URL)" \
		--env-var notificationServiceUrl="$(E2E_NOTIFICATION_SERVICE_URL)" \
		--reporters cli,junit \
		--delay-request 1000 \
		--reporter-junit-export newman/reports/$(SCENARIO).xml

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
	for service in $(APP_IMAGE_SERVICES); do \
		printf 'building %s/%s:%s\n' '$(IMAGE_REPOSITORY_PREFIX)' "$$service" '$(IMAGE_TAG)'; \
		case "$$service" in \
			concert-service|reservation-service|payment-service|ticket-service|notification-service) \
				docker build -f "services/$$service/Dockerfile" -t "$(IMAGE_REPOSITORY_PREFIX)/$$service:$(IMAGE_TAG)" . ;; \
			*) \
				docker build -t "$(IMAGE_REPOSITORY_PREFIX)/$$service:$(IMAGE_TAG)" "services/$$service" ;; \
		esac; \
	done

app-images-push: app-images-build
	@set -euo pipefail; \
	for service in $(APP_IMAGE_SERVICES); do \
		printf 'pushing %s/%s:%s\n' '$(IMAGE_REPOSITORY_PREFIX)' "$$service" '$(IMAGE_TAG)'; \
		docker push "$(IMAGE_REPOSITORY_PREFIX)/$$service:$(IMAGE_TAG)"; \
	done

dev-images-build:
	$(MAKE) app-images-build IMAGE_REGISTRY="$(DEV_IMAGE_REGISTRY)" IMAGE_NAMESPACE="$(DEV_IMAGE_NAMESPACE)" IMAGE_TAG="$(DEV_IMAGE_TAG)"

dev-images-push:
	$(MAKE) app-images-push IMAGE_REGISTRY="$(DEV_IMAGE_REGISTRY)" IMAGE_NAMESPACE="$(DEV_IMAGE_NAMESPACE)" IMAGE_TAG="$(DEV_IMAGE_TAG)"
