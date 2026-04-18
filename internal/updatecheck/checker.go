package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultLatestReleaseURL = "https://api.github.com/repos/AspireOne/codex-auth-manager/releases/latest"

var latestReleaseAPIURL = defaultLatestReleaseURL

type Checker struct {
	LatestReleaseURL string
	HTTPClient       *http.Client
}

type Result struct {
	CurrentVersion  string
	LatestVersion   string
	URL             string
	Checked         bool
	UpdateAvailable bool
}

type releaseResponse struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type version struct {
	major int
	minor int
	patch int
}

func New() Checker {
	return Checker{
		LatestReleaseURL: defaultLatestReleaseURL,
		HTTPClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

func Check(ctx context.Context, currentVersion string, client *http.Client) (Result, error) {
	return Checker{HTTPClient: client}.Check(ctx, currentVersion)
}

func (c Checker) Check(ctx context.Context, currentVersion string) (Result, error) {
	normalizedCurrent, currentParsed, ok := parseStableVersion(currentVersion)
	if !ok {
		return Result{CurrentVersion: currentVersion}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.latestReleaseURL(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("build update request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "codex-manage/"+normalizedCurrent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("request latest release: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("request latest release: unexpected status %s", resp.Status)
	}

	var release releaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Result{}, fmt.Errorf("decode latest release: %w", err)
	}

	if release.Draft || release.Prerelease {
		return Result{
			CurrentVersion: normalizedCurrent,
			Checked:        true,
		}, nil
	}

	normalizedLatest, latestParsed, ok := parseStableVersion(release.TagName)
	if !ok {
		return Result{}, fmt.Errorf("parse latest release tag %q", release.TagName)
	}

	return Result{
		CurrentVersion:  normalizedCurrent,
		LatestVersion:   normalizedLatest,
		URL:             release.HTMLURL,
		Checked:         true,
		UpdateAvailable: compareVersions(latestParsed, currentParsed) > 0,
	}, nil
}

func (c Checker) latestReleaseURL() string {
	if c.LatestReleaseURL != "" {
		return c.LatestReleaseURL
	}
	return latestReleaseAPIURL
}

func (c Checker) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func parseStableVersion(raw string) (string, version, bool) {
	if raw == "" || raw == "dev" {
		return "", version{}, false
	}

	trimmed := strings.TrimPrefix(raw, "v")
	core, prerelease, _ := strings.Cut(trimmed, "-")
	if prerelease != "" {
		return "", version{}, false
	}
	core, _, _ = strings.Cut(core, "+")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return "", version{}, false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", version{}, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", version{}, false
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", version{}, false
	}

	return fmt.Sprintf("v%d.%d.%d", major, minor, patch), version{
		major: major,
		minor: minor,
		patch: patch,
	}, true
}

func compareVersions(a, b version) int {
	switch {
	case a.major != b.major:
		return comparePart(a.major, b.major)
	case a.minor != b.minor:
		return comparePart(a.minor, b.minor)
	default:
		return comparePart(a.patch, b.patch)
	}
}

func comparePart(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}
