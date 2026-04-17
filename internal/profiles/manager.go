package profiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const CurrentProfileMarkerName = "current-profile"

type Snapshot struct {
	Profiles       []string
	CurrentProfile string
	AuthActive     bool
}

type Manager struct {
	AuthFile           string
	ProfileDir         string
	LegacyProfileDir   string
	CurrentProfileFile string
}

var ErrStateChanged = errors.New("operation changed persisted state before failing")

type authFileData struct {
	AuthMode     string `json:"auth_mode"`
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		AccountID string `json:"account_id"`
	} `json:"tokens"`
}

type authIdentity struct {
	AuthMode   string `json:"auth_mode,omitempty"`
	AccountID  string `json:"account_id,omitempty"`
	APIKeyHash string `json:"api_key_hash,omitempty"`
}

type currentProfileMarker struct {
	Name     string       `json:"name"`
	Identity authIdentity `json:"identity"`
}

func NewManager(codexDir string) Manager {
	managerDir := filepath.Join(codexDir, "auth_manager")
	return Manager{
		AuthFile:           filepath.Join(codexDir, "auth.json"),
		ProfileDir:         filepath.Join(managerDir, "profiles"),
		LegacyProfileDir:   managerDir,
		CurrentProfileFile: filepath.Join(managerDir, CurrentProfileMarkerName),
	}
}

func (m Manager) Snapshot() (Snapshot, error) {
	if err := os.MkdirAll(m.ProfileDir, 0o755); err != nil {
		return Snapshot{}, fmt.Errorf("failed to create profile directory: %w", err)
	}
	if err := os.MkdirAll(m.LegacyProfileDir, 0o755); err != nil {
		return Snapshot{}, fmt.Errorf("failed to create legacy profile directory: %w", err)
	}
	if err := migrateLegacyProfiles(m.LegacyProfileDir, m.ProfileDir); err != nil {
		return Snapshot{}, err
	}

	profiles, err := listProfiles(m.ProfileDir)
	if err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		Profiles:   profiles,
		AuthActive: fileExists(m.AuthFile),
	}
	if !snapshot.AuthActive {
		return snapshot, nil
	}

	marker, err := resolveCurrentProfileMarker(m.AuthFile, m.CurrentProfileFile, m.ProfileDir, profiles)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.CurrentProfile = marker.Name
	return snapshot, nil
}

func (m Manager) SyncTrackedProfile() error {
	if !fileExists(m.AuthFile) {
		return nil
	}

	profiles, err := listProfiles(m.ProfileDir)
	if err != nil {
		return err
	}

	marker, err := resolveCurrentProfileMarker(m.AuthFile, m.CurrentProfileFile, m.ProfileDir, profiles)
	if err != nil {
		return err
	}
	if marker.Name == "" {
		return nil
	}

	return syncProfileFromAuth(m.AuthFile, m.ProfileDir, marker)
}

func (m Manager) Activate(name string) error {
	if err := m.SyncTrackedProfile(); err != nil {
		return err
	}
	if err := activateProfile(m.AuthFile, []string{m.ProfileDir, m.LegacyProfileDir}, name); err != nil {
		return err
	}

	marker, err := markerForProfile(m.ProfileDir, name)
	if err != nil {
		_ = clearCurrentProfileMarker(m.CurrentProfileFile)
		return err
	}
	if err := writeCurrentProfileMarker(m.CurrentProfileFile, marker); err != nil {
		_ = clearCurrentProfileMarker(m.CurrentProfileFile)
		return fmt.Errorf("activated profile %q, but failed to track it: %v", name, err)
	}

	return nil
}

func (m Manager) SaveCurrent(name string) error {
	if err := saveCurrentAuth(m.AuthFile, m.ProfileDir, name); err != nil {
		return err
	}
	marker, err := markerForProfile(m.ProfileDir, name)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	if err := writeCurrentProfileMarker(m.CurrentProfileFile, marker); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	return nil
}

func (m Manager) Rename(oldName, newName, currentProfile string) error {
	if err := renameProfile(m.ProfileDir, oldName, newName); err != nil {
		return err
	}
	if currentProfile != oldName {
		return nil
	}
	marker, err := markerForProfile(m.ProfileDir, newName)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	if err := writeCurrentProfileMarker(m.CurrentProfileFile, marker); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	return nil
}

func (m Manager) Delete(name, currentProfile string) error {
	if err := deleteProfile(m.ProfileDir, name); err != nil {
		return err
	}
	if currentProfile == name {
		if err := clearCurrentProfileMarker(m.CurrentProfileFile); err != nil {
			return fmt.Errorf("%w: %w", ErrStateChanged, err)
		}
	}
	return nil
}

func (m Manager) Logout() error {
	if err := m.SyncTrackedProfile(); err != nil {
		return err
	}
	if err := logoutAuth(m.AuthFile); err != nil {
		return err
	}
	if err := clearCurrentProfileMarker(m.CurrentProfileFile); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	return nil
}

func listProfiles(dirs ...string) ([]string, error) {
	seen := make(map[string]struct{})
	var profiles []string

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("failed to read profile directory: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !isProfileFilename(name) {
				continue
			}
			if _, err := readAuthIdentity(filepath.Join(dir, name)); err != nil {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			profiles = append(profiles, name)
		}
	}

	sort.Strings(profiles)
	return profiles, nil
}

var errNoMatchingProfile = errors.New("no matching saved profile")

func currentProfile(authFile string, profileDirs []string, profiles []string) (string, error) {
	authIdentity, err := readAuthIdentity(authFile)
	if err != nil {
		return "", nil
	}

	name, err := findMatchingProfile(profileDirs, profiles, func(path string) (bool, error) {
		return filesEqual(authFile, path)
	})
	if err == nil {
		return name, nil
	}
	if !errors.Is(err, errNoMatchingProfile) {
		return "", err
	}

	return findMatchingProfile(profileDirs, profiles, func(path string) (bool, error) {
		profileIdentity, err := readAuthIdentity(path)
		if err != nil {
			return false, nil
		}
		return profileIdentity.matches(authIdentity), nil
	})
}

func activateProfile(authFile string, profileDirs []string, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("missing profile name")
	}

	src, ok := findProfilePath(profileDirs, name)
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	if err := copyFile(src, authFile); err != nil {
		return fmt.Errorf("failed to activate profile %q: %w", name, err)
	}
	return nil
}

func syncProfileFromAuth(authFile, profileDir string, marker currentProfileMarker) error {
	if strings.TrimSpace(marker.Name) == "" || !fileExists(authFile) {
		return nil
	}

	authIdentity, err := readAuthIdentity(authFile)
	if err != nil || !marker.Identity.matches(authIdentity) {
		return nil
	}

	dst := filepath.Join(profileDir, marker.Name)
	if !fileExists(dst) {
		return nil
	}

	same, err := filesEqual(authFile, dst)
	if err != nil {
		return err
	}
	if same {
		return nil
	}

	if err := copyFile(authFile, dst); err != nil {
		return fmt.Errorf("failed to update profile %q: %w", marker.Name, err)
	}
	if err := writeCurrentProfileMarker(filepath.Join(filepath.Dir(profileDir), CurrentProfileMarkerName), currentProfileMarker{
		Name:     marker.Name,
		Identity: authIdentity,
	}); err != nil {
		return err
	}
	return nil
}

func saveCurrentAuth(authFile, profileDir, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("profile name cannot be empty")
	}
	if !isValidProfileName(name) {
		return errors.New("invalid profile name; use letters, numbers, dot, underscore, dash")
	}
	if !fileExists(authFile) {
		return errors.New("no auth.json found - nothing to save")
	}
	if _, err := readAuthIdentity(authFile); err != nil {
		return fmt.Errorf("current auth.json is invalid: %w", err)
	}

	dst := filepath.Join(profileDir, name)
	if fileExists(dst) {
		return fmt.Errorf("profile %q already exists", name)
	}

	profiles, err := listProfiles(profileDir)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		same, err := filesEqual(authFile, filepath.Join(profileDir, p))
		if err != nil {
			return err
		}
		if same {
			return fmt.Errorf("same auth already exists as profile %q", p)
		}
	}

	if err := copyFile(authFile, dst); err != nil {
		return fmt.Errorf("failed to save profile %q: %w", name, err)
	}
	return nil
}

func renameProfile(profileDir, oldName, newName string) error {
	newName = strings.TrimSpace(newName)

	if oldName == "" {
		return errors.New("missing source profile")
	}
	if newName == "" {
		return errors.New("new profile name cannot be empty")
	}
	if !isValidProfileName(newName) {
		return errors.New("invalid profile name; use letters, numbers, dot, underscore, dash")
	}
	if oldName == newName {
		return nil
	}

	oldPath := filepath.Join(profileDir, oldName)
	newPath := filepath.Join(profileDir, newName)

	if !fileExists(oldPath) {
		return fmt.Errorf("profile %q not found", oldName)
	}
	if fileExists(newPath) {
		return fmt.Errorf("profile %q already exists", newName)
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("failed to rename profile: %w", err)
	}
	return nil
}

func deleteProfile(profileDir, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("missing profile name")
	}

	path := filepath.Join(profileDir, name)
	if !fileExists(path) {
		return fmt.Errorf("profile %q not found", name)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to delete profile %q: %w", name, err)
	}
	return nil
}

func readCurrentProfileMarker(path, profileDir string) (currentProfileMarker, error) {
	if !fileExists(path) {
		return currentProfileMarker{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return currentProfileMarker{}, fmt.Errorf("failed to read current profile marker: %w", err)
	}

	var marker currentProfileMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		marker.Name = strings.TrimSpace(string(data))
	}

	marker.Name = strings.TrimSpace(marker.Name)
	if marker.Name == "" {
		return currentProfileMarker{}, nil
	}
	profilePath := filepath.Join(profileDir, marker.Name)
	if !fileExists(profilePath) {
		return currentProfileMarker{}, nil
	}
	if !marker.Identity.hasUsableIdentity() {
		identity, err := readAuthIdentity(profilePath)
		if err != nil {
			return currentProfileMarker{}, err
		}
		marker.Identity = identity
	}

	return marker, nil
}

func resolveCurrentProfileMarker(authFile, markerPath, profileDir string, profiles []string) (currentProfileMarker, error) {
	authIdentity, err := readAuthIdentity(authFile)
	if err != nil {
		return currentProfileMarker{}, nil
	}

	marker, err := readCurrentProfileMarker(markerPath, profileDir)
	if err != nil {
		return currentProfileMarker{}, err
	}
	if marker.Name != "" && marker.Identity.matches(authIdentity) {
		if err := writeCurrentProfileMarker(markerPath, marker); err != nil {
			return currentProfileMarker{}, err
		}
		return marker, nil
	}

	name, err := currentProfile(authFile, []string{profileDir}, profiles)
	if err != nil {
		if errors.Is(err, errNoMatchingProfile) {
			return currentProfileMarker{}, nil
		}
		return currentProfileMarker{}, err
	}

	resolved := currentProfileMarker{
		Name:     name,
		Identity: authIdentity,
	}
	if err := writeCurrentProfileMarker(markerPath, resolved); err != nil {
		return currentProfileMarker{}, err
	}
	return resolved, nil
}

func writeCurrentProfileMarker(path string, marker currentProfileMarker) error {
	marker.Name = strings.TrimSpace(marker.Name)
	if marker.Name == "" {
		return clearCurrentProfileMarker(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create marker directory: %w", err)
	}
	body, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode current profile marker: %w", err)
	}
	if err := writeFileAtomically(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("failed to write current profile marker: %w", err)
	}
	return nil
}

func clearCurrentProfileMarker(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to clear current profile marker: %w", err)
	}
	return nil
}

func markerForProfile(profileDir, name string) (currentProfileMarker, error) {
	identity, err := readAuthIdentity(filepath.Join(profileDir, name))
	if err != nil {
		return currentProfileMarker{}, fmt.Errorf("failed to read profile identity for %q: %w", name, err)
	}
	return currentProfileMarker{Name: name, Identity: identity}, nil
}

func migrateLegacyProfiles(legacyDir, profileDir string) error {
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to read legacy profile directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !isProfileFilename(name) {
			continue
		}

		legacyPath := filepath.Join(legacyDir, name)
		if _, err := readAuthIdentity(legacyPath); err != nil {
			continue
		}

		dst := filepath.Join(profileDir, name)
		if !fileExists(dst) {
			if err := os.Rename(legacyPath, dst); err != nil {
				return fmt.Errorf("failed to migrate legacy profile %q: %w", name, err)
			}
			continue
		}

		same, err := filesEqual(legacyPath, dst)
		if err != nil {
			return err
		}
		if same {
			if err := os.Remove(legacyPath); err != nil {
				return fmt.Errorf("failed to remove duplicate legacy profile %q: %w", name, err)
			}
			continue
		}

		migratedName, err := uniqueMigratedProfileName(profileDir, name)
		if err != nil {
			return err
		}
		if err := os.Rename(legacyPath, filepath.Join(profileDir, migratedName)); err != nil {
			return fmt.Errorf("failed to migrate conflicting legacy profile %q: %w", name, err)
		}
	}

	return nil
}

func uniqueMigratedProfileName(profileDir, name string) (string, error) {
	base := name + "-legacy"
	candidate := base
	for i := 2; ; i++ {
		if !fileExists(filepath.Join(profileDir, candidate)) {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func readAuthIdentity(path string) (authIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return authIdentity{}, fmt.Errorf("failed to read auth file %s: %w", path, err)
	}

	var auth authFileData
	if err := json.Unmarshal(data, &auth); err != nil {
		return authIdentity{}, fmt.Errorf("failed to parse auth file %s: %w", path, err)
	}

	identity := authIdentity{
		AuthMode:  strings.TrimSpace(auth.AuthMode),
		AccountID: strings.TrimSpace(auth.Tokens.AccountID),
	}
	if key := strings.TrimSpace(auth.OpenAIAPIKey); key != "" {
		sum := sha256.Sum256([]byte(key))
		identity.APIKeyHash = hex.EncodeToString(sum[:])
	}

	if identity.AuthMode == "" && identity.AccountID == "" && identity.APIKeyHash == "" {
		return authIdentity{}, fmt.Errorf("auth file %s does not contain a usable identity", path)
	}

	return identity, nil
}

func (a authIdentity) matches(other authIdentity) bool {
	if a.AuthMode != other.AuthMode {
		return false
	}
	if a.AccountID != "" || other.AccountID != "" {
		return a.AccountID != "" && a.AccountID == other.AccountID
	}
	if a.APIKeyHash != "" || other.APIKeyHash != "" {
		return a.APIKeyHash != "" && a.APIKeyHash == other.APIKeyHash
	}
	return false
}

func (a authIdentity) hasUsableIdentity() bool {
	return a.AuthMode != "" || a.AccountID != "" || a.APIKeyHash != ""
}

func logoutAuth(authFile string) error {
	if !fileExists(authFile) {
		return errors.New("already logged out")
	}
	if err := os.Remove(authFile); err != nil {
		return fmt.Errorf("failed to remove auth.json: %w", err)
	}
	return nil
}

func filesEqual(a, b string) (bool, error) {
	ab, err := os.ReadFile(a)
	if err != nil {
		return false, fmt.Errorf("failed reading %s: %w", a, err)
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		return false, fmt.Errorf("failed reading %s: %w", b, err)
	}
	return bytes.Equal(ab, bb), nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := out.Name()

	if err := out.Chmod(0o600); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()

	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}

	return os.Rename(tmp, dst)
}

func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	out, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := out.Name()

	if err := out.Chmod(perm); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}

	if _, err := out.Write(data); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func findMatchingProfile(profileDirs, profiles []string, match func(path string) (bool, error)) (string, error) {
	for _, name := range profiles {
		for _, dir := range profileDirs {
			path := filepath.Join(dir, name)
			if !fileExists(path) {
				continue
			}
			ok, err := match(path)
			if err != nil {
				return "", err
			}
			if ok {
				return name, nil
			}
		}
	}

	return "", errNoMatchingProfile
}

func findProfilePath(dirs []string, name string) (string, bool) {
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		if fileExists(path) {
			if _, err := readAuthIdentity(path); err != nil {
				continue
			}
			return path, true
		}
	}
	return "", false
}

func isValidProfileName(name string) bool {
	if name == CurrentProfileMarkerName {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return name != ""
}

func isProfileFilename(name string) bool {
	if name == CurrentProfileMarkerName {
		return false
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".tmp") || strings.Contains(lower, ".tmp-") {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	return true
}
