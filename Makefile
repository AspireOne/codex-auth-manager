GOOS := linux
GOARCH := amd64
CGO_ENABLED := 0
OUTPUT_DIR := dist
OUTPUT := $(OUTPUT_DIR)/codex-manage
LDFLAGS := -s -w

.PHONY: build clean

build:
	mkdir -p $(OUTPUT_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags="$(LDFLAGS)" -o $(OUTPUT) ./cmd/codex-manage

clean:
	rm -rf $(OUTPUT_DIR)
