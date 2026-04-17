GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
CGO_ENABLED ?= 0
OUTPUT_DIR := dist
LDFLAGS := -s -w

ifeq ($(GOOS),windows)
	BINARY := codex-manage.exe
else
	BINARY := codex-manage
endif

OUTPUT := $(OUTPUT_DIR)/$(BINARY)

.PHONY: build clean

build:
	@mkdir -p $(OUTPUT_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags="$(LDFLAGS)" -o $(OUTPUT) ./cmd/codex-manage

clean:
	@rm -rf $(OUTPUT_DIR)
