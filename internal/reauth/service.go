package reauth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	profilemgr "codex-manage/internal/profiles"
)

const (
	initializeRequestID = 1
	loginRequestID      = 2
	cancelRequestID     = 3
	stderrTailLimit     = 8 * 1024
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

	tempRoot, err := s.tempDir("", "codex-manage-login-*")
	if err != nil {
		return fmt.Errorf("failed to create isolated login directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempRoot) }()
	if err := os.Chmod(tempRoot, 0o700); err != nil { //nolint:gosec // Directory traversal requires execute permission.
		return fmt.Errorf("failed to secure isolated login directory: %w", err)
	}

	tempCodexHome := filepath.Join(tempRoot, "codex")
	tempSQLiteHome := filepath.Join(tempRoot, "sqlite")
	if err := os.MkdirAll(tempCodexHome, 0o700); err != nil {
		return fmt.Errorf("failed to create isolated Codex home: %w", err)
	}
	if err := os.MkdirAll(tempSQLiteHome, 0o700); err != nil {
		return fmt.Errorf("failed to create isolated Codex state directory: %w", err)
	}
	if err := copyOptionalConfig(filepath.Join(filepath.Dir(s.manager.AuthFile), "config.toml"), filepath.Join(tempCodexHome, "config.toml")); err != nil {
		return err
	}

	cmd := s.command(codex, "-c", `cli_auth_credentials_store="file"`, "app-server", "--listen", "stdio://")
	cmd.Env = isolatedEnvironment(os.Environ(), tempCodexHome, tempSQLiteHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to connect to Codex app-server input: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to connect to Codex app-server output: %w", err)
	}
	tail := &tailBuffer{limit: stderrTailLimit}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Codex app-server: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer stopProcess(cmd, exited)

	messages := scanMessages(stdout)
	client := protocolClient{stdin: stdin, messages: messages, exited: exited, stderr: tail}
	if err := client.request(ctx, initializeRequestID, "initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "codex-manage",
			"title":   "Codex Auth Manager",
			"version": "1",
		},
	}, nil); err != nil {
		return err
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		return client.protocolError("failed to initialize Codex app-server", err)
	}

	var started struct {
		Type    string `json:"type"`
		LoginID string `json:"loginId"`
		AuthURL string `json:"authUrl"`
	}
	if err := client.request(ctx, loginRequestID, "account/login/start", map[string]any{"type": "chatgpt"}, &started); err != nil {
		return err
	}
	if started.Type != "chatgpt" || strings.TrimSpace(started.LoginID) == "" {
		return client.protocolError("Codex app-server returned an invalid login response", nil)
	}
	if err := validateAuthURL(started.AuthURL); err != nil {
		_ = client.cancelLogin(started.LoginID)
		return err
	}
	if err := s.browser.Open(profile, started.AuthURL); err != nil {
		_ = client.cancelLogin(started.LoginID)
		return fmt.Errorf("failed to open authentication browser: %w", err)
	}

	if err := client.waitForLogin(ctx, started.LoginID); err != nil {
		return err
	}
	tempAuth := filepath.Join(tempCodexHome, "auth.json")
	if _, err := os.Stat(tempAuth); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("codex reported a successful login but did not write auth.json")
		}
		return fmt.Errorf("failed to read isolated login result: %w", err)
	}
	if err := s.manager.ReplaceAndActivate(profile.Key, tempAuth); err != nil {
		return fmt.Errorf("failed to install refreshed credentials: %w", err)
	}
	return nil
}

type protocolMessage struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *protocolError  `json:"error,omitempty"`
}

type protocolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type scannedMessage struct {
	message protocolMessage
	err     error
}

type protocolClient struct {
	stdin    io.Writer
	messages <-chan scannedMessage
	exited   <-chan error
	stderr   *tailBuffer
}

func (c protocolClient) request(ctx context.Context, id int, method string, params, result any) error {
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return c.protocolError("failed to send request to Codex app-server", err)
	}
	for {
		message, err := c.next(ctx)
		if err != nil {
			return err
		}
		if message.ID == nil || *message.ID != id {
			continue
		}
		if message.Error != nil {
			return c.protocolError(fmt.Sprintf("Codex app-server rejected %s: %s", method, message.Error.Message), nil)
		}
		if result != nil {
			if len(message.Result) == 0 {
				return c.protocolError("Codex app-server returned an empty response", nil)
			}
			if err := json.Unmarshal(message.Result, result); err != nil {
				return c.protocolError("Codex app-server returned an invalid response", err)
			}
		}
		return nil
	}
}

func (c protocolClient) notify(method string, params any) error {
	return c.write(map[string]any{"method": method, "params": params})
}

func (c protocolClient) waitForLogin(ctx context.Context, loginID string) error {
	for {
		message, err := c.next(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				_ = c.cancelLogin(loginID)
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
			return c.protocolError("Codex app-server returned an invalid login notification", err)
		}
		if completed.LoginID != loginID {
			continue
		}
		if !completed.Success {
			message := "authentication did not complete"
			if completed.Error != nil && strings.TrimSpace(*completed.Error) != "" {
				message += ": " + strings.TrimSpace(*completed.Error)
			}
			return errors.New(message)
		}
		return nil
	}
}

func (c protocolClient) cancelLogin(loginID string) error {
	return c.write(map[string]any{
		"id":     cancelRequestID,
		"method": "account/login/cancel",
		"params": map[string]string{"loginId": loginID},
	})
}

func (c protocolClient) write(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

func (c protocolClient) next(ctx context.Context) (protocolMessage, error) {
	select {
	case <-ctx.Done():
		return protocolMessage{}, ctx.Err()
	case err := <-c.exited:
		return protocolMessage{}, c.protocolError("Codex app-server exited before authentication completed", err)
	case scanned, ok := <-c.messages:
		if !ok {
			return protocolMessage{}, c.protocolError("Codex app-server closed its output before authentication completed", nil)
		}
		if scanned.err != nil {
			return protocolMessage{}, c.protocolError("Codex app-server returned malformed protocol data", scanned.err)
		}
		return scanned.message, nil
	}
}

func (c protocolClient) protocolError(message string, cause error) error {
	if cause != nil && !errors.Is(cause, os.ErrProcessDone) {
		message += ": " + cause.Error()
	}
	if diagnostics := strings.TrimSpace(c.stderr.String()); diagnostics != "" {
		message += "; app-server: " + diagnostics
	}
	return fmt.Errorf("%s; update the Codex CLI if it does not support the app-server login API", message)
}

func scanMessages(reader io.Reader) <-chan scannedMessage {
	messages := make(chan scannedMessage)
	go func() {
		defer close(messages)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			var message protocolMessage
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				messages <- scannedMessage{err: err}
				return
			}
			messages <- scannedMessage{message: message}
		}
		if err := scanner.Err(); err != nil {
			messages <- scannedMessage{err: err}
		}
	}()
	return messages
}

func validateAuthURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("codex app-server returned an invalid authentication URL")
	}
	return nil
}

func isolatedEnvironment(environment []string, codexHome, sqliteHome string) []string {
	blocked := map[string]struct{}{
		"CODEX_HOME": {}, "CODEX_SQLITE_HOME": {}, "CODEX_ACCESS_TOKEN": {},
		"CODEX_API_KEY": {}, "OPENAI_API_KEY": {},
	}
	result := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, remove := blocked[strings.ToUpper(name)]; !remove {
			result = append(result, entry)
		}
	}
	return append(result, "CODEX_HOME="+codexHome, "CODEX_SQLITE_HOME="+sqliteHome)
}

func copyOptionalConfig(source, destination string) error {
	data, err := os.ReadFile(source) // #nosec G304 -- source is the configured Codex home.
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read Codex config for isolated login: %w", err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return fmt.Errorf("failed to copy Codex config for isolated login: %w", err)
	}
	return nil
}

func stopProcess(cmd *exec.Cmd, exited <-chan error) {
	select {
	case <-exited:
		return
	default:
	}
	_ = cmd.Process.Kill()
	select {
	case <-exited:
	case <-time.After(time.Second):
	}
}

type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.TrimSpace(b.data))
}
