SHELL = /bin/bash
OS = $(shell uname -s)

# Project variables
PACKAGE = github.com/eclipse-iofog/port-manager/v3
BINARY_NAME = port-manager
IMAGE = iofog/port-manager

# Build variables
BUILD_DIR ?= bin
BUILD_PACKAGE = $(PACKAGE)/cmd/manager
export CGO_ENABLED ?= 0
ifeq ($(VERBOSE), 1)
	GOARGS += -v
endif

GOFILES_NOVENDOR = $(shell find . -type f -name '*.go' -not -path "./vendor/*")

MAJOR ?= $(shell cat version | grep MAJOR | sed 's/MAJOR=//g')
MINOR ?= $(shell cat version | grep MINOR | sed 's/MINOR=//g')
PATCH ?= $(shell cat version | grep PATCH | sed 's/PATCH=//g')
SUFFIX ?= $(shell cat version | grep SUFFIX | sed 's/SUFFIX=//g')
VERSION = $(MAJOR).$(MINOR).$(PATCH)$(SUFFIX)
GO_SDK_MODULE = iofog-go-sdk/v3@v3.0.0-alpha1

.PHONY: clean
clean: ## Clean the working area and the project
	rm -rf $(BUILD_DIR)/

.PHONY: build
build: GOARGS += -mod=vendor -tags "$(GOTAGS)" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)
build: fmt
	go build $(GOARGS) $(BUILD_PACKAGE)

.PHONY: lint
lint: fmt
	@golangci-lint run --timeout 5m0s

.PHONY: fmt
fmt:
	@gofmt -s -w $(GOFILES_NOVENDOR)

.PHONY: test
test:
	set -o pipefail; go list -mod=vendor ./... | xargs -n1 go test -mod=vendor $(GOARGS) -v -parallel 1 2>&1 | tee test.txt

.PHONY: modules
modules: get ## Get modules

.PHONY: get
get: ## Pull modules
	@for module in $(GO_SDK_MODULE); do \
		go get github.com/eclipse-iofog/$$module; \
	done

.PHONY: vendor
vendor: modules # Vendor all deps
	@go mod vendor

.PHONY: build-img
build-img:
	docker build -t eclipse-iofog/port-manager:latest -f build/Dockerfile .

golangci-lint: ## Install golangci
ifeq (, $(shell which golangci-lint))
	@{ \
	set -e ;\
	GOLANGCI_TMP_DIR=$$(mktemp -d) ;\
	cd $$GOLANGCI_TMP_DIR ;\
	go mod init tmp ;\
	go get github.com/golangci/golangci-lint/cmd/golangci-lint@v1.33.0 ;\
	rm -rf $$GOLANGCI_TMP_DIR ;\
	}
GOLANGCI_LINT=$(GOBIN)/golangci-lint
else
GOLANGCI_LINT=$(shell which golangci-lint)
endif

.PHONY: list
list: ## List all make targets
	@$(MAKE) -pRrn : -f $(MAKEFILE_LIST) 2>/dev/null | awk -v RS= -F: '/^# File/,/^# Finished Make data base/ {if ($$1 !~ "^[#.]") {print $$1}}' | egrep -v -e '^[^[:alnum:]]' -e '^$@$$' | sort

.PHONY: help
.DEFAULT_GOAL := help
help:
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

# Variable outputting/exporting rules
var-%: ; @echo $($*)
varexport-%: ; @echo $*=$($*)
