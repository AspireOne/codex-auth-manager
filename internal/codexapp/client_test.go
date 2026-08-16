package codexapp

import (
	"strings"
	"testing"
)

func TestIsolatedEnvironmentRemovesCredentialOverrides(t *testing.T) {
	got := strings.Join(IsolatedEnvironment([]string{
		"PATH=/bin", "CODEX_HOME=/real", "CODEX_SQLITE_HOME=/real/db",
		"CODEX_ACCESS_TOKEN=secret", "CODEX_API_KEY=secret", "OPENAI_API_KEY=secret",
		"HTTPS_PROXY=http://proxy",
	}, "/isolated/codex", "/isolated/sqlite"), "\n")
	for _, forbidden := range []string{"/real", "secret"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("environment contains %q: %s", forbidden, got)
		}
	}
	for _, want := range []string{"CODEX_HOME=/isolated/codex", "CODEX_SQLITE_HOME=/isolated/sqlite", "HTTPS_PROXY=http://proxy"} {
		if !strings.Contains(got, want) {
			t.Fatalf("environment missing %q: %s", want, got)
		}
	}
}

func TestTailBufferKeepsBoundedDiagnosticTail(t *testing.T) {
	buffer := &tailBuffer{limit: 8}
	_, _ = buffer.Write([]byte("old diagnostics:"))
	_, _ = buffer.Write([]byte(" useful"))
	if got, want := buffer.String(), ": useful"; got != want {
		t.Fatalf("tail = %q, want %q", got, want)
	}
}
