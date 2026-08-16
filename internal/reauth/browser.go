package reauth

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	profilemgr "codex-manage/internal/profiles"
)

const (
	browserExecutableEnv = "CODEX_MANAGE_BROWSER_EXECUTABLE"
	browserProfilesEnv   = "CODEX_MANAGE_BROWSER_PROFILES_DIR"
	legacyBraveEnv       = "CODEX_BRAVE_EXE"
	legacyProfilesEnv    = "CODEX_BROWSER_PROFILES_DIR"
	windowsOS            = "windows"
)

type browserLauncher struct {
	goos     string
	getenv   func(string) string
	homeDir  func() (string, error)
	lookPath func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
	command  func(string, ...string) *exec.Cmd
	output   func(string, ...string) ([]byte, error)
	readFile func(string) ([]byte, error)
}

type browserSelection struct {
	executable string
	name       string
	windows    bool
}

// BrowserName reports the Chromium browser that would be used for browser-based
// re-authentication without opening it or creating a profile directory.
func BrowserName() (string, error) {
	return newBrowserLauncher().selectedBrowserName()
}

func newBrowserLauncher() *browserLauncher {
	return &browserLauncher{
		goos: runtime.GOOS, getenv: os.Getenv, homeDir: os.UserHomeDir,
		lookPath: exec.LookPath, stat: os.Stat, command: exec.Command, readFile: os.ReadFile,
		output: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output() // #nosec G204 -- fixed local path-conversion commands only.
		},
	}
}

func (b *browserLauncher) Open(profile profilemgr.ProfileSummary, authURL string) error {
	if err := validateAuthURL(authURL); err != nil {
		return err
	}
	browser, err := b.resolveBrowser()
	if err != nil {
		return err
	}
	root, browserRoot, err := b.resolveProfileRoot(browser)
	if err != nil {
		return err
	}
	directory := profile.Key
	legacy := sanitizeLegacyProfileName(profile.Label)
	if !directoryExists(filepath.Join(root, directory)) && legacy != "" && directoryExists(filepath.Join(root, legacy)) {
		directory = legacy
	}
	hostDirectory := filepath.Join(root, directory)
	if err := os.MkdirAll(hostDirectory, 0o700); err != nil {
		return fmt.Errorf("failed to create browser profile directory: %w", err)
	}
	browserDirectory := filepath.Join(browserRoot, directory)
	if b.isWSL() && browser.windows {
		browserDirectory, err = b.wslPath(hostDirectory, "-w")
		if err != nil {
			return fmt.Errorf("failed to convert browser profile path for Windows: %w", err)
		}
	}

	cmd := b.command(browser.executable, "--user-data-dir="+browserDirectory, authURL)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch %s: %w", browser.name, err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("launched %s but failed to detach it: %w", browser.name, err)
	}
	return nil
}

func (b *browserLauncher) resolveBrowser() (browserSelection, error) {
	if value := strings.TrimSpace(b.getenv(browserExecutableEnv)); value != "" {
		return b.explicitBrowser(value)
	}
	if value := strings.TrimSpace(b.getenv(legacyBraveEnv)); value != "" {
		selection, err := b.explicitBrowser(value)
		selection.name = "brave"
		return selection, err
	}

	for _, candidate := range b.browserCandidates() {
		if candidate.path {
			if info, err := b.stat(candidate.value); err == nil && !info.IsDir() {
				return browserSelection{executable: candidate.value, name: candidate.name, windows: candidate.windows}, nil
			}
			continue
		}
		if executable, err := b.lookPath(candidate.value); err == nil {
			return browserSelection{executable: executable, name: candidate.name, windows: candidate.windows}, nil
		}
	}
	return browserSelection{}, errors.New("no supported Chromium browser found; set " + browserExecutableEnv)
}

func (b *browserLauncher) selectedBrowserName() (string, error) {
	browser, err := b.resolveBrowser()
	if err != nil {
		return "", err
	}
	return strings.ToUpper(browser.name[:1]) + browser.name[1:], nil
}

func (b *browserLauncher) explicitBrowser(value string) (browserSelection, error) {
	executable := value
	windowsBrowser := b.goos == windowsOS
	if !strings.ContainsAny(value, `/\\`) {
		resolved, err := b.lookPath(value)
		if err != nil {
			return browserSelection{}, fmt.Errorf("browser executable %q was not found", value)
		}
		executable = resolved
	} else if b.isWSL() && isWindowsPath(value) {
		converted, err := b.wslPath(value, "-u")
		if err != nil {
			return browserSelection{}, fmt.Errorf("failed to convert Windows browser path: %w", err)
		}
		executable = converted
	}
	if info, err := b.stat(executable); err != nil || info.IsDir() {
		return browserSelection{}, fmt.Errorf("browser executable %q does not exist", value)
	}
	if b.isWSL() && (isWindowsPath(value) || isWSLMountPath(executable)) {
		windowsBrowser = true
	}
	return browserSelection{executable: executable, name: browserName(value), windows: windowsBrowser}, nil
}

type browserCandidate struct {
	name    string
	value   string
	path    bool
	windows bool
}

func (b *browserLauncher) browserCandidates() []browserCandidate {
	if b.goos == "darwin" {
		return []browserCandidate{
			{name: "brave", value: "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser", path: true},
			{name: "chromium", value: "/Applications/Chromium.app/Contents/MacOS/Chromium", path: true},
			{name: "chrome", value: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", path: true},
			{name: "edge", value: "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", path: true},
		}
	}
	if b.goos == windowsOS || b.isWSL() {
		local := b.getenv("LOCALAPPDATA")
		programFiles := b.getenv("PROGRAMFILES")
		programFilesX86 := b.getenv("PROGRAMFILES(X86)")
		if b.isWSL() {
			local = b.windowsEnvironment("LOCALAPPDATA")
			programFiles = b.windowsEnvironment("ProgramFiles")
			programFilesX86 = b.windowsEnvironment("ProgramFiles(x86)")
		}
		paths := []browserCandidate{
			{name: "brave", value: filepath.Join(programFiles, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"), path: true, windows: true},
			{name: "brave", value: filepath.Join(local, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"), path: true, windows: true},
			{name: "chromium", value: filepath.Join(local, "Chromium", "Application", "chrome.exe"), path: true, windows: true},
			{name: "chrome", value: filepath.Join(programFiles, "Google", "Chrome", "Application", "chrome.exe"), path: true, windows: true},
			{name: "chrome", value: filepath.Join(programFilesX86, "Google", "Chrome", "Application", "chrome.exe"), path: true, windows: true},
			{name: "chrome", value: filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"), path: true, windows: true},
			{name: "edge", value: filepath.Join(programFilesX86, "Microsoft", "Edge", "Application", "msedge.exe"), path: true, windows: true},
			{name: "edge", value: filepath.Join(programFiles, "Microsoft", "Edge", "Application", "msedge.exe"), path: true, windows: true},
		}
		if b.isWSL() {
			for i := range paths {
				if converted, err := b.wslPath(paths[i].value, "-u"); err == nil {
					paths[i].value = converted
				}
			}
		}
		return paths
	}
	return []browserCandidate{
		{name: "brave", value: "brave-browser"}, {name: "brave", value: "brave"},
		{name: "chromium", value: "chromium"}, {name: "chromium", value: "chromium-browser"},
		{name: "chrome", value: "google-chrome"}, {name: "chrome", value: "google-chrome-stable"},
		{name: "edge", value: "microsoft-edge"}, {name: "edge", value: "microsoft-edge-stable"},
	}
}

func (b *browserLauncher) resolveProfileRoot(browser browserSelection) (string, string, error) {
	for _, env := range []string{browserProfilesEnv, legacyProfilesEnv} {
		if value := strings.TrimSpace(b.getenv(env)); value != "" {
			return b.normalizeProfileRoot(value, browser.windows)
		}
	}

	legacyHost, legacyBrowser, err := b.legacyProfileRoot(browser.windows)
	if err != nil {
		return "", "", err
	}
	if directoryExists(legacyHost) {
		return legacyHost, legacyBrowser, nil
	}
	return b.managedProfileRoot(browser.name, browser.windows)
}

func (b *browserLauncher) normalizeProfileRoot(value string, windowsBrowser bool) (string, string, error) {
	if b.isWSL() && isWindowsPath(value) {
		host, err := b.wslPath(value, "-u")
		if windowsBrowser {
			return host, value, err
		}
		return host, host, err
	}
	return value, value, nil
}

func (b *browserLauncher) legacyProfileRoot(windowsBrowser bool) (string, string, error) {
	if b.isWSL() && windowsBrowser {
		windowsRoot := filepath.Join(b.windowsEnvironment("USERPROFILE"), ".codex-browser-profiles")
		host, err := b.wslPath(windowsRoot, "-u")
		return host, windowsRoot, err
	}
	home, err := b.homeDir()
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	root := filepath.Join(home, ".codex-browser-profiles")
	return root, root, nil
}

func (b *browserLauncher) managedProfileRoot(browser string, windowsBrowser bool) (string, string, error) {
	if b.isWSL() && windowsBrowser {
		windowsRoot := filepath.Join(b.windowsEnvironment("LOCALAPPDATA"), "codex-manage", "browser-profiles", browser)
		host, err := b.wslPath(windowsRoot, "-u")
		return host, windowsRoot, err
	}
	if b.goos == windowsOS {
		root := filepath.Join(b.getenv("LOCALAPPDATA"), "codex-manage", "browser-profiles", browser)
		return root, root, nil
	}
	home, err := b.homeDir()
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	if b.goos == "darwin" {
		root := filepath.Join(home, "Library", "Application Support", "codex-manage", "browser-profiles", browser)
		return root, root, nil
	}
	dataHome := strings.TrimSpace(b.getenv("XDG_DATA_HOME"))
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	root := filepath.Join(dataHome, "codex-manage", "browser-profiles", browser)
	return root, root, nil
}

func (b *browserLauncher) isWSL() bool {
	if b.goos != "linux" {
		return false
	}
	data, err := b.readFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func (b *browserLauncher) windowsEnvironment(name string) string {
	output, err := b.output("cmd.exe", "/d", "/c", "echo", "%"+name+"%")
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(output))
	if value == "%"+name+"%" {
		return ""
	}
	return value
}

func (b *browserLauncher) wslPath(value, direction string) (string, error) {
	output, err := b.output("wslpath", direction, value)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func sanitizeLegacyProfileName(value string) string {
	var result strings.Builder
	lastUnderscore := false
	for _, r := range value {
		allowed := unicode.IsLetter(r) && r <= unicode.MaxASCII || unicode.IsDigit(r) || strings.ContainsRune("._@-", r)
		if allowed {
			result.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			result.WriteByte('_')
			lastUnderscore = true
		}
	}
	name := strings.TrimRight(strings.TrimLeft(result.String(), "."), ". ")
	if isWindowsReservedName(name) {
		name = "_" + name
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

func isWindowsReservedName(value string) bool {
	upper := strings.ToUpper(value)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" {
		return true
	}
	return len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9'
}

func browserName(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.Contains(base, "brave"):
		return "brave"
	case strings.Contains(base, "chromium"):
		return "chromium"
	case strings.Contains(base, "edge"), strings.Contains(base, "msedge"):
		return "edge"
	default:
		return "chrome"
	}
}

func directoryExists(path string) bool {
	info, err := os.Stat(path) // #nosec G703 -- path is selected from managed browser roots.
	if err != nil {
		return false
	}
	return info.IsDir()
}

func isWindowsPath(value string) bool {
	if len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/') {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	return parsed.Scheme != "" && len(parsed.Scheme) == 1
}

func isWSLMountPath(value string) bool {
	clean := filepath.ToSlash(filepath.Clean(value))
	return len(clean) > len("/mnt/c/") && strings.HasPrefix(clean, "/mnt/") &&
		((clean[5] >= 'a' && clean[5] <= 'z') || (clean[5] >= 'A' && clean[5] <= 'Z')) && clean[6] == '/'
}
