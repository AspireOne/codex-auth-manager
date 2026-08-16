package profilestatus

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-manage/internal/codexapp"
	profilemgr "codex-manage/internal/profiles"
)

func TestShortestWindowSelectsShortestUsableWindow(t *testing.T) {
	primaryPercent, secondaryPercent := 17.6, 42.4
	primaryMinutes, secondaryMinutes := int64(10080), int64(300)
	primaryReset, secondaryReset := int64(2_000_000_000), int64(1_900_000_000)
	window, err := shortestWindow(rateLimit{
		Primary:   &rateWindow{UsedPercent: &primaryPercent, WindowDurationMin: &primaryMinutes, ResetsAt: &primaryReset},
		Secondary: &rateWindow{UsedPercent: &secondaryPercent, WindowDurationMin: &secondaryMinutes, ResetsAt: &secondaryReset},
	})
	if err != nil {
		t.Fatal(err)
	}
	if window != nil && *window.UsedPercent != secondaryPercent {
		t.Fatalf("selected percent = %v, want %v", *window.UsedPercent, secondaryPercent)
	}
}

func TestStatusFromRateLimitsPrefersCodexBucketAndRoundsUsage(t *testing.T) {
	preferredPercent, fallbackPercent := 17.6, 99.0
	minutes := int64(300)
	preferredReset, fallbackReset := int64(1_900_000_000), int64(2_000_000_000)
	fetched := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	status, err := statusFromRateLimits(rateLimitsResponse{
		RateLimitsByLimitID: map[string]rateLimit{"codex": {Primary: &rateWindow{UsedPercent: &preferredPercent, WindowDurationMin: &minutes, ResetsAt: &preferredReset}}},
		RateLimits:          &rateLimit{Primary: &rateWindow{UsedPercent: &fallbackPercent, WindowDurationMin: &minutes, ResetsAt: &fallbackReset}},
	}, fetched)
	if err != nil {
		t.Fatal(err)
	}
	if status.UsedPercent == nil || *status.UsedPercent != 18 {
		t.Fatalf("used percent = %v, want 18", status.UsedPercent)
	}
	if status.ResetsAt == nil || status.ResetsAt.Unix() != preferredReset {
		t.Fatalf("reset = %v, want %d", status.ResetsAt, preferredReset)
	}
}

func TestStatusFromRateLimitsUsesCompatibleFallback(t *testing.T) {
	percent := 12.0
	minutes, reset := int64(300), int64(1_900_000_000)
	status, err := statusFromRateLimits(rateLimitsResponse{RateLimits: &rateLimit{
		Primary: &rateWindow{UsedPercent: &percent, WindowDurationMin: &minutes, ResetsAt: &reset},
	}}, time.Now())
	if err != nil || status.UsedPercent == nil || *status.UsedPercent != 12 {
		t.Fatalf("status/error = %#v/%v", status, err)
	}
}

func TestStatusFromRateLimitsClampsOutOfRangeUsage(t *testing.T) {
	minutes, reset := int64(300), int64(1_900_000_000)
	for _, test := range []struct {
		input float64
		want  int
	}{{input: -4, want: 0}, {input: 104, want: 100}} {
		status, err := statusFromRateLimits(rateLimitsResponse{RateLimits: &rateLimit{Primary: &rateWindow{
			UsedPercent: &test.input, WindowDurationMin: &minutes, ResetsAt: &reset,
		}}}, time.Now())
		if err != nil || status.UsedPercent == nil || *status.UsedPercent != test.want {
			t.Fatalf("input %v produced status/error %#v/%v, want %d", test.input, status, err, test.want)
		}
	}
}

func TestShortestWindowRejectsIncompleteLimits(t *testing.T) {
	percent := 10.0
	if _, err := shortestWindow(rateLimit{Primary: &rateWindow{UsedPercent: &percent}}); err == nil {
		t.Fatal("shortestWindow() error = nil, want unusable-window error")
	}
}

func TestClassifyErrorOnlyMapsExplicitAuthenticationFailures(t *testing.T) {
	if !errors.Is(classifyError(&codexapp.RPCError{Message: "ChatGPT authentication required"}), ErrSignInRequired) {
		t.Fatal("authentication rejection was not classified as sign-in required")
	}
	if errors.Is(classifyError(&codexapp.RPCError{Message: "backend unavailable"}), ErrSignInRequired) {
		t.Fatal("generic rejection was classified as sign-in required")
	}
}

func TestQueryerFetchEndToEnd(t *testing.T) {
	tests := []struct {
		name          string
		scenario      string
		wantPercent   int
		wantSignIn    bool
		wantErrorText string
	}{
		{name: "preferred shortest window", scenario: "success", wantPercent: 36},
		{name: "compatible fallback", scenario: "fallback", wantPercent: 24},
		{name: "account requires auth", scenario: "signed-out", wantSignIn: true},
		{name: "non ChatGPT account", scenario: "api-account", wantSignIn: true},
		{name: "explicit RPC auth rejection", scenario: "auth-rejected", wantSignIn: true},
		{name: "generic RPC failure", scenario: "generic-rejected", wantErrorText: "backend unavailable"},
		{name: "missing limits", scenario: "missing-limits", wantErrorText: "usable quota window"},
		{name: "malformed response", scenario: "malformed", wantErrorText: "malformed protocol data"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queryer, profile, _ := newTestQueryer(t, test.scenario, false)
			status, err := queryer.Fetch(context.Background(), profile)
			switch {
			case test.wantSignIn:
				if !errors.Is(err, ErrSignInRequired) {
					t.Fatalf("Fetch() error = %v, want sign-in required", err)
				}
			case test.wantErrorText != "":
				if err == nil || !strings.Contains(err.Error(), test.wantErrorText) {
					t.Fatalf("Fetch() error = %v, want %q", err, test.wantErrorText)
				}
				if errors.Is(err, ErrSignInRequired) {
					t.Fatalf("generic error incorrectly classified as sign-in required: %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if status.UsedPercent == nil || *status.UsedPercent != test.wantPercent || status.ResetsAt == nil {
					t.Fatalf("status = %#v, want %d%% with reset", status, test.wantPercent)
				}
			}
		})
	}
}

func TestQueryerTimeoutAndCleanup(t *testing.T) {
	queryer, profile, roots := newTestQueryer(t, "wait-rate-limits", false)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := queryer.Fetch(ctx, profile)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Fetch() error = %v, want deadline exceeded", err)
	}
	for _, root := range *roots {
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("isolated query root remains after timeout: %s (%v)", root, err)
		}
	}
}

func TestQueryerReconcilesRefreshedCredentialsForSavedAndActiveProfile(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "inactive"
		if active {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			queryer, profile, _ := newTestQueryer(t, "refresh", active)
			if _, err := queryer.Fetch(context.Background(), profile); err != nil {
				t.Fatal(err)
			}
			saved := readStatusTestFile(t, filepath.Join(queryer.manager.ProfileDir, profile.Key))
			if !strings.Contains(string(saved), `"refresh":"new"`) {
				t.Fatalf("saved credentials were not reconciled: %s", saved)
			}
			if active {
				current := readStatusTestFile(t, queryer.manager.AuthFile)
				if string(current) != string(saved) {
					t.Fatalf("active credentials differ from saved refresh\nactive: %s\nsaved: %s", current, saved)
				}
			}
		})
	}
}

func TestQueryerRejectsRefreshedCredentialsForAnotherAccount(t *testing.T) {
	queryer, profile, _ := newTestQueryer(t, "wrong-refresh", false)
	before := readStatusTestFile(t, filepath.Join(queryer.manager.ProfileDir, profile.Key))
	_, err := queryer.Fetch(context.Background(), profile)
	if err == nil || !strings.Contains(err.Error(), "different account") {
		t.Fatalf("Fetch() error = %v, want identity mismatch", err)
	}
	after := readStatusTestFile(t, filepath.Join(queryer.manager.ProfileDir, profile.Key))
	if string(after) != string(before) {
		t.Fatalf("saved profile changed after identity mismatch\nbefore: %s\nafter: %s", before, after)
	}
}

func TestInstalledCodexStatusAgainstIsolatedProfileCopy(t *testing.T) {
	source := os.Getenv("CODEX_MANAGE_TEST_STATUS_AUTH")
	if source == "" {
		t.Skip("set CODEX_MANAGE_TEST_STATUS_AUTH to a saved ChatGPT auth file")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed on PATH")
	}
	auth := readStatusTestFile(t, source)
	codexDir := filepath.Join(t.TempDir(), ".codex")
	manager := profilemgr.NewManager(codexDir)
	if err := os.MkdirAll(manager.ProfileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profileKey := filepath.Base(source)
	if err := os.WriteFile(filepath.Join(manager.ProfileDir, profileKey), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	sourceDir := filepath.Dir(source)
	sourceManagerDir := filepath.Dir(sourceDir)
	sourceCodexDir := filepath.Dir(sourceManagerDir)
	if filepath.Base(sourceDir) != "profiles" {
		sourceCodexDir = sourceDir
		sourceManagerDir = filepath.Join(sourceCodexDir, "auth_manager")
	}
	if config, err := os.ReadFile(filepath.Join(sourceCodexDir, "config.toml")); err == nil { //nolint:gosec // Explicit smoke-test source.
		if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), config, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if ids, err := os.ReadFile(filepath.Join(sourceManagerDir, ".profile-installation-ids.json")); err == nil { //nolint:gosec // Explicit smoke-test source.
		if err := os.WriteFile(manager.InstallationIDsFile, ids, 0o600); err != nil {
			t.Fatal(err)
		}
	} else if installationID, err := os.ReadFile(filepath.Join(sourceCodexDir, "installation_id")); err == nil { //nolint:gosec // Explicit smoke-test source.
		ids, marshalErr := json.Marshal(map[string]string{profileKey: strings.TrimSpace(string(installationID))})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := os.WriteFile(manager.InstallationIDsFile, ids, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := manager.Snapshot()
	if err != nil || len(snapshot.Profiles) != 1 {
		t.Fatalf("Snapshot() = %#v, %v", snapshot, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := New(manager).Fetch(ctx, snapshot.Profiles[0])
	if err != nil {
		t.Fatalf("installed Codex status fetch failed: %v", err)
	}
	if status.AuthStatus != profilemgr.ProfileAuthAuthenticated || status.UsedPercent == nil || status.ResetsAt == nil {
		t.Fatalf("installed Codex returned incomplete status")
	}
}

func newTestQueryer(t *testing.T, scenario string, active bool) (*Queryer, profilemgr.ProfileSummary, *[]string) {
	t.Helper()
	codexDir := filepath.Join(t.TempDir(), ".codex")
	manager := profilemgr.NewManager(codexDir)
	if err := os.MkdirAll(manager.ProfileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	auth := statusTestAuth("acct", "old")
	if err := os.WriteFile(filepath.Join(manager.ProfileDir, "work"), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if active {
		if err := manager.Activate("work"); err != nil {
			t.Fatal(err)
		}
	}
	roots := &[]string{}
	launcher := codexapp.DefaultLauncher()
	launcher.Command = func(string, ...string) *exec.Cmd {
		return exec.Command(os.Args[0], "-test.run=TestStatusHelperProcess", "--", "--status-helper", scenario) // #nosec G204 -- test helper invocation.
	}
	launcher.TempDir = func(_, pattern string) (string, error) {
		root, err := os.MkdirTemp(t.TempDir(), pattern)
		*roots = append(*roots, root)
		return root, err
	}
	return &Queryer{manager: manager, executable: os.Args[0], launcher: launcher}, snapshot.Profiles[0], roots
}

func TestStatusHelperProcess(t *testing.T) {
	scenario := statusHelperScenario(os.Args)
	if scenario == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			fmt.Printf(`{"id":%d,"result":{}}`+"\n", request.ID)
		case "account/read":
			switch scenario {
			case "signed-out":
				fmt.Printf(`{"id":%d,"result":{"account":null,"requiresOpenaiAuth":true}}`+"\n", request.ID)
			case "api-account":
				fmt.Printf(`{"id":%d,"result":{"account":{"type":"apiKey"},"requiresOpenaiAuth":true}}`+"\n", request.ID)
			case "auth-rejected":
				fmt.Printf(`{"id":%d,"error":{"code":-32000,"message":"ChatGPT authentication required"}}`+"\n", request.ID)
			default:
				if scenario == "refresh" {
					_ = os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"), statusTestAuth("acct", "new"), 0o600)
				}
				if scenario == "wrong-refresh" {
					_ = os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"), statusTestAuth("other", "new"), 0o600)
				}
				fmt.Printf(`{"id":%d,"result":{"account":{"type":"chatgpt","email":"test@example.com","planType":"plus"},"requiresOpenaiAuth":true}}`+"\n", request.ID)
			}
		case "account/rateLimits/read":
			switch scenario {
			case "generic-rejected":
				fmt.Printf(`{"id":%d,"error":{"code":-32000,"message":"backend unavailable"}}`+"\n", request.ID)
			case "missing-limits":
				fmt.Printf(`{"id":%d,"result":{"rateLimits":null,"rateLimitsByLimitId":null}}`+"\n", request.ID)
			case "malformed":
				fmt.Println("{not-json")
			case "wait-rate-limits":
				time.Sleep(time.Hour)
			case "fallback":
				fmt.Printf(`{"id":%d,"result":{"rateLimits":{"primary":{"usedPercent":24,"windowDurationMins":300,"resetsAt":1900000000}},"rateLimitsByLimitId":null}}`+"\n", request.ID)
			default:
				fmt.Println(`{"method":"account/rateLimits/updated","params":{"ignored":true}}`)
				fmt.Printf(`{"id":%d,"result":{"rateLimits":{"primary":{"usedPercent":99,"windowDurationMins":300,"resetsAt":1900000000}},"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":10,"windowDurationMins":10080,"resetsAt":2000000000},"secondary":{"usedPercent":35.6,"windowDurationMins":300,"resetsAt":1900000000}}}}}`+"\n", request.ID)
			}
		}
	}
	os.Exit(0)
}

func statusHelperScenario(arguments []string) string {
	for index, argument := range arguments {
		if argument == "--status-helper" && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}

func statusTestAuth(accountID, refresh string) []byte {
	return []byte(fmt.Sprintf(`{"auth_mode":"account","tokens":{"account_id":%q},"refresh":%q}`+"\n", accountID, refresh))
}

func readStatusTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test-controlled path or explicit smoke-test source.
	if err != nil {
		t.Fatal(err)
	}
	return data
}
