package profiles

import (
	"bytes"
	"crypto/rand"
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
const profileNotesFileName = ".profile-notes.json"
const profileInstallationIDsFileName = ".profile-installation-ids.json"

const invalidJSONReason = "invalid JSON"

type Snapshot struct {
	Profiles        []ProfileSummary
	InvalidProfiles []ProfileIssue
	CurrentProfile  string
	AuthActive      bool
}

type ProfileSummary struct {
	Name string
	Note string
}

type ProfileIssue struct {
	Name   string
	Reason string
}

type Manager struct {
	AuthFile            string
	InstallationIDFile  string
	ProfileDir          string
	CurrentProfileFile  string
	NotesFile           string
	InstallationIDsFile string
}

var (
	ErrStateChanged     = errors.New("operation changed persisted state before failing")
	errNoUsableIdentity = errors.New("auth file does not contain a usable identity")
)

type profileScan struct {
	Profiles        []ProfileSummary
	InvalidProfiles []ProfileIssue
}

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
		AuthFile:            filepath.Join(codexDir, "auth.json"),
		InstallationIDFile:  filepath.Join(codexDir, "installation_id"),
		ProfileDir:          filepath.Join(managerDir, "profiles"),
		CurrentProfileFile:  filepath.Join(managerDir, CurrentProfileMarkerName),
		NotesFile:           filepath.Join(managerDir, profileNotesFileName),
		InstallationIDsFile: filepath.Join(managerDir, profileInstallationIDsFileName),
	}
}

func (m Manager) Snapshot() (Snapshot, error) {
	if err := os.MkdirAll(m.ProfileDir, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("failed to create profile directory: %w", err)
	}

	scan, err := scanProfiles(m.ProfileDir)
	if err != nil {
		return Snapshot{}, err
	}
	notes, err := readProfileNotes(m.NotesFile)
	if err != nil {
		notes = map[string]string{}
	}
	applyProfileNotes(scan.Profiles, notes)

	snapshot := Snapshot{
		Profiles:        scan.Profiles,
		InvalidProfiles: scan.InvalidProfiles,
		AuthActive:      fileExists(m.AuthFile),
	}
	if !snapshot.AuthActive {
		if err := clearInstallationID(m.InstallationIDFile); err != nil {
			return Snapshot{}, err
		}
		return snapshot, nil
	}

	marker, err := resolveCurrentProfileMarker(m.AuthFile, m.CurrentProfileFile, m.ProfileDir, profileNames(scan.Profiles))
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.CurrentProfile = marker.Name
	if err := m.syncActiveInstallationID(marker); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (m Manager) SyncTrackedProfile() error {
	if !fileExists(m.AuthFile) {
		return nil
	}

	profiles, err := listProfileNames(m.ProfileDir)
	if err != nil {
		return err
	}

	marker, err := resolveCurrentProfileMarker(m.AuthFile, m.CurrentProfileFile, m.ProfileDir, profiles)
	if err != nil {
		return err
	}
	if marker.Name == "" {
		return clearInstallationID(m.InstallationIDFile)
	}

	if err := syncProfileFromAuth(m.AuthFile, m.ProfileDir, marker); err != nil {
		return err
	}
	return m.syncActiveInstallationID(marker)
}

func (m Manager) Activate(name string) error {
	if err := m.SyncTrackedProfile(); err != nil {
		return err
	}
	if err := activateProfile(m.AuthFile, []string{m.ProfileDir}, name); err != nil {
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
	if err := m.setActiveInstallationIDForProfile(name); err != nil {
		return fmt.Errorf("%w: activated profile %q, but failed to write installation_id: %v", ErrStateChanged, name, err)
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
	if err := m.setActiveInstallationIDForProfile(name); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	return nil
}

func (m Manager) Rename(oldName, newName, currentProfile string) error {
	if err := renameProfile(m.ProfileDir, oldName, newName); err != nil {
		return err
	}
	if err := m.renameNote(oldName, newName); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	if err := m.renameInstallationID(oldName, newName); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
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
	if err := m.setActiveInstallationIDForProfile(newName); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	return nil
}

func (m Manager) Delete(name, currentProfile string) error {
	if err := deleteProfile(m.ProfileDir, name); err != nil {
		return err
	}
	if err := m.deleteNote(name); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	if err := m.deleteInstallationID(name); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	if currentProfile == name {
		if err := clearCurrentProfileMarker(m.CurrentProfileFile); err != nil {
			return fmt.Errorf("%w: %w", ErrStateChanged, err)
		}
		if err := clearInstallationID(m.InstallationIDFile); err != nil {
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
	if err := clearInstallationID(m.InstallationIDFile); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	return nil
}

func (m Manager) SetNote(name, note string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("missing profile name")
	}
	if !fileExists(filepath.Join(m.ProfileDir, name)) {
		return fmt.Errorf("profile %q not found", name)
	}

	notes, err := readProfileNotes(m.NotesFile)
	if err != nil {
		return err
	}

	note, err = validateProfileNote(note)
	if err != nil {
		return err
	}

	if note == "" {
		delete(notes, name)
	} else {
		notes[name] = note
	}

	if err := writeProfileNotes(m.NotesFile, notes); err != nil {
		return err
	}
	return nil
}

func (m Manager) renameNote(oldName, newName string) error {
	notes, err := readProfileNotes(m.NotesFile)
	if err != nil {
		return err
	}

	note, ok := notes[oldName]
	if !ok {
		return nil
	}
	delete(notes, oldName)
	notes[newName] = note
	return writeProfileNotes(m.NotesFile, notes)
}

func (m Manager) deleteNote(name string) error {
	notes, err := readProfileNotes(m.NotesFile)
	if err != nil {
		return err
	}
	if _, ok := notes[name]; !ok {
		return nil
	}
	delete(notes, name)
	return writeProfileNotes(m.NotesFile, notes)
}

func (m Manager) syncActiveInstallationID(marker currentProfileMarker) error {
	if strings.TrimSpace(marker.Name) == "" {
		return clearInstallationID(m.InstallationIDFile)
	}
	return m.setActiveInstallationIDForProfile(marker.Name)
}

func (m Manager) setActiveInstallationIDForProfile(name string) error {
	id, err := m.ensureProfileInstallationID(name)
	if err != nil {
		return err
	}
	return writeInstallationID(m.InstallationIDFile, id)
}

func (m Manager) ensureProfileInstallationID(name string) (string, error) {
	ids, err := readProfileInstallationIDs(m.InstallationIDsFile)
	if err != nil {
		return "", err
	}

	if id := strings.TrimSpace(ids[name]); id != "" {
		if err := validateInstallationID(id); err == nil {
			return id, nil
		}
	}

	id, err := generateInstallationID()
	if err != nil {
		return "", err
	}
	ids[name] = id
	if err := writeProfileInstallationIDs(m.InstallationIDsFile, ids); err != nil {
		return "", err
	}
	return id, nil
}

func (m Manager) renameInstallationID(oldName, newName string) error {
	ids, err := readProfileInstallationIDs(m.InstallationIDsFile)
	if err != nil {
		return err
	}
	id, ok := ids[oldName]
	if !ok {
		return nil
	}
	delete(ids, oldName)
	ids[newName] = id
	return writeProfileInstallationIDs(m.InstallationIDsFile, ids)
}

func (m Manager) deleteInstallationID(name string) error {
	ids, err := readProfileInstallationIDs(m.InstallationIDsFile)
	if err != nil {
		return err
	}
	if _, ok := ids[name]; !ok {
		return nil
	}
	delete(ids, name)
	return writeProfileInstallationIDs(m.InstallationIDsFile, ids)
}

func listProfileNames(dirs ...string) ([]string, error) {
	scan, err := scanProfiles(dirs...)
	if err != nil {
		return nil, err
	}
	return profileNames(scan.Profiles), nil
}

func scanProfiles(dirs ...string) (profileScan, error) {
	seen := make(map[string]struct{})
	seenInvalid := make(map[string]struct{})
	var profiles []ProfileSummary
	var invalidProfiles []ProfileIssue

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return profileScan{}, fmt.Errorf("failed to read profile directory: %w", err)
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
				key := filepath.Join(dir, name)
				if _, ok := seenInvalid[key]; ok {
					continue
				}
				seenInvalid[key] = struct{}{}
				invalidProfiles = append(invalidProfiles, ProfileIssue{
					Name:   name,
					Reason: profileIssueReason(err),
				})
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			profiles = append(profiles, ProfileSummary{Name: name})
		}
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})
	sort.Slice(invalidProfiles, func(i, j int) bool {
		return invalidProfiles[i].Name < invalidProfiles[j].Name
	})
	return profileScan{Profiles: profiles, InvalidProfiles: invalidProfiles}, nil
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
		return errors.New("invalid profile name; use letters, numbers, dot, underscore, dash, @")
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

	profiles, err := listProfileNames(profileDir)
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
		return errors.New("invalid profile name; use letters, numbers, dot, underscore, dash, @")
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

func profileNames(profiles []ProfileSummary) []string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	return names
}

func applyProfileNotes(profiles []ProfileSummary, notes map[string]string) {
	for i := range profiles {
		profiles[i].Note = notes[profiles[i].Name]
	}
}

func readProfileNotes(path string) (map[string]string, error) {
	if !fileExists(path) {
		return map[string]string{}, nil
	}

	data, err := os.ReadFile(path) // #nosec G304 -- notes path is derived from the configured Codex directory.
	if err != nil {
		return nil, fmt.Errorf("failed to read profile notes: %w", err)
	}

	var notes map[string]string
	if err := json.Unmarshal(data, &notes); err != nil {
		return nil, fmt.Errorf("failed to parse profile notes: %w", err)
	}
	if notes == nil {
		return map[string]string{}, nil
	}

	cleaned := make(map[string]string, len(notes))
	for name, note := range notes {
		if !isValidProfileName(name) {
			continue
		}
		validated, err := validateProfileNote(note)
		if err != nil {
			continue
		}
		if validated == "" {
			continue
		}
		cleaned[name] = validated
	}
	return cleaned, nil
}

func writeProfileNotes(path string, notes map[string]string) error {
	filtered := make(map[string]string, len(notes))
	for name, note := range notes {
		if !isValidProfileName(name) {
			continue
		}
		validated, err := validateProfileNote(note)
		if err != nil {
			return err
		}
		if validated == "" {
			continue
		}
		filtered[name] = validated
	}

	if len(filtered) == 0 {
		err := os.Remove(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to clear profile notes: %w", err)
		}
		return nil
	}

	body, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode profile notes: %w", err)
	}
	if err := writeFileAtomically(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("failed to write profile notes: %w", err)
	}
	return nil
}

func readProfileInstallationIDs(path string) (map[string]string, error) {
	if !fileExists(path) {
		return map[string]string{}, nil
	}

	data, err := os.ReadFile(path) // #nosec G304 -- metadata path is derived from the configured Codex directory.
	if err != nil {
		return nil, fmt.Errorf("failed to read profile installation IDs: %w", err)
	}

	var ids map[string]string
	if err := json.Unmarshal(data, &ids); err != nil {
		return map[string]string{}, nil
	}
	if ids == nil {
		return map[string]string{}, nil
	}

	cleaned := make(map[string]string, len(ids))
	for name, id := range ids {
		if !isValidProfileName(name) {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if err := validateInstallationID(id); err != nil {
			continue
		}
		cleaned[name] = id
	}
	return cleaned, nil
}

func writeProfileInstallationIDs(path string, ids map[string]string) error {
	filtered := make(map[string]string, len(ids))
	for name, id := range ids {
		if !isValidProfileName(name) {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if err := validateInstallationID(id); err != nil {
			return err
		}
		filtered[name] = id
	}

	if len(filtered) == 0 {
		err := os.Remove(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to clear profile installation IDs: %w", err)
		}
		return nil
	}

	body, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode profile installation IDs: %w", err)
	}
	if err := writeFileAtomically(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("failed to write profile installation IDs: %w", err)
	}
	return nil
}

func writeInstallationID(path, id string) error {
	if err := validateInstallationID(id); err != nil {
		return err
	}
	if err := writeFileAtomically(path, []byte(id+"\n"), 0o600); err != nil {
		return fmt.Errorf("failed to write installation_id: %w", err)
	}
	return nil
}

func clearInstallationID(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to clear installation_id: %w", err)
	}
	return nil
}

func generateInstallationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate installation ID: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	), nil
}

func validateInstallationID(value string) error {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return errors.New("installation ID must be a UUID v4")
	}
	for _, idx := range []int{8, 13, 18, 23} {
		if value[idx] != '-' {
			return errors.New("installation ID must be a UUID v4")
		}
	}
	if value[14] != '4' {
		return errors.New("installation ID must be a UUID v4")
	}
	switch value[19] {
	case '8', '9', 'a', 'b', 'A', 'B':
	default:
		return errors.New("installation ID must be a UUID v4")
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return errors.New("installation ID must be a UUID v4")
		}
	}
	return nil
}

func validateProfileNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return "", nil
	}
	if len([]rune(note)) > 255 {
		return "", errors.New("profile note cannot exceed 255 characters")
	}
	return note, nil
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

	data, err := os.ReadFile(path) // #nosec G304 -- marker path is derived from the configured Codex directory.
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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

func readAuthIdentity(path string) (authIdentity, error) {
	data, err := os.ReadFile(path) // #nosec G304 G703 -- auth/profile paths are derived from the configured Codex directory.
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
		return authIdentity{}, fmt.Errorf("auth file %s: %w", path, errNoUsableIdentity)
	}

	return identity, nil
}

func profileIssueReason(err error) string {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return invalidJSONReason
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return "invalid auth JSON shape"
	}

	if errors.Is(err, errNoUsableIdentity) {
		return "missing usable identity"
	}

	return "unreadable profile"
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
	ab, err := os.ReadFile(a) // #nosec G304 -- compared paths come from managed profile locations.
	if err != nil {
		return false, fmt.Errorf("failed reading %s: %w", a, err)
	}
	bb, err := os.ReadFile(b) // #nosec G304 -- compared paths come from managed profile locations.
	if err != nil {
		return false, fmt.Errorf("failed reading %s: %w", b, err)
	}
	return bytes.Equal(ab, bb), nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}

	in, err := os.Open(src) // #nosec G304 -- source path is a managed auth/profile path.
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := out.Name()

	if err := out.Chmod(0o600); err != nil {
		_ = out.Close()
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	out, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := out.Name()

	if err := out.Chmod(perm); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}

	if _, err := out.Write(data); err != nil {
		_ = out.Close()
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
	info, err := os.Stat(path) // #nosec G703 -- callers pass managed application paths.
	if err != nil {
		return false
	}
	return !info.IsDir()
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
		case r == '.', r == '_', r == '-', r == '@':
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
