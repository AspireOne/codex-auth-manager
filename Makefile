GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
CGO_ENABLED ?= 0
OUTPUT_DIR := dist
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

ifeq ($(GOOS),windows)
	BINARY := codex-manage.exe
else
	BINARY := codex-manage
endif

OUTPUT := $(OUTPUT_DIR)/$(BINARY)

.PHONY: build check clean cross-build hooks

build:
	@mkdir -p $(OUTPUT_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags="$(LDFLAGS)" -o $(OUTPUT) ./cmd/codex-manage

check:
	lefthook run pre-commit --force --no-auto-install

hooks:
	lefthook install

cross-build:
	@set -eu; \
	check_dir="$$(mktemp -d)"; \
	trap 'find "$$check_dir" -type f -delete; rmdir "$$check_dir"' 0; \
	for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		goos="$${platform%/*}"; \
		goarch="$${platform#*/}"; \
		extension=""; \
		if [ "$$goos" = windows ]; then extension=".exe"; fi; \
		GOOS="$$goos" GOARCH="$$goarch" CGO_ENABLED=0 go build -trimpath \
			-ldflags="-s -w -X main.version=check" \
			-o "$$check_dir/codex-manage-$$goos-$$goarch$$extension" ./cmd/codex-manage; \
	done

clean:
	@rm -rf $(OUTPUT_DIR)
