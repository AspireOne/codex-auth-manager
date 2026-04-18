package updatecheck

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const testCurrentVersion = "v1.2.3"

func TestCheckSkipsDevBuildsWithoutCallingTheNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	restore := overrideLatestReleaseAPIURL(t, server.URL)

	result, err := Check(context.Background(), "dev", server.Client())
	restore()

	if err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	if result.UpdateAvailable {
		t.Fatalf("UpdateAvailable = true, want false")
	}
	if result.Checked {
		t.Fatalf("Checked = true, want false")
	}
	if result.LatestVersion != "" {
		t.Fatalf("LatestVersion = %q, want empty", result.LatestVersion)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("request count = %d, want 0", got)
	}
}

func TestCheckReportsAvailableUpdateFromLatestStableRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("User-Agent"); got != "codex-manage/"+testCurrentVersion {
			t.Fatalf("User-Agent = %q, want %q", got, "codex-manage/"+testCurrentVersion)
		}

		writeResponse(t, w, `{
			"tag_name":"v1.3.0",
			"html_url":"https://github.com/AspireOne/codex-auth-manager/releases/tag/v1.3.0",
			"draft":false,
			"prerelease":false
		}`)
	}))
	defer server.Close()

	restore := overrideLatestReleaseAPIURL(t, server.URL)

	result, err := Check(context.Background(), testCurrentVersion, server.Client())
	restore()

	if err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	if !result.UpdateAvailable {
		t.Fatal("UpdateAvailable = false, want true")
	}
	if !result.Checked {
		t.Fatal("Checked = false, want true")
	}
	if result.CurrentVersion != testCurrentVersion {
		t.Fatalf("CurrentVersion = %q, want %q", result.CurrentVersion, testCurrentVersion)
	}
	if result.LatestVersion != "v1.3.0" {
		t.Fatalf("LatestVersion = %q, want %q", result.LatestVersion, "v1.3.0")
	}
	if result.URL != "https://github.com/AspireOne/codex-auth-manager/releases/tag/v1.3.0" {
		t.Fatalf("URL = %q, want release page URL", result.URL)
	}
}

func TestCheckDoesNotReportWhenCurrentVersionIsLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, `{
			"tag_name":"v1.2.3",
			"html_url":"https://github.com/AspireOne/codex-auth-manager/releases/tag/v1.2.3",
			"draft":false,
			"prerelease":false
		}`)
	}))
	defer server.Close()

	restore := overrideLatestReleaseAPIURL(t, server.URL)

	result, err := Check(context.Background(), testCurrentVersion, server.Client())
	restore()

	if err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	if result.UpdateAvailable {
		t.Fatal("UpdateAvailable = true, want false")
	}
	if !result.Checked {
		t.Fatal("Checked = false, want true")
	}
	if result.LatestVersion != testCurrentVersion {
		t.Fatalf("LatestVersion = %q, want %q", result.LatestVersion, testCurrentVersion)
	}
}

func TestCheckIgnoresDraftAndPrereleaseResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "draft",
			body: `{
				"tag_name":"v1.3.0",
				"html_url":"https://github.com/AspireOne/codex-auth-manager/releases/tag/v1.3.0",
				"draft":true,
				"prerelease":false
			}`,
		},
		{
			name: "prerelease",
			body: `{
				"tag_name":"v1.3.0-beta.1",
				"html_url":"https://github.com/AspireOne/codex-auth-manager/releases/tag/v1.3.0-beta.1",
				"draft":false,
				"prerelease":true
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeResponse(t, w, tt.body)
			}))
			defer server.Close()

			restore := overrideLatestReleaseAPIURL(t, server.URL)

			result, err := Check(context.Background(), testCurrentVersion, server.Client())
			restore()

			if err != nil {
				t.Fatalf("Check() error = %v, want nil", err)
			}
			if result.UpdateAvailable {
				t.Fatal("UpdateAvailable = true, want false")
			}
			if !result.Checked {
				t.Fatal("Checked = false, want true")
			}
			if result.LatestVersion != "" {
				t.Fatalf("LatestVersion = %q, want empty", result.LatestVersion)
			}
		})
	}
}

func TestCheckReturnsErrorForUnexpectedStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	restore := overrideLatestReleaseAPIURL(t, server.URL)

	_, err := Check(context.Background(), testCurrentVersion, server.Client())
	restore()

	if err == nil {
		t.Fatal("Check() error = nil, want non-nil")
	}
}

func TestCheckReturnsErrorForInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, `{"tag_name":`)
	}))
	defer server.Close()

	restore := overrideLatestReleaseAPIURL(t, server.URL)

	_, err := Check(context.Background(), testCurrentVersion, server.Client())
	restore()

	if err == nil {
		t.Fatal("Check() error = nil, want non-nil")
	}
}

func TestCheckReturnsErrorForInvalidReleaseTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, `{
			"tag_name":"release-2026-04-18",
			"html_url":"https://github.com/AspireOne/codex-auth-manager/releases/tag/release-2026-04-18",
			"draft":false,
			"prerelease":false
		}`)
	}))
	defer server.Close()

	restore := overrideLatestReleaseAPIURL(t, server.URL)

	_, err := Check(context.Background(), testCurrentVersion, server.Client())
	restore()

	if err == nil {
		t.Fatal("Check() error = nil, want non-nil")
	}
}

func TestCheckHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		writeResponse(t, w, `{
			"tag_name":"v1.3.0",
			"html_url":"https://github.com/AspireOne/codex-auth-manager/releases/tag/v1.3.0",
			"draft":false,
			"prerelease":false
		}`)
	}))
	defer server.Close()

	restore := overrideLatestReleaseAPIURL(t, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Check(ctx, testCurrentVersion, server.Client())
	restore()

	if err == nil {
		t.Fatal("Check() error = nil, want non-nil")
	}
}

func TestCheckAcceptsBuildMetadataInCurrentVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "codex-manage/"+testCurrentVersion {
			t.Fatalf("User-Agent = %q, want %q", got, "codex-manage/"+testCurrentVersion)
		}

		writeResponse(t, w, `{
			"tag_name":"v1.3.0",
			"html_url":"https://github.com/AspireOne/codex-auth-manager/releases/tag/v1.3.0",
			"draft":false,
			"prerelease":false
		}`)
	}))
	defer server.Close()

	restore := overrideLatestReleaseAPIURL(t, server.URL)

	result, err := Check(context.Background(), "v1.2.3+homebrew.1", server.Client())
	restore()

	if err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	if !result.Checked {
		t.Fatal("Checked = false, want true")
	}
	if result.CurrentVersion != testCurrentVersion {
		t.Fatalf("CurrentVersion = %q, want %q", result.CurrentVersion, testCurrentVersion)
	}
	if !result.UpdateAvailable {
		t.Fatal("UpdateAvailable = false, want true")
	}
}

func writeResponse(t *testing.T, w io.Writer, body string) {
	t.Helper()

	if _, err := io.WriteString(w, body); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func overrideLatestReleaseAPIURL(t *testing.T, url string) func() {
	t.Helper()

	original := latestReleaseAPIURL
	latestReleaseAPIURL = url
	return func() {
		latestReleaseAPIURL = original
	}
}
