SHELL := /bin/sh

.DEFAULT_GOAL := help

TASK ?= task
SERVICE ?=
SERVICES ?=
SCENARIO ?=
IMAGE_REGISTRY ?= localhost:5001
IMAGE_NAMESPACE ?=
IMAGE_TAG ?= dev

.PHONY: help list install test-runner-build test-service test-services test-unit test test-all test-e2e test-go-unit app-images-build app-images-push dev-images-build dev-images-push

help list:
	@$(TASK) --list

install:
	$(TASK) install

test-runner-build:
	$(TASK) test-runner-build SERVICE="$(SERVICE)"

test-service:
	$(TASK) test-service SERVICE="$(SERVICE)"

test-services:
	$(TASK) test-services SERVICES="$(SERVICES)"

test-unit:
	$(TASK) test-unit

test:
	$(TASK) test

test-all:
	$(TASK) test-all

test-e2e:
	$(TASK) test-e2e SCENARIO="$(SCENARIO)"

test-go-unit:
	$(TASK) test-go-unit

app-images-build:
	$(TASK) app-images-build IMAGE_REGISTRY="$(IMAGE_REGISTRY)" IMAGE_NAMESPACE="$(IMAGE_NAMESPACE)" IMAGE_TAG="$(IMAGE_TAG)"

app-images-push:
	$(TASK) app-images-push IMAGE_REGISTRY="$(IMAGE_REGISTRY)" IMAGE_NAMESPACE="$(IMAGE_NAMESPACE)" IMAGE_TAG="$(IMAGE_TAG)"

dev-images-build:
	$(TASK) dev-images-build DEV_IMAGE_REGISTRY="$(IMAGE_REGISTRY)" DEV_IMAGE_NAMESPACE="$(IMAGE_NAMESPACE)" DEV_IMAGE_TAG="$(IMAGE_TAG)"

dev-images-push:
	$(TASK) dev-images-push DEV_IMAGE_REGISTRY="$(IMAGE_REGISTRY)" DEV_IMAGE_NAMESPACE="$(IMAGE_NAMESPACE)" DEV_IMAGE_TAG="$(IMAGE_TAG)"
