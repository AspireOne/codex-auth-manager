package reauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codex-manage/internal/codexapp"
	profilemgr "codex-manage/internal/profiles"
)

// Authenticator refreshes one existing ChatGPT profile.
type Authenticator interface {
	Reauthenticate(context.Context, profilemgr.ProfileSummary) error
}

type browserOpener interface {
	Open(profilemgr.ProfileSummary, string) error
}

// Service coordinates an isolated Codex login and installs the result only
// after the returned account identity has been verified by the profile manager.
type Service struct {
	manager profilemgr.Manager
	browser browserOpener

	lookPath func(string) (string, error)
	command  func(string, ...string) *exec.Cmd
	tempDir  func(string, string) (string, error)
}

func New(manager profilemgr.Manager) *Service {
	return &Service{
		manager:  manager,
		browser:  newBrowserLauncher(),
		lookPath: exec.LookPath,
		command:  exec.Command, // #nosec G204 -- executable is resolved from PATH; arguments are fixed protocol values.
		tempDir:  os.MkdirTemp,
	}
}

func (s *Service) Reauthenticate(ctx context.Context, profile profilemgr.ProfileSummary) error {
	if profile.Kind != profilemgr.AuthKindChatGPT {
		return errors.New("only ChatGPT profiles can be re-authenticated")
	}
	if strings.TrimSpace(profile.Key) == "" {
		return errors.New("missing profile key")
	}

	codex, err := s.lookPath("codex")
	if err != nil {
		return errors.New("codex CLI was not found on PATH")
	}

	config, err := os.ReadFile(filepath.Join(filepath.Dir(s.manager.AuthFile), "config.toml")) // #nosec G304 -- configured Codex home.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to read Codex config for isolated login: %w", err)
	}
	launcher := codexapp.Launcher{Command: s.command, TempDir: s.tempDir}
	client, err := launcher.Start(ctx, codex, codexapp.Seed{Prefix: "codex-manage-login-*", ConfigTOML: config})
	if err != nil {
		return err
	}
	defer client.Close()

	var started struct {
		Type    string `json:"type"`
		LoginID string `json:"loginId"`
		AuthURL string `json:"authUrl"`
	}
	if err := client.Request(ctx, "account/login/start", map[string]any{"type": "chatgpt"}, &started); err != nil {
		return err
	}
	if started.Type != "chatgpt" || strings.TrimSpace(started.LoginID) == "" {
		return errors.New("codex app-server returned an invalid login response")
	}
	if err := validateAuthURL(started.AuthURL); err != nil {
		_ = cancelLogin(client, started.LoginID)
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = cancelLogin(client, started.LoginID)
		return fmt.Errorf("authentication cancelled: %w", err)
	}
	if err := s.browser.Open(profile, started.AuthURL); err != nil {
		_ = cancelLogin(client, started.LoginID)
		return fmt.Errorf("failed to open authentication browser: %w", err)
	}

	if err := waitForLogin(ctx, client, started.LoginID); err != nil {
		return err
	}
	tempAuth := filepath.Join(client.Home, "auth.json")
	if _, err := os.Stat(tempAuth); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("codex reported a successful login but did not write auth.json")
		}
		return fmt.Errorf("failed to read isolated login result: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("authentication cancelled: %w", err)
	}
	if err := s.manager.ReplaceAndActivate(profile.Key, tempAuth); err != nil {
		return fmt.Errorf("failed to install refreshed credentials: %w", err)
	}
	return nil
}

func waitForLogin(ctx context.Context, client *codexapp.Session, loginID string) error {
	for {
		message, err := client.Next(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				_ = cancelLogin(client, loginID)
				return fmt.Errorf("authentication cancelled: %w", err)
			}
			return err
		}
		if message.Method != "account/login/completed" {
			continue
		}
		var completed struct {
			LoginID string  `json:"loginId"`
			Success bool    `json:"success"`
			Error   *string `json:"error"`
		}
		if err := json.Unmarshal(message.Params, &completed); err != nil {
			return fmt.Errorf("codex app-server returned an invalid login notification: %w", err)
		}
		if completed.LoginID != loginID {
			continue
		}
		if !completed.Success {
			text := "authentication did not complete"
			if completed.Error != nil && strings.TrimSpace(*completed.Error) != "" {
				text += ": " + strings.TrimSpace(*completed.Error)
			}
			return errors.New(text)
		}
		return nil
	}
}

func cancelLogin(client *codexapp.Session, loginID string) error {
	return client.SendRequest("account/login/cancel", map[string]string{"loginId": loginID})
}

func validateAuthURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("codex app-server returned an invalid authentication URL")
	}
	return nil
}
