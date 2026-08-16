package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const stderrTailLimit = 8 * 1024

type Seed struct {
	Prefix         string
	AuthJSON       []byte
	ConfigTOML     []byte
	InstallationID string
}

type Launcher struct {
	Command func(string, ...string) *exec.Cmd
	TempDir func(string, string) (string, error)
}

func DefaultLauncher() Launcher {
	return Launcher{Command: exec.Command, TempDir: os.MkdirTemp} // #nosec G204 -- executable is resolved by the caller; arguments are fixed.
}

type Session struct {
	Home     string
	stdin    io.WriteCloser
	messages <-chan scannedMessage
	exited   <-chan error
	stderr   *tailBuffer
	cmd      *exec.Cmd
	root     string
	nextID   int
	close    sync.Once
}

type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string { return e.Message }

type protocolMessage struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type scannedMessage struct {
	message protocolMessage
	err     error
}

func (l Launcher) Start(ctx context.Context, executable string, seed Seed) (*Session, error) {
	if l.Command == nil {
		l.Command = exec.Command // #nosec G204 -- executable is resolved by the caller; arguments are fixed.
	}
	if l.TempDir == nil {
		l.TempDir = os.MkdirTemp
	}
	prefix := seed.Prefix
	if prefix == "" {
		prefix = "codex-manage-*"
	}
	root, err := l.TempDir("", prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to create isolated Codex directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // Traversable private directory.
		cleanup()
		return nil, fmt.Errorf("failed to secure isolated Codex directory: %w", err)
	}
	home := filepath.Join(root, "codex")
	sqlite := filepath.Join(root, "sqlite")
	if err := os.MkdirAll(home, 0o700); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.MkdirAll(sqlite, 0o700); err != nil {
		cleanup()
		return nil, err
	}
	for name, data := range map[string][]byte{"auth.json": seed.AuthJSON, "config.toml": seed.ConfigTOML} {
		if len(data) > 0 {
			if err := os.WriteFile(filepath.Join(home, name), data, 0o600); err != nil {
				cleanup()
				return nil, fmt.Errorf("failed to seed isolated Codex %s: %w", name, err)
			}
		}
	}
	if seed.InstallationID != "" {
		if err := os.WriteFile(filepath.Join(home, "installation_id"), []byte(seed.InstallationID+"\n"), 0o600); err != nil {
			cleanup()
			return nil, fmt.Errorf("failed to seed isolated Codex installation ID: %w", err)
		}
	}

	cmd := l.Command(executable, "-c", `cli_auth_credentials_store="file"`, "app-server", "--listen", "stdio://")
	cmd.Env = IsolatedEnvironment(os.Environ(), home, sqlite)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	tail := &tailBuffer{limit: stderrTailLimit}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to start Codex app-server: %w", err)
	}
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		close(exited)
	}()
	s := &Session{Home: home, stdin: stdin, messages: scanMessages(stdout), exited: exited, stderr: tail, cmd: cmd, root: root, nextID: 1}
	if err := s.Request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "codex-manage", "title": "Codex Auth Manager", "version": "1"}}, nil); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.Notify("initialized", map[string]any{}); err != nil {
		s.Close()
		return nil, s.protocolError("failed to initialize Codex app-server", err)
	}
	return s, nil
}

func (s *Session) Request(ctx context.Context, method string, params, result any) error {
	id := s.nextID
	s.nextID++
	if err := s.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return s.protocolError("failed to send request to Codex app-server", err)
	}
	for {
		message, err := s.next(ctx)
		if err != nil {
			return err
		}
		if message.ID == nil || *message.ID != id {
			continue
		}
		if message.Error != nil {
			return &RPCError{Code: message.Error.Code, Message: strings.TrimSpace(message.Error.Message)}
		}
		if result != nil {
			if len(message.Result) == 0 || json.Unmarshal(message.Result, result) != nil {
				return s.protocolError("Codex app-server returned an invalid response", nil)
			}
		}
		return nil
	}
}

type Message struct {
	ID     *int
	Method string
	Params json.RawMessage
}

func (s *Session) Next(ctx context.Context) (Message, error) {
	message, err := s.next(ctx)
	if err != nil {
		return Message{}, err
	}
	return Message{ID: message.ID, Method: message.Method, Params: message.Params}, nil
}

func (s *Session) next(ctx context.Context) (protocolMessage, error) {
	select {
	case <-ctx.Done():
		return protocolMessage{}, ctx.Err()
	case err := <-s.exited:
		return protocolMessage{}, s.protocolError("Codex app-server exited unexpectedly", err)
	case scanned, ok := <-s.messages:
		if !ok {
			return protocolMessage{}, s.protocolError("Codex app-server closed its output", nil)
		}
		if scanned.err != nil {
			return protocolMessage{}, s.protocolError("Codex app-server returned malformed protocol data", scanned.err)
		}
		return scanned.message, nil
	}
}

func (s *Session) Notify(method string, params any) error {
	return s.write(map[string]any{"method": method, "params": params})
}

// SendRequest sends a best-effort request whose response the caller does not
// need, such as cancellation immediately before process shutdown.
func (s *Session) SendRequest(method string, params any) error {
	id := s.nextID
	s.nextID++
	return s.write(map[string]any{"id": id, "method": method, "params": params})
}

func (s *Session) ReadAuth() ([]byte, error) {
	return os.ReadFile(filepath.Join(s.Home, "auth.json")) // #nosec G304 -- isolated managed path.
}

func (s *Session) Close() {
	s.close.Do(func() {
		_ = s.stdin.Close()
		select {
		case <-s.exited:
		default:
			_ = s.cmd.Process.Kill()
			select {
			case <-s.exited:
			case <-time.After(time.Second):
			}
		}
		_ = os.RemoveAll(s.root)
	})
}

func (s *Session) write(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(data, '\n'))
	return err
}

func (s *Session) protocolError(message string, cause error) error {
	if cause != nil && !errors.Is(cause, os.ErrProcessDone) {
		message += ": " + cause.Error()
	}
	if diagnostics := strings.TrimSpace(s.stderr.String()); diagnostics != "" {
		message += "; app-server: " + diagnostics
	}
	return errors.New(message + "; update the Codex CLI if app-server is incompatible")
}

func IsolatedEnvironment(environment []string, codexHome, sqliteHome string) []string {
	blocked := map[string]struct{}{"CODEX_HOME": {}, "CODEX_SQLITE_HOME": {}, "CODEX_ACCESS_TOKEN": {}, "CODEX_API_KEY": {}, "OPENAI_API_KEY": {}}
	result := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, remove := blocked[strings.ToUpper(name)]; !remove {
			result = append(result, entry)
		}
	}
	return append(result, "CODEX_HOME="+codexHome, "CODEX_SQLITE_HOME="+sqliteHome)
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

type tailBuffer struct {
	mu    sync.Mutex
	b     []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.b = append(b.b, p...)
	if len(b.b) > b.limit {
		b.b = append([]byte(nil), b.b[len(b.b)-b.limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.TrimSpace(b.b))
}
