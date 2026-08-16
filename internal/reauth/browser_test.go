package reauth

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	profilemgr "codex-manage/internal/profiles"
)

func TestBrowserDetectionOrder(t *testing.T) {
	var lookedUp []string
	browser := testBrowserLauncher(t)
	browser.lookPath = func(name string) (string, error) {
		lookedUp = append(lookedUp, name)
		if name == "google-chrome" {
			return "/browser/chrome", nil
		}
		return "", errors.New("missing")
	}

	selected, err := browser.resolveBrowser()
	if err != nil {
		t.Fatalf("resolveBrowser() error = %v", err)
	}
	if selected.name != "chrome" {
		t.Fatalf("browser = %q, want chrome", selected.name)
	}
	want := []string{"brave-browser", "brave", "chromium", "chromium-browser", "google-chrome"}
	if !reflect.DeepEqual(lookedUp, want) {
		t.Fatalf("lookup order = %v, want %v", lookedUp, want)
	}
}

func TestBrowserEnvironmentOverridePrecedence(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "custom-browser")
	if err := os.WriteFile(executable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	browser := testBrowserLauncher(t)
	browser.getenv = func(name string) string {
		switch name {
		case browserExecutableEnv:
			return executable
		case legacyBraveEnv:
			return "/ignored/brave"
		default:
			return ""
		}
	}

	selected, err := browser.resolveBrowser()
	if err != nil {
		t.Fatalf("resolveBrowser() error = %v", err)
	}
	if selected.executable != executable {
		t.Fatalf("executable = %q, want %q", selected.executable, executable)
	}
}

func TestBrowserUsesLegacyProfileDirectoryWhenPresent(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "person@example.com")
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "brave")
	if err := os.WriteFile(executable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	browser := testBrowserLauncher(t)
	browser.getenv = func(name string) string {
		switch name {
		case browserExecutableEnv:
			return executable
		case browserProfilesEnv:
			return root
		default:
			return ""
		}
	}
	browser.command = func(_ string, args ...string) *exec.Cmd {
		gotArgs = append([]string(nil), args...)
		return exec.Command(os.Args[0], "-test.run=TestDetachedBrowserHelper") // #nosec G204 -- test helper invocation.
	}

	err := browser.Open(profilemgr.ProfileSummary{Key: "chatgpt-stable", Label: "person@example.com"}, "https://chatgpt.com/oauth")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "--user-data-dir="+legacy || gotArgs[1] != "https://chatgpt.com/oauth" {
		t.Fatalf("browser args = %v", gotArgs)
	}
}

func TestManagedProfileRootDefaults(t *testing.T) {
	browser := testBrowserLauncher(t)
	browser.homeDir = func() (string, error) { return "/home/person", nil }
	browser.getenv = func(name string) string {
		if name == "XDG_DATA_HOME" {
			return "/data"
		}
		return ""
	}
	host, browserPath, err := browser.managedProfileRoot("brave", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/data", "codex-manage", "browser-profiles", "brave")
	if host != want || browserPath != want {
		t.Fatalf("roots = %q/%q, want %q", host, browserPath, want)
	}
}

func TestWSLBrowserReceivesWindowsProfilePath(t *testing.T) {
	root := wslTestRoot(t)
	fixtureExecutable := filepath.Join(t.TempDir(), "browser.exe")
	if err := os.WriteFile(fixtureExecutable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureInfo, err := os.Stat(fixtureExecutable)
	if err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	browser := testBrowserLauncher(t)
	browser.readFile = func(string) ([]byte, error) { return []byte("microsoft-standard-WSL2"), nil }
	browser.getenv = func(name string) string {
		switch name {
		case browserExecutableEnv:
			return "/mnt/c/fixture/browser.exe"
		case browserProfilesEnv:
			return root
		default:
			return ""
		}
	}
	browser.stat = func(string) (os.FileInfo, error) { return fixtureInfo, nil }
	browser.output = func(name string, args ...string) ([]byte, error) {
		if name == "wslpath" && len(args) == 2 && args[0] == "-w" {
			return []byte(`C:\Users\person\profile` + "\n"), nil
		}
		return nil, errors.New("unexpected conversion")
	}
	browser.command = func(_ string, args ...string) *exec.Cmd {
		gotArgs = append([]string(nil), args...)
		return exec.Command(os.Args[0], "-test.run=TestDetachedBrowserHelper") // #nosec G204 -- test helper invocation.
	}

	err = browser.Open(profilemgr.ProfileSummary{Key: "chatgpt-stable", Label: "person@example.com"}, "https://chatgpt.com/oauth")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(gotArgs) != 2 || gotArgs[0] != `--user-data-dir=C:\Users\person\profile` {
		t.Fatalf("browser args = %v, want converted Windows profile path", gotArgs)
	}
}

func TestWSLNativeBrowserKeepsLinuxProfilePath(t *testing.T) {
	root := wslTestRoot(t)
	fixtureExecutable := filepath.Join(t.TempDir(), "chromium")
	if err := os.WriteFile(fixtureExecutable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureInfo, err := os.Stat(fixtureExecutable)
	if err != nil {
		t.Fatal(err)
	}
	executable := "/tmp/codex-manage-browser"
	var gotArgs []string
	browser := testBrowserLauncher(t)
	browser.readFile = func(string) ([]byte, error) { return []byte("microsoft-standard-WSL2"), nil }
	browser.getenv = func(name string) string {
		switch name {
		case browserExecutableEnv:
			return executable
		case browserProfilesEnv:
			return root
		default:
			return ""
		}
	}
	browser.stat = func(string) (os.FileInfo, error) { return fixtureInfo, nil }
	browser.output = func(string, ...string) ([]byte, error) {
		return nil, errors.New("native WSL browser must not invoke path conversion")
	}
	browser.command = func(_ string, args ...string) *exec.Cmd {
		gotArgs = append([]string(nil), args...)
		return exec.Command(os.Args[0], "-test.run=TestDetachedBrowserHelper") // #nosec G204 -- test helper invocation.
	}

	if err := browser.Open(profilemgr.ProfileSummary{Key: "chatgpt-stable", Label: "person@example.com"}, "https://chatgpt.com/oauth"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	want := "--user-data-dir=" + filepath.Join(root, "chatgpt-stable")
	if len(gotArgs) != 2 || gotArgs[0] != want {
		t.Fatalf("browser args = %v, want native WSL path %q", gotArgs, want)
	}
}

func wslTestRoot(t *testing.T) string {
	t.Helper()
	root := filepath.ToSlash(filepath.Join(string(os.PathSeparator), "tmp", "codex-manage", t.Name()))
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func TestWSLBrowserCommandDetectsResolvedWindowsExecutable(t *testing.T) {
	fixtureExecutable := filepath.Join(t.TempDir(), "browser.exe")
	if err := os.WriteFile(fixtureExecutable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureInfo, err := os.Stat(fixtureExecutable)
	if err != nil {
		t.Fatal(err)
	}
	browser := testBrowserLauncher(t)
	browser.readFile = func(string) ([]byte, error) { return []byte("microsoft-standard-WSL2"), nil }
	browser.lookPath = func(string) (string, error) { return "/mnt/c/fixture/brave.exe", nil }
	browser.stat = func(string) (os.FileInfo, error) { return fixtureInfo, nil }

	selected, err := browser.explicitBrowser("brave.exe")
	if err != nil {
		t.Fatalf("explicitBrowser() error = %v", err)
	}
	if !selected.windows {
		t.Fatalf("selection = %#v, want resolved Windows browser", selected)
	}
}

func TestInstalledBrowserResolution(t *testing.T) {
	if os.Getenv("CODEX_MANAGE_TEST_INSTALLED_BROWSER") != "1" {
		t.Skip("set CODEX_MANAGE_TEST_INSTALLED_BROWSER=1 to exercise local browser discovery")
	}
	browser := newBrowserLauncher()
	selected, err := browser.resolveBrowser()
	if err != nil {
		t.Fatalf("resolveBrowser() error = %v", err)
	}
	hostRoot, browserRoot, err := browser.resolveProfileRoot(selected)
	if err != nil {
		t.Fatalf("resolveProfileRoot() error = %v", err)
	}
	if selected.executable == "" || selected.name == "" || hostRoot == "" || browserRoot == "" {
		t.Fatalf("incomplete browser resolution: selection=%#v roots=%q/%q", selected, hostRoot, browserRoot)
	}
	t.Logf("resolved %s (windows=%t) at %s with profile root %s", selected.name, selected.windows, selected.executable, browserRoot)
}

func TestSanitizeLegacyProfileName(t *testing.T) {
	tests := map[string]string{
		"../evil name.": "_evil_name",
		"CON":           "_CON",
		"person@x.test": "person@x.test",
	}
	for input, want := range tests {
		if got := sanitizeLegacyProfileName(input); got != want {
			t.Fatalf("sanitizeLegacyProfileName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDetachedBrowserHelper(t *testing.T) {
	if strings.Contains(strings.Join(os.Args, " "), "TestDetachedBrowserHelper") {
		return
	}
}

func testBrowserLauncher(t *testing.T) *browserLauncher {
	t.Helper()
	return &browserLauncher{
		goos: "linux", getenv: func(string) string { return "" },
		homeDir:  func() (string, error) { return t.TempDir(), nil },
		lookPath: func(string) (string, error) { return "", errors.New("missing") },
		stat:     os.Stat, command: exec.Command,
		output:   func(string, ...string) ([]byte, error) { return nil, errors.New("not WSL") },
		readFile: func(string) ([]byte, error) { return []byte("linux"), nil },
	}
}
