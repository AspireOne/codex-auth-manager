GOOS := linux
GOARCH := amd64
CGO_ENABLED := 0
OUTPUT := codex-manage
LDFLAGS := -s -w

.PHONY: build clean

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags="$(LDFLAGS)" -o $(OUTPUT)

clean:
	rm -f $(OUTPUT)
