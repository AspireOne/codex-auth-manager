package authsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	profilemgr "codex-manage/internal/profiles"
)

const (
	piProviderKey       = "openai-codex"
	piAuthFileName      = "auth.json"
	piAuthLockSuffix    = ".lock"
	piLockStale         = 30 * time.Second
	piLockWait          = 5 * time.Second
	piLockRetryInterval = 20 * time.Millisecond
)

// PiTarget projects the active ChatGPT OAuth credential into Pi's provider-
// keyed auth file. It owns only the openai-codex entry.
type PiTarget struct {
	authPath string
}

// NewPiTarget uses PI_CODING_AGENT_DIR when set, otherwise Pi's default agent
// directory under home.
func NewPiTarget(home string) *PiTarget {
	agentDir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
	if agentDir == "" {
		agentDir = defaultPiAgentDir(home)
	} else {
		agentDir = expandHome(agentDir, home)
	}
	return NewPiTargetAtPath(filepath.Join(agentDir, piAuthFileName))
}

// NewPiTargetAtPath constructs a target for a specific Pi auth file. It is
// useful for tests and for callers with a non-standard Pi installation.
func NewPiTargetAtPath(path string) *PiTarget {
	path = strings.TrimSpace(path)
	if path == "" {
		return &PiTarget{}
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return &PiTarget{authPath: filepath.Clean(path)}
}

func (t *PiTarget) Name() string {
	return "Pi Agent"
}

// Sync updates Pi's OpenAI Codex credential. API-key profiles deliberately
// clear the OAuth projection; API-key projection can be added independently
// if a target needs it in the future.
func (t *PiTarget) Sync(credentials profilemgr.AuthCredentials) error {
	return t.mutate(func(entries map[string]json.RawMessage) (bool, error) {
		if credentials.Kind != profilemgr.AuthKindChatGPT {
			if _, exists := entries[piProviderKey]; !exists {
				return false, nil
			}
			delete(entries, piProviderKey)
			return true, nil
		}
		if err := validateChatGPTCredentials(credentials); err != nil {
			return false, err
		}

		entry := map[string]json.RawMessage{}
		if existing, ok := entries[piProviderKey]; ok {
			var decoded map[string]json.RawMessage
			if json.Unmarshal(existing, &decoded) == nil && decoded != nil {
				entry = decoded
			}
		}
		changed := setRawString(entry, "type", "oauth")
		changed = setRawString(entry, "access", credentials.AccessToken) || changed
		changed = setRawString(entry, "refresh", credentials.RefreshToken) || changed
		changed = setRawInt64(entry, "expires", credentials.ExpiresAt.UnixMilli()) || changed
		changed = setRawString(entry, "accountId", credentials.AccountID) || changed
		if !changed {
			return false, nil
		}

		encoded, err := json.Marshal(entry)
		if err != nil {
			return false, fmt.Errorf("failed to encode Pi OpenAI Codex credentials: %w", err)
		}
		entries[piProviderKey] = encoded
		return true, nil
	})
}

// Clear removes only the credential managed by this target and leaves all
// other Pi providers untouched.
func (t *PiTarget) Clear() error {
	return t.mutate(func(entries map[string]json.RawMessage) (bool, error) {
		if _, exists := entries[piProviderKey]; !exists {
			return false, nil
		}
		delete(entries, piProviderKey)
		return true, nil
	})
}

func (t *PiTarget) mutate(mutator func(map[string]json.RawMessage) (bool, error)) error {
	if t == nil || strings.TrimSpace(t.authPath) == "" {
		return errors.New("auth target has no Pi auth file path")
	}

	present, err := targetDirectoryPresent(filepath.Dir(t.authPath))
	if err != nil {
		return err
	}
	if !present {
		return nil
	}

	return withPiAuthLock(t.authPath, func() error {
		entries, err := readPiAuthFile(t.authPath)
		if err != nil {
			return err
		}
		changed, err := mutator(entries)
		if err != nil {
			return err
		}
		if !changed {
			return securePiAuthFile(t.authPath)
		}
		return writePiAuthFile(t.authPath, entries)
	})
}

func validateChatGPTCredentials(credentials profilemgr.AuthCredentials) error {
	if strings.TrimSpace(credentials.AccountID) == "" ||
		strings.TrimSpace(credentials.AccessToken) == "" ||
		strings.TrimSpace(credentials.RefreshToken) == "" ||
		credentials.ExpiresAt.IsZero() || credentials.ExpiresAt.UnixMilli() <= 0 {
		return errors.New("ChatGPT auth is missing account or OAuth token data")
	}
	return nil
}

func targetDirectoryPresent(path string) (bool, error) {
	info, err := os.Stat(path) // #nosec G304 -- path is configured by the user or derived from home.
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to inspect Pi auth directory: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("auth path parent for Pi is not a directory: %s", path)
	}
	return true, nil
}

func defaultPiAgentDir(home string) string {
	standard := filepath.Join(home, ".pi", "agent")
	if directoryExists(standard) {
		return standard
	}
	legacy := filepath.Join(home, ".pi", "agents")
	if directoryExists(legacy) {
		return legacy
	}
	return standard
}

func directoryExists(path string) bool {
	info, err := os.Stat(path) // #nosec G304 -- path is derived from the user's home directory.
	if err != nil || info == nil {
		return false
	}
	return info.IsDir()
}

func readPiAuthFile(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is configured by the user or derived from home.
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read Pi auth file: %w", err)
	}

	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("auth file for Pi contains invalid JSON: %w", err)
	}
	if entries == nil {
		return nil, errors.New("auth file for Pi must contain a JSON object")
	}
	return entries, nil
}

func writePiAuthFile(path string, entries map[string]json.RawMessage) error {
	body, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode Pi auth file: %w", err)
	}
	return writeFileAtomically(path, append(body, '\n'), 0o600)
}

func securePiAuthFile(path string) error {
	info, err := os.Stat(path) // #nosec G304 -- path is configured by the user or derived from home.
	if errors.Is(err, os.ErrNotExist) || err != nil || !info.Mode().IsRegular() {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("failed to secure Pi auth file: %w", err)
	}
	return nil
}

func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	out, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary Pi auth file: %w", err)
	}
	tmp := out.Name()
	cleanup := func() {
		_ = out.Close()
		_ = os.Remove(tmp)
	}

	if err := out.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("failed to secure temporary Pi auth file: %w", err)
	}
	if _, err := out.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("failed to write temporary Pi auth file: %w", err)
	}
	if err := out.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("failed to flush temporary Pi auth file: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to close temporary Pi auth file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to replace Pi auth file: %w", err)
	}
	return nil
}

func withPiAuthLock(path string, operation func() error) error {
	lockPath := path + piAuthLockSuffix
	deadline := time.Now().Add(piLockWait)
	for {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("failed to acquire Pi auth lock: %w", err)
		}

		info, statErr := os.Stat(lockPath) // #nosec G304 -- lock path is derived from configured auth path.
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("failed to inspect Pi auth lock: %w", statErr)
		}
		if info.IsDir() && time.Since(info.ModTime()) >= piLockStale {
			if removeErr := os.Remove(lockPath); removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
				continue
			}
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for Pi auth lock")
		}
		time.Sleep(piLockRetryInterval)
	}

	operationErr := operation()
	releaseErr := os.Remove(lockPath)
	if releaseErr != nil && !errors.Is(releaseErr, os.ErrNotExist) {
		releaseErr = fmt.Errorf("failed to release Pi auth lock: %w", releaseErr)
	} else {
		releaseErr = nil
	}
	if operationErr != nil && releaseErr != nil {
		return errors.Join(operationErr, releaseErr)
	}
	if operationErr != nil {
		return operationErr
	}
	return releaseErr
}

func setRawString(values map[string]json.RawMessage, key, value string) bool {
	encoded, _ := json.Marshal(value)
	if bytes.Equal(values[key], encoded) {
		return false
	}
	values[key] = encoded
	return true
}

func setRawInt64(values map[string]json.RawMessage, key string, value int64) bool {
	encoded, _ := json.Marshal(value)
	if bytes.Equal(values[key], encoded) {
		return false
	}
	values[key] = encoded
	return true
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	prefix := "~" + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return filepath.Join(home, strings.TrimPrefix(path, prefix))
	}
	if filepath.Separator != '/' && strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}
