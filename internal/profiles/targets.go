package profiles

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// AuthCredentials is the source-neutral credential material exposed to auth
// targets. The profile manager remains the source of truth; targets project
// this material into another tool's storage format.
type AuthCredentials struct {
	Kind         AuthKind
	AccountID    string
	APIKey       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// AuthTarget projects the active Codex credentials into another tool.
// Implementations own the target file format and persistence semantics.
type AuthTarget interface {
	Name() string
	Sync(AuthCredentials) error
	Clear() error
}

var ErrTargetSync = errors.New("auth target synchronization failed")

func syncAuthTargets(targets []AuthTarget, credentials *AuthCredentials) error {
	if len(targets) == 0 {
		return nil
	}

	errs := make([]error, 0)
	for _, target := range targets {
		if target == nil {
			continue
		}

		var err error
		if credentials == nil {
			err = target.Clear()
		} else {
			err = target.Sync(*credentials)
		}
		if err != nil {
			name := strings.TrimSpace(target.Name())
			if name == "" {
				name = "unnamed auth target"
			}
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrTargetSync, errors.Join(errs...))
}
