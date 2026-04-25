package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunListPrintsAvailableProfiles(t *testing.T) {
	home := t.TempDir()
	writeCLIAuthFile(t, filepath.Join(home, ".codex", "auth_manager", "profiles", "work"), "acct-work")
	writeCLIAuthFile(t, filepath.Join(home, ".codex", "auth_manager", "profiles", "personal"), "acct-personal")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--list"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t))

	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if got, want := stdout.String(), "personal\nwork\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunListPrintsInvalidProfilesToStderr(t *testing.T) {
	home := t.TempDir()
	profileDir := filepath.Join(home, ".codex", "auth_manager", "profiles")
	writeCLIAuthFile(t, filepath.Join(profileDir, "work"), "acct-work")
	if err := os.WriteFile(filepath.Join(profileDir, "corrupt"), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt profile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"-l"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t))

	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if got, want := stdout.String(), "work\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); !strings.Contains(got, `warning: ignored invalid profile "corrupt": invalid JSON`) {
		t.Fatalf("stderr = %q, want invalid profile warning", got)
	}
}

func TestRunSelectActivatesProfile(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	profilePath := filepath.Join(codexDir, "auth_manager", "profiles", "work")
	writeCLIAuthFile(t, profilePath, "acct-work")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--select", "work"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t))

	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if got, want := stdout.String(), "Activated profile \"work\".\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	assertCLIFileEqual(t, filepath.Join(codexDir, "auth.json"), profilePath)
}

func TestRunSelectMissingProfileReturnsError(t *testing.T) {
	home := t.TempDir()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"-s", "missing"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t))

	if code != 1 {
		t.Fatalf("run() code = %d, want 1", code)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `profile "missing" not found`) {
		t.Fatalf("stderr = %q, want missing profile error", got)
	}
}

func TestRunRejectsConflictingActionFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--list", "--select", "work"}, &stdout, &stderr, func() (string, error) {
		return t.TempDir(), nil
	}, failUIRun(t))

	if code != 2 {
		t.Fatalf("run() code = %d, want 2", code)
	}
	if got := stderr.String(); !strings.Contains(got, "cannot use --list and --select together") {
		t.Fatalf("stderr = %q, want conflict error", got)
	}
}

func TestRunRejectsEmptySelect(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--select", ""}, &stdout, &stderr, func() (string, error) {
		return t.TempDir(), nil
	}, failUIRun(t))

	if code != 2 {
		t.Fatalf("run() code = %d, want 2", code)
	}
	if got := stderr.String(); !strings.Contains(got, "--select requires a profile name") {
		t.Fatalf("stderr = %q, want empty select error", got)
	}
}

func TestRunWithoutActionStartsUI(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	code := run(nil, &stdout, &stderr, func() (string, error) {
		t.Fatal("userHomeDir should not be called when launching UI")
		return "", nil
	}, func(gotVersion string) error {
		called = true
		if gotVersion != version {
			return fmt.Errorf("version = %q, want %q", gotVersion, version)
		}
		return nil
	})

	if code != 0 {
		t.Fatalf("run() code = %d, want 0", code)
	}
	if !called {
		t.Fatal("runUI was not called")
	}
}

func failUIRun(t *testing.T) func(string) error {
	t.Helper()
	return func(string) error {
		t.Fatal("runUI should not be called")
		return nil
	}
}

func writeCLIAuthFile(t *testing.T, path, accountID string) {
	t.Helper()

	body := map[string]any{
		"auth_mode": "account",
		"tokens": map[string]any{
			"account_id": accountID,
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal auth fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func assertCLIFileEqual(t *testing.T, gotPath, wantPath string) {
	t.Helper()

	got, err := os.ReadFile(gotPath) // #nosec G304 -- test fixture path is under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", gotPath, err)
	}
	want, err := os.ReadFile(wantPath) // #nosec G304 -- test fixture path is under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", wantPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%q content = %q, want %q", gotPath, got, want)
	}
}
