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

// Version 4 adds the server-reported account plan type. PlanFetchedAt is an
// additive field; v4 entries without it fall back to FetchedAt.
const profileStatusCacheVersion = 4

const profilePlanCacheTTL = 14 * 24 * time.Hour

type ProfileAuthStatus string

const (
	ProfileAuthAuthenticated  ProfileAuthStatus = "authenticated"
	ProfileAuthSignInRequired ProfileAuthStatus = "sign_in_required"
)

type ProfileQuota struct {
	WindowDurationMins int64     `json:"window_duration_mins,omitempty"`
	Monthly            bool      `json:"monthly,omitempty"`
	RemainingPercent   int       `json:"remaining_percent"`
	ResetsAt           time.Time `json:"resets_at"`
}

type ProfileStatus struct {
	FetchedAt  time.Time         `json:"fetched_at"`
	AuthStatus ProfileAuthStatus `json:"auth_status"`
	// PlanType is optional because the app-server may omit it.
	PlanType      Plan           `json:"plan_type,omitempty"`
	PlanFetchedAt *time.Time     `json:"plan_fetched_at,omitempty"`
	Quotas        []ProfileQuota `json:"quotas,omitempty"`
	// UsedPercent and ResetsAt are retained as the legacy nearest-window view
	// for callers that have not migrated to Quotas yet.
	UsedPercent *int       `json:"used_percent,omitempty"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
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
		// Status is auxiliary; an unreadable cache must not block the TUI.
		return map[string]ProfileStatus{}, nil
	}
	changed := false
	for key, status := range cache.Profiles {
		kind, exists := valid[key]
		if !exists || kind != AuthKindChatGPT || status.FetchedAt.After(now) || !validCachedStatus(status) {
			delete(cache.Profiles, key)
			changed = true
			continue
		}
		if status.AuthStatus == ProfileAuthAuthenticated && !status.HasFreshPlan(now) && status.PlanType != PlanUnknown {
			status.PlanType = PlanUnknown
			status.PlanFetchedAt = nil
			cache.Profiles[key] = status
			changed = true
		}
	}
	if changed {
		// Pruning is best effort. Later status writes remain strict and surface
		// as per-profile failure states when persistence is unavailable.
		_ = m.writeProfileStatusCache(cache)
	}
	return cache.Profiles, nil
}

func (m Manager) SaveProfileStatus(key string, status ProfileStatus) error {
	if strings.TrimSpace(key) == "" || !validCachedStatus(status) {
		return errors.New("invalid profile status")
	}
	status.FetchedAt = status.FetchedAt.UTC()
	if status.PlanFetchedAt != nil {
		planFetchedAt := status.PlanFetchedAt.UTC()
		status.PlanFetchedAt = &planFetchedAt
	}
	if strings.TrimSpace(string(status.PlanType)) != "" && status.PlanFetchedAt == nil {
		planFetchedAt := status.FetchedAt
		status.PlanFetchedAt = &planFetchedAt
	}
	if status.ResetsAt != nil {
		reset := status.ResetsAt.UTC()
		status.ResetsAt = &reset
	}
	for i := range status.Quotas {
		status.Quotas[i].ResetsAt = status.Quotas[i].ResetsAt.UTC()
	}
	cache, err := m.readProfileStatusCache()
	if err != nil {
		return err
	}
	if status.AuthStatus == ProfileAuthAuthenticated && status.PlanType != PlanUnknown && !status.HasFreshPlan(status.FetchedAt) {
		status.PlanType = PlanUnknown
		status.PlanFetchedAt = nil
	}
	if status.AuthStatus == ProfileAuthAuthenticated && status.PlanType == PlanUnknown {
		if previous, ok := cache.Profiles[key]; ok && previous.HasFreshPlan(status.FetchedAt) {
			status.PlanType = previous.PlanType
			planFetchedAt := previous.effectivePlanFetchedAt()
			status.PlanFetchedAt = &planFetchedAt
		}
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
		if err := m.syncCurrentAuthTargetsAfterStateChange(); err != nil {
			return fmt.Errorf("%w: refreshed saved credentials and active credentials, but failed to synchronize auth targets: %w", ErrStateChanged, err)
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
		return status.PlanType == PlanUnknown && status.PlanFetchedAt == nil && status.UsedPercent == nil && status.ResetsAt == nil && len(status.Quotas) == 0
	}
	if status.AuthStatus != ProfileAuthAuthenticated {
		return false
	}
	if status.PlanType == PlanUnknown && status.PlanFetchedAt != nil {
		return false
	}
	if len(status.Quotas) > 0 {
		for _, quota := range status.Quotas {
			if !validProfileQuota(quota) {
				return false
			}
		}
		return true
	}
	return status.UsedPercent != nil && *status.UsedPercent >= 0 && *status.UsedPercent <= 100 && status.ResetsAt != nil
}

// HasFreshPlan reports whether the cached plan is still within its two-week
// retention window. Older cache entries use the full status timestamp.
func (status ProfileStatus) HasFreshPlan(now time.Time) bool {
	if status.AuthStatus != ProfileAuthAuthenticated || strings.TrimSpace(string(status.PlanType)) == "" {
		return false
	}
	fetchedAt := status.effectivePlanFetchedAt()
	now = now.UTC()
	return !fetchedAt.IsZero() && !fetchedAt.After(now) && now.Sub(fetchedAt) < profilePlanCacheTTL
}

func (status ProfileStatus) effectivePlanFetchedAt() time.Time {
	if status.PlanFetchedAt != nil {
		return status.PlanFetchedAt.UTC()
	}
	return status.FetchedAt.UTC()
}

func validProfileQuota(quota ProfileQuota) bool {
	return (quota.Monthly || quota.WindowDurationMins > 0) &&
		quota.RemainingPercent >= 0 && quota.RemainingPercent <= 100 && !quota.ResetsAt.IsZero()
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
