package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	profilemgr "codex-manage/internal/profiles"
)

func TestRunListPrintsAvailableProfiles(t *testing.T) {
	home := t.TempDir()
	writeCLIAuthFile(t, filepath.Join(home, ".codex", "auth_manager", "profiles", "work"), "acct-work")
	writeCLIAuthFile(t, filepath.Join(home, ".codex", "auth_manager", "profiles", "personal"), "acct-personal")
	metadataPath := filepath.Join(home, ".codex", "auth_manager", ".profile-metadata.json")
	if err := os.WriteFile(metadataPath, []byte(`{"work":{"plan":"pro"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile metadata: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--list"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t))

	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if got, want := stdout.String(), "acct-personal@example.com\nacct-work@example.com\n"; got != want {
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
	if got, want := stdout.String(), "acct-work@example.com\n"; got != want {
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
	code := run([]string{"--select", "acct-work@example.com"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t))

	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if got, want := stdout.String(), "Activated profile \"acct-work@example.com\".\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	assertCLIFileEqual(t, filepath.Join(codexDir, "auth.json"), profilePath)
	if got := readCLIInstallationID(t, filepath.Join(codexDir, "installation_id")); !uuidV4Pattern.MatchString(got) {
		t.Fatalf("installation_id = %q, want UUID v4", got)
	}
	ids := readCLIInstallationIDs(t, filepath.Join(codexDir, "auth_manager", ".profile-installation-ids.json"))
	if ids["work"] != readCLIInstallationID(t, filepath.Join(codexDir, "installation_id")) {
		t.Fatalf("profile installation ID = %q, want active installation_id", ids["work"])
	}
}

func TestRunSelectSwitchesInstallationIDByProfile(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	profileDir := filepath.Join(codexDir, "auth_manager", "profiles")
	writeCLIAuthFile(t, filepath.Join(profileDir, "work"), "acct-work")
	writeCLIAuthFile(t, filepath.Join(profileDir, "personal"), "acct-personal")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"--select", "acct-work@example.com"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t)); code != 0 {
		t.Fatalf("run(work) code = %d, want 0; stderr=%q", code, stderr.String())
	}
	workID := readCLIInstallationID(t, filepath.Join(codexDir, "installation_id"))

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--select", "acct-personal@example.com"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t)); code != 0 {
		t.Fatalf("run(personal) code = %d, want 0; stderr=%q", code, stderr.String())
	}
	personalID := readCLIInstallationID(t, filepath.Join(codexDir, "installation_id"))
	if personalID == workID {
		t.Fatalf("installation_id after switching = %q, want different value from %q", personalID, workID)
	}
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
	if got := stderr.String(); !strings.Contains(got, `profile label "missing" not found`) {
		t.Fatalf("stderr = %q, want missing profile error", got)
	}
}

func TestUniqueProfileByLabelRejectsAmbiguousMatches(t *testing.T) {
	profiles := []profilemgr.ProfileSummary{
		{Key: "first", Label: "duplicate"},
		{Key: "second", Label: "duplicate"},
	}

	_, err := uniqueProfileByLabel(profiles, "duplicate")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("uniqueProfileByLabel() error = %v, want ambiguity error", err)
	}
}

func TestRunSelectDoesNotAcceptStorageKeyAlias(t *testing.T) {
	home := t.TempDir()
	writeCLIAuthFile(t, filepath.Join(home, ".codex", "auth_manager", "profiles", "work"), "acct-work")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--select", "work"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t))

	if code != 1 || !strings.Contains(stderr.String(), `profile label "work" not found`) {
		t.Fatalf("code/stderr = %d/%q, want rejected storage-key selector", code, stderr.String())
	}
}

func TestRunSelectAcceptsQuotedAPIKeyLabelWithSpaces(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	profileDir := filepath.Join(codexDir, "auth_manager", "profiles")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "legacy-api"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-cli"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile API profile: %v", err)
	}
	metadataPath := filepath.Join(codexDir, "auth_manager", ".profile-metadata.json")
	if err := os.WriteFile(metadataPath, []byte(`{"legacy-api":{"label":"Personal project"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile metadata: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--select", "Personal project"}, &stdout, &stderr, func() (string, error) {
		return home, nil
	}, failUIRun(t))

	if code != 0 || stdout.String() != "Activated profile \"Personal project\".\n" {
		t.Fatalf("code/stdout/stderr = %d/%q/%q", code, stdout.String(), stderr.String())
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
			"id_token":   cliIDToken(accountID + "@example.com"),
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

func cliIDToken(email string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"email":%q}`, email)))
	return "header." + payload + ".signature"
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

var uuidV4Pattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func readCLIInstallationID(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path is under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

func readCLIInstallationIDs(t *testing.T, path string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path is under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var ids map[string]string
	if err := json.Unmarshal(data, &ids); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}
	return ids
}
