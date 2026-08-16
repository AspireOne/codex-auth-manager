package profiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const profileStatusCacheVersion = 1

type ProfileAuthStatus string

const (
	ProfileAuthAuthenticated  ProfileAuthStatus = "authenticated"
	ProfileAuthSignInRequired ProfileAuthStatus = "sign_in_required"
)

type ProfileStatus struct {
	FetchedAt   time.Time         `json:"fetched_at"`
	AuthStatus  ProfileAuthStatus `json:"auth_status"`
	UsedPercent *int              `json:"used_percent,omitempty"`
	ResetsAt    *time.Time        `json:"resets_at,omitempty"`
}

type profileStatusCache struct {
	Version  int                      `json:"version"`
	Profiles map[string]ProfileStatus `json:"profiles"`
}

type StatusQuerySource struct {
	AuthJSON       []byte
	ConfigTOML     []byte
	InstallationID string
}

func (m Manager) LoadProfileStatuses(valid map[string]AuthKind, now time.Time) (map[string]ProfileStatus, error) {
	cache, err := m.readProfileStatusCache()
	if err != nil {
		return nil, err
	}
	changed := false
	for key, status := range cache.Profiles {
		kind, exists := valid[key]
		if !exists || kind != AuthKindChatGPT || status.FetchedAt.After(now) || !validCachedStatus(status) {
			delete(cache.Profiles, key)
			changed = true
		}
	}
	if changed {
		if err := m.writeProfileStatusCache(cache); err != nil {
			return nil, err
		}
	}
	return cache.Profiles, nil
}

func (m Manager) SaveProfileStatus(key string, status ProfileStatus) error {
	if strings.TrimSpace(key) == "" || !validCachedStatus(status) {
		return errors.New("invalid profile status")
	}
	status.FetchedAt = status.FetchedAt.UTC()
	if status.ResetsAt != nil {
		reset := status.ResetsAt.UTC()
		status.ResetsAt = &reset
	}
	cache, err := m.readProfileStatusCache()
	if err != nil {
		return err
	}
	cache.Profiles[key] = status
	return m.writeProfileStatusCache(cache)
}

func (m Manager) InvalidateProfileStatus(key string) error {
	cache, err := m.readProfileStatusCache()
	if err != nil {
		return err
	}
	if _, ok := cache.Profiles[key]; !ok {
		return nil
	}
	delete(cache.Profiles, key)
	return m.writeProfileStatusCache(cache)
}

func (m Manager) StatusQuerySource(key string) (StatusQuerySource, error) {
	path := filepath.Join(m.ProfileDir, key)
	descriptor, err := readAuthDescriptor(path)
	if err != nil {
		return StatusQuerySource{}, fmt.Errorf("failed to read profile %q: %w", key, err)
	}
	if descriptor.Kind != AuthKindChatGPT {
		return StatusQuerySource{}, errors.New("status is available only for ChatGPT profiles")
	}
	auth, err := os.ReadFile(path) // #nosec G304 -- path is a managed profile path.
	if err != nil {
		return StatusQuerySource{}, err
	}
	config, err := os.ReadFile(filepath.Join(filepath.Dir(m.AuthFile), "config.toml")) // #nosec G304 -- configured Codex home.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return StatusQuerySource{}, fmt.Errorf("failed to read Codex config: %w", err)
	}
	id, err := m.ensureProfileInstallationID(key)
	if err != nil {
		return StatusQuerySource{}, err
	}
	return StatusQuerySource{AuthJSON: auth, ConfigTOML: config, InstallationID: id}, nil
}

func (m Manager) ReconcileStatusCredentials(key string, original, refreshed []byte) error {
	originalDescriptor, err := readAuthDescriptorBytes(original)
	if err != nil {
		return err
	}
	refreshedDescriptor, err := readAuthDescriptorBytes(refreshed)
	if err != nil {
		return fmt.Errorf("refreshed credentials are invalid: %w", err)
	}
	if originalDescriptor.Kind != AuthKindChatGPT || refreshedDescriptor.Kind != AuthKindChatGPT ||
		!originalDescriptor.Identity.matches(refreshedDescriptor.Identity) {
		return errors.New("codex refreshed credentials for a different account")
	}
	profilePath := filepath.Join(m.ProfileDir, key)
	current, err := os.ReadFile(profilePath) // #nosec G304 -- path is a managed profile path.
	if err != nil {
		return fmt.Errorf("profile changed while status was loading: %w", err)
	}
	if !bytes.Equal(current, original) {
		return errors.New("profile changed while status was loading")
	}
	if bytes.Equal(original, refreshed) {
		return nil
	}

	marker, err := readCurrentProfileMarker(m.CurrentProfileFile, m.ProfileDir)
	if err != nil {
		return err
	}
	updateActive := marker.Name == key
	if updateActive {
		active, err := os.ReadFile(m.AuthFile) // #nosec G304 -- configured auth path.
		if err != nil || !bytes.Equal(active, original) {
			return errors.New("active credentials changed while status was loading")
		}
	}
	if err := writeFileAtomically(profilePath, refreshed, 0o600); err != nil {
		return fmt.Errorf("failed to store refreshed credentials: %w", err)
	}
	if updateActive {
		if err := writeFileAtomically(m.AuthFile, refreshed, 0o600); err != nil {
			return fmt.Errorf("%w: refreshed saved credentials, but failed to update active credentials: %v", ErrStateChanged, err)
		}
	}
	return nil
}

func (m Manager) readProfileStatusCache() (profileStatusCache, error) {
	empty := profileStatusCache{Version: profileStatusCacheVersion, Profiles: map[string]ProfileStatus{}}
	data, err := os.ReadFile(m.StatusCacheFile) // #nosec G304 -- configured manager path.
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("failed to read profile status cache: %w", err)
	}
	var cache profileStatusCache
	if json.Unmarshal(data, &cache) != nil || cache.Version != profileStatusCacheVersion || cache.Profiles == nil {
		return empty, nil
	}
	return cache, nil
}

func (m Manager) writeProfileStatusCache(cache profileStatusCache) error {
	if len(cache.Profiles) == 0 {
		if err := os.Remove(m.StatusCacheFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to clear profile status cache: %w", err)
		}
		return nil
	}
	cache.Version = profileStatusCacheVersion
	body, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomically(m.StatusCacheFile, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("failed to write profile status cache: %w", err)
	}
	return nil
}

func validCachedStatus(status ProfileStatus) bool {
	if status.FetchedAt.IsZero() {
		return false
	}
	if status.AuthStatus == ProfileAuthSignInRequired {
		return status.UsedPercent == nil && status.ResetsAt == nil
	}
	return status.AuthStatus == ProfileAuthAuthenticated && status.UsedPercent != nil &&
		*status.UsedPercent >= 0 && *status.UsedPercent <= 100 && status.ResetsAt != nil
}

func readAuthDescriptorBytes(data []byte) (authDescriptor, error) {
	var auth authFileData
	if err := json.Unmarshal(data, &auth); err != nil {
		return authDescriptor{}, err
	}
	identity := authIdentity{AuthMode: strings.TrimSpace(auth.AuthMode), AccountID: strings.TrimSpace(auth.Tokens.AccountID)}
	if identity.AccountID == "" {
		return authDescriptor{}, errNoUsableIdentity
	}
	return authDescriptor{Identity: identity, Kind: AuthKindChatGPT, Email: emailFromIDToken(auth.Tokens.IDToken)}, nil
}
