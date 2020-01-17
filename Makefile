SHELL = /bin/bash
OS = $(shell uname -s)

# Project variables
PACKAGE = github.com/eclipse-iofog/port-manager
BINARY_NAME = port-manager
IMAGE = iofog/port-manager

# Build variables
BUILD_DIR ?= bin
BUILD_PACKAGE = $(PACKAGE)/cmd/manager
export CGO_ENABLED ?= 0
ifeq ($(VERBOSE), 1)
	GOARGS += -v
endif

GOLANG_VERSION = 1.12

GOFILES_NOVENDOR = $(shell find . -type f -name '*.go' -not -path "./vendor/*")


.PHONY: clean
clean: ## Clean the working area and the project
	rm -rf $(BUILD_DIR)/

.PHONY: build
build: GOARGS += -mod=vendor -tags "$(GOTAGS)" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)
build: fmt
ifneq ($(IGNORE_GOLANG_VERSION_REQ), 1)
	@printf "$(GOLANG_VERSION)\n$$(go version | awk '{sub(/^go/, "", $$3);print $$3}')" | sort -t '.' -k 1,1 -k 2,2 -k 3,3 -g | head -1 | grep -q -E "^$(GOLANG_VERSION)$$" || (printf "Required Go version is $(GOLANG_VERSION)\nInstalled: `go version`" && exit 1)
endif
	go build $(GOARGS) $(BUILD_PACKAGE)

.PHONY: fmt
fmt:
	@gofmt -s -w $(GOFILES_NOVENDOR)

.PHONY: test
test:
	set -o pipefail; go list -mod=vendor ./... | xargs -n1 go test -mod=vendor $(GOARGS) -v -parallel 1 2>&1 | tee test.txt

.PHONY: vendor
vendor: # Vendor all deps
	@go mod vendor

.PHONY: build-img
build-img:
	docker build -t eclipse-iofog/port-manager:latest -f build/Dockerfile .

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
