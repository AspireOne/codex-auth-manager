package profiles

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const CurrentProfileMarkerName = "current-profile"
const profileMetadataFileName = ".profile-metadata.json"
const profileNotesFileName = ".profile-notes.json"
const profileInstallationIDsFileName = ".profile-installation-ids.json"

const invalidJSONReason = "invalid JSON"

type Snapshot struct {
	Profiles          []ProfileSummary
	InvalidProfiles   []ProfileIssue
	CurrentProfileKey string
	CurrentAuth       AuthSummary
	AuthActive        bool
}

type ProfileSummary struct {
	Key         string
	Label       string
	CustomLabel string
	Kind        AuthKind
	Note        string
	Plan        Plan

	identity  authIdentity
	email     string
	baseLabel string
}

type AuthSummary struct {
	Kind  AuthKind
	Label string
}

type AuthKind string

const (
	AuthKindChatGPT AuthKind = "chatgpt"
	AuthKindAPIKey  AuthKind = "apikey"
)

type Plan string

const (
	PlanFree Plan = "free"
	PlanPlus Plan = "plus"
	PlanPro  Plan = "pro"
)

func (p Plan) Label() string {
	switch p {
	case PlanPro:
		return "Pro"
	case PlanPlus:
		return "Plus"
	case PlanFree:
		return "Free"
	default:
		return "Free"
	}
}

func (p Plan) Next() Plan {
	switch p {
	case PlanPlus:
		return PlanPro
	case PlanPro:
		return PlanFree
	case PlanFree:
		return PlanPlus
	default:
		return PlanPlus
	}
}

func (p Plan) Rank() int {
	switch p {
	case PlanPro:
		return 0
	case PlanPlus:
		return 1
	case PlanFree:
		return 2
	default:
		return 2
	}
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
	MetadataFile        string
	LegacyNotesFile     string
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

type profileMetadata struct {
	Label string `json:"label,omitempty"`
	Note  string `json:"note,omitempty"`
	Plan  Plan   `json:"plan,omitempty"`
}

type authFileData struct {
	AuthMode     string `json:"auth_mode"`
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		AccountID string `json:"account_id"`
		IDToken   string `json:"id_token"`
	} `json:"tokens"`
}

type authDescriptor struct {
	Identity authIdentity
	Kind     AuthKind
	Email    string
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
		MetadataFile:        filepath.Join(managerDir, profileMetadataFileName),
		LegacyNotesFile:     filepath.Join(managerDir, profileNotesFileName),
		InstallationIDsFile: filepath.Join(managerDir, profileInstallationIDsFileName),
	}
}

func (m Manager) ensurePrivateStorage() error {
	dirs := []string{filepath.Dir(m.ProfileDir), m.ProfileDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("failed to create auth manager directory: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("failed to secure auth manager directory %s: %w", dir, err)
		}
	}

	managedFiles := []string{
		m.AuthFile,
		m.InstallationIDFile,
		m.CurrentProfileFile,
		m.MetadataFile,
		m.LegacyNotesFile,
		m.InstallationIDsFile,
	}
	entries, err := os.ReadDir(m.ProfileDir)
	if err != nil {
		return fmt.Errorf("failed to read profile directory: %w", err)
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			managedFiles = append(managedFiles, filepath.Join(m.ProfileDir, entry.Name()))
		}
	}
	for _, path := range managedFiles {
		if err := chmodRegularFile(path, 0o600); err != nil {
			return fmt.Errorf("failed to secure managed auth file %s: %w", path, err)
		}
	}
	return nil
}

func chmodRegularFile(path string, perm os.FileMode) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return os.Chmod(path, perm)
}

func (m Manager) Snapshot() (Snapshot, error) {
	if err := m.ensurePrivateStorage(); err != nil {
		return Snapshot{}, err
	}

	scan, err := scanProfiles(m.ProfileDir)
	if err != nil {
		return Snapshot{}, err
	}
	metadata, err := m.loadProfileMetadata()
	if err != nil {
		return Snapshot{}, err
	}
	applyProfileMetadata(scan.Profiles, metadata)

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

	auth, err := readAuthDescriptor(m.AuthFile)
	if err != nil {
		if clearErr := clearInstallationID(m.InstallationIDFile); clearErr != nil {
			return Snapshot{}, clearErr
		}
		return snapshot, nil
	}
	snapshot.CurrentAuth = AuthSummary{Kind: auth.Kind, Label: baseAuthLabel(auth, "")}

	marker, err := resolveCurrentProfileMarker(m.AuthFile, m.CurrentProfileFile, m.ProfileDir, profileKeys(scan.Profiles))
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.CurrentProfileKey = marker.Name
	if current := profileByKey(snapshot.Profiles, marker.Name); current != nil {
		snapshot.CurrentAuth = AuthSummary{Kind: current.Kind, Label: current.Label}
	}
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

func (m Manager) SaveCurrent(label string) error {
	descriptor, err := readAuthDescriptor(m.AuthFile)
	if err != nil {
		if !fileExists(m.AuthFile) {
			return errors.New("no auth.json found - nothing to save")
		}
		return fmt.Errorf("current auth.json is invalid: %w", err)
	}

	label, err = validateLabelForAuth(descriptor.Kind, label)
	if err != nil {
		return err
	}
	if err := m.ensurePrivateStorage(); err != nil {
		return err
	}
	key := storageKeyForIdentity(descriptor)
	if err := m.ensureIdentityNotSaved(descriptor.Identity); err != nil {
		return err
	}
	if fileExists(filepath.Join(m.ProfileDir, key)) {
		return fmt.Errorf("profile storage key %q already exists", key)
	}
	if err := copyFile(m.AuthFile, filepath.Join(m.ProfileDir, key)); err != nil {
		return fmt.Errorf("failed to save current auth: %w", err)
	}
	if descriptor.Kind == AuthKindAPIKey && label != "" {
		if err := m.setProfileLabel(key, label); err != nil {
			return fmt.Errorf("%w: %w", ErrStateChanged, err)
		}
	}

	marker, err := markerForProfile(m.ProfileDir, key)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	if err := writeCurrentProfileMarker(m.CurrentProfileFile, marker); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	if err := m.setActiveInstallationIDForProfile(key); err != nil {
		return fmt.Errorf("%w: %w", ErrStateChanged, err)
	}
	return nil
}

func (m Manager) Delete(name, currentProfile string) error {
	if err := deleteProfile(m.ProfileDir, name); err != nil {
		return err
	}
	if err := m.deleteMetadata(name); err != nil {
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

	metadata, err := m.loadProfileMetadata()
	if err != nil {
		return err
	}

	note, err = validateProfileNote(note)
	if err != nil {
		return err
	}

	entry := metadata[name]
	entry.Note = note
	metadata[name] = entry
	return writeProfileMetadata(m.MetadataFile, metadata)
}

func (m Manager) SetPlan(name string, plan Plan) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("missing profile name")
	}
	if !fileExists(filepath.Join(m.ProfileDir, name)) {
		return fmt.Errorf("profile %q not found", name)
	}
	if err := validatePlan(plan); err != nil {
		return err
	}

	metadata, err := m.loadProfileMetadata()
	if err != nil {
		return err
	}
	entry := metadata[name]
	entry.Plan = plan
	metadata[name] = entry
	return writeProfileMetadata(m.MetadataFile, metadata)
}

func (m Manager) SetLabel(key, label string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("missing profile key")
	}
	descriptor, err := readAuthDescriptor(filepath.Join(m.ProfileDir, key))
	if err != nil {
		return fmt.Errorf("failed to read profile: %w", err)
	}
	if descriptor.Kind != AuthKindAPIKey {
		return errors.New("ChatGPT profile labels come from the account email")
	}
	label, err = validateProfileLabel(label)
	if err != nil {
		return err
	}
	return m.setProfileLabel(key, label)
}

func (m Manager) setProfileLabel(key, label string) error {
	metadata, err := m.loadProfileMetadata()
	if err != nil {
		return err
	}
	entry := metadata[key]
	entry.Label = label
	metadata[key] = entry
	return writeProfileMetadata(m.MetadataFile, metadata)
}

func (m Manager) deleteMetadata(name string) error {
	metadata, err := m.loadProfileMetadata()
	if err != nil {
		return err
	}
	if _, ok := metadata[name]; !ok {
		return nil
	}
	delete(metadata, name)
	return writeProfileMetadata(m.MetadataFile, metadata)
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
	return profileKeys(scan.Profiles), nil
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
			descriptor, err := readAuthDescriptor(filepath.Join(dir, name))
			if err != nil {
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
			profiles = append(profiles, ProfileSummary{
				Key:       name,
				Kind:      descriptor.Kind,
				identity:  descriptor.Identity,
				email:     descriptor.Email,
				baseLabel: baseAuthLabel(descriptor, ""),
			})
		}
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Key < profiles[j].Key
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

func profileKeys(profiles []ProfileSummary) []string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Key)
	}
	return names
}

func applyProfileMetadata(profiles []ProfileSummary, metadata map[string]profileMetadata) {
	for i := range profiles {
		entry := metadata[profiles[i].Key]
		profiles[i].Note = entry.Note
		profiles[i].Plan = normalizedPlan(entry.Plan)
		if profiles[i].Kind == AuthKindAPIKey {
			profiles[i].CustomLabel = entry.Label
		}
		profiles[i].baseLabel = profileBaseLabel(profiles[i], entry.Label)
	}
	resolveProfileLabels(profiles)
}

func profileByKey(profiles []ProfileSummary, key string) *ProfileSummary {
	for i := range profiles {
		if profiles[i].Key == key {
			return &profiles[i]
		}
	}
	return nil
}

func profileBaseLabel(profile ProfileSummary, customLabel string) string {
	descriptor := authDescriptor{Identity: profile.identity, Kind: profile.Kind, Email: profile.email}
	return baseAuthLabel(descriptor, customLabel)
}

func baseAuthLabel(descriptor authDescriptor, customLabel string) string {
	switch descriptor.Kind {
	case AuthKindChatGPT:
		if email := strings.TrimSpace(descriptor.Email); email != "" {
			return email
		}
		return "ChatGPT account · " + accountIDSuffix(descriptor.Identity.AccountID)
	case AuthKindAPIKey:
		if label := strings.TrimSpace(customLabel); label != "" {
			return label
		}
		return "API key · " + shortHead(descriptor.Identity.APIKeyHash, 8)
	default:
		return "Unknown auth"
	}
}

func resolveProfileLabels(profiles []ProfileSummary) {
	baseGroups := make(map[string][]int)
	for i := range profiles {
		profiles[i].Label = profiles[i].baseLabel
		key := strings.ToLower(profiles[i].baseLabel)
		baseGroups[key] = append(baseGroups[key], i)
	}
	for _, indexes := range baseGroups {
		if len(indexes) < 2 {
			continue
		}
		for _, i := range indexes {
			fingerprint := shortHead(hashString(canonicalIdentity(profiles[i].identity)), 8)
			profiles[i].Label += " · " + fingerprint
		}
	}

	roots := make([]string, len(profiles))
	suffixLengths := make([]int, len(profiles))
	for i := range profiles {
		roots[i] = profiles[i].Label
	}
	for {
		collisions := duplicateLabelGroups(profiles)
		if len(collisions) == 0 {
			return
		}
		for _, indexes := range collisions {
			for _, i := range indexes {
				if suffixLengths[i] == 0 {
					suffixLengths[i] = 8
				} else if suffixLengths[i] < sha256.Size*2 {
					suffixLengths[i] += 4
				} else {
					profiles[i].Label = roots[i] + " · " + profileDiscriminator(profiles[i]) + " · " + base64.RawURLEncoding.EncodeToString([]byte(profiles[i].Key))
					continue
				}
				fingerprint := profileDiscriminator(profiles[i])
				profiles[i].Label = roots[i] + " · " + shortHead(fingerprint, suffixLengths[i])
			}
		}
	}
}

func profileDiscriminator(profile ProfileSummary) string {
	identity := canonicalIdentity(profile.identity)
	return hashString(identity + "\x00" + profile.Key)
}

func duplicateLabelGroups(profiles []ProfileSummary) [][]int {
	groups := make(map[string][]int)
	for i := range profiles {
		key := strings.ToLower(profiles[i].Label)
		groups[key] = append(groups[key], i)
	}
	duplicates := make([][]int, 0)
	for _, indexes := range groups {
		if len(indexes) > 1 {
			duplicates = append(duplicates, indexes)
		}
	}
	return duplicates
}

func shortHead(value string, length int) string {
	if len(value) <= length {
		return value
	}
	return value[:length]
}

func shortTail(value string, length int) string {
	if len(value) <= length {
		return value
	}
	return value[len(value)-length:]
}

func accountIDSuffix(accountID string) string {
	for _, r := range accountID {
		if unicode.IsControl(r) {
			return shortHead(hashString(accountID), 8)
		}
	}
	return shortTail(accountID, 8)
}

func (m Manager) loadProfileMetadata() (map[string]profileMetadata, error) {
	if fileExists(m.MetadataFile) {
		metadata, err := readProfileMetadata(m.MetadataFile)
		if err != nil {
			return nil, err
		}
		if err := removeLegacyNotesFile(m.LegacyNotesFile); err != nil {
			return nil, err
		}
		return metadata, nil
	}

	if !fileExists(m.LegacyNotesFile) {
		return map[string]profileMetadata{}, nil
	}

	notes, err := readLegacyProfileNotes(m.LegacyNotesFile)
	if err != nil {
		return nil, fmt.Errorf("failed to migrate profile notes: %w", err)
	}
	metadata := make(map[string]profileMetadata, len(notes))
	for name, note := range notes {
		metadata[name] = profileMetadata{Note: note, Plan: PlanFree}
	}
	if err := writeProfileMetadata(m.MetadataFile, metadata); err != nil {
		return nil, fmt.Errorf("failed to migrate profile notes: %w", err)
	}
	if err := removeLegacyNotesFile(m.LegacyNotesFile); err != nil {
		return nil, err
	}
	return metadata, nil
}

func removeLegacyNotesFile(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove migrated profile notes: %w", err)
	}
	return nil
}

func readLegacyProfileNotes(path string) (map[string]string, error) {
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

func readProfileMetadata(path string) (map[string]profileMetadata, error) {
	if !fileExists(path) {
		return map[string]profileMetadata{}, nil
	}

	data, err := os.ReadFile(path) // #nosec G304 -- metadata path is derived from the configured Codex directory.
	if err != nil {
		return nil, fmt.Errorf("failed to read profile metadata: %w", err)
	}

	var metadata map[string]profileMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse profile metadata: %w", err)
	}
	if metadata == nil {
		return map[string]profileMetadata{}, nil
	}

	cleaned := make(map[string]profileMetadata, len(metadata))
	for name, entry := range metadata {
		if !isValidProfileName(name) {
			continue
		}
		note, err := validateProfileNote(entry.Note)
		if err != nil {
			return nil, fmt.Errorf("invalid metadata for profile %q: %w", name, err)
		}
		if entry.Plan != "" {
			if err := validatePlan(entry.Plan); err != nil {
				return nil, fmt.Errorf("invalid metadata for profile %q: %w", name, err)
			}
		}
		label, err := validateProfileLabel(entry.Label)
		if err != nil {
			return nil, fmt.Errorf("invalid metadata for profile %q: %w", name, err)
		}
		cleaned[name] = profileMetadata{Label: label, Note: note, Plan: normalizedPlan(entry.Plan)}
	}
	return cleaned, nil
}

func writeProfileMetadata(path string, metadata map[string]profileMetadata) error {
	filtered := make(map[string]profileMetadata, len(metadata))
	for name, entry := range metadata {
		if !isValidProfileName(name) {
			continue
		}
		note, err := validateProfileNote(entry.Note)
		if err != nil {
			return err
		}
		plan := normalizedPlan(entry.Plan)
		if err := validatePlan(plan); err != nil {
			return err
		}
		label, err := validateProfileLabel(entry.Label)
		if err != nil {
			return err
		}
		if label == "" && note == "" && plan == PlanFree {
			continue
		}
		if plan == PlanFree {
			plan = ""
		}
		filtered[name] = profileMetadata{Label: label, Note: note, Plan: plan}
	}

	if len(filtered) == 0 {
		err := os.Remove(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to clear profile metadata: %w", err)
		}
		return nil
	}

	body, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode profile metadata: %w", err)
	}
	if err := writeFileAtomically(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("failed to write profile metadata: %w", err)
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

func validateProfileLabel(label string) (string, error) {
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", errors.New("profile label must be a single line without control characters")
		}
	}
	label = strings.TrimSpace(label)
	if utf8.RuneCountInString(label) > 64 {
		return "", errors.New("profile label cannot exceed 64 characters")
	}
	return label, nil
}

func validateLabelForAuth(kind AuthKind, label string) (string, error) {
	label, err := validateProfileLabel(label)
	if err != nil {
		return "", err
	}
	if kind == AuthKindChatGPT && label != "" {
		return "", errors.New("ChatGPT profile labels come from the account email")
	}
	return label, nil
}

func validatePlan(plan Plan) error {
	switch plan {
	case PlanFree, PlanPlus, PlanPro:
		return nil
	default:
		return fmt.Errorf("invalid profile plan %q; use free, plus, or pro", plan)
	}
}

func normalizedPlan(plan Plan) Plan {
	if plan == "" {
		return PlanFree
	}
	return plan
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
	descriptor, err := readAuthDescriptor(path)
	if err != nil {
		return authIdentity{}, err
	}
	return descriptor.Identity, nil
}

func readAuthDescriptor(path string) (authDescriptor, error) {
	data, err := os.ReadFile(path) // #nosec G304 G703 -- auth/profile paths are derived from the configured Codex directory.
	if err != nil {
		return authDescriptor{}, fmt.Errorf("failed to read auth file %s: %w", path, err)
	}

	var auth authFileData
	if err := json.Unmarshal(data, &auth); err != nil {
		return authDescriptor{}, fmt.Errorf("failed to parse auth file %s: %w", path, err)
	}

	identity := authIdentity{
		AuthMode:  strings.TrimSpace(auth.AuthMode),
		AccountID: strings.TrimSpace(auth.Tokens.AccountID),
	}
	if key := strings.TrimSpace(auth.OpenAIAPIKey); key != "" {
		sum := sha256.Sum256([]byte(key))
		identity.APIKeyHash = hex.EncodeToString(sum[:])
	}

	if identity.AccountID == "" && identity.APIKeyHash == "" {
		return authDescriptor{}, fmt.Errorf("auth file %s: %w", path, errNoUsableIdentity)
	}

	if identity.AccountID != "" {
		return authDescriptor{
			Identity: identity,
			Kind:     AuthKindChatGPT,
			Email:    emailFromIDToken(auth.Tokens.IDToken),
		}, nil
	}
	return authDescriptor{Identity: identity, Kind: AuthKindAPIKey}, nil
}

func emailFromIDToken(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	email := strings.TrimSpace(claims.Email)
	if email == "" || utf8.RuneCountInString(email) > 254 {
		return ""
	}
	for _, r := range email {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return email
}

func storageKeyForIdentity(descriptor authDescriptor) string {
	return string(descriptor.Kind) + "-" + hashString(canonicalIdentity(descriptor.Identity))
}

func canonicalIdentity(identity authIdentity) string {
	if identity.AccountID != "" {
		return "chatgpt\x00" + identity.AccountID
	}
	return "apikey\x00" + identity.APIKeyHash
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (m Manager) ensureIdentityNotSaved(identity authIdentity) error {
	profiles, err := scanProfiles(m.ProfileDir)
	if err != nil {
		return err
	}
	metadata, err := m.loadProfileMetadata()
	if err != nil {
		return err
	}
	applyProfileMetadata(profiles.Profiles, metadata)
	for _, profile := range profiles.Profiles {
		if profile.identity.sameLogicalIdentity(identity) {
			return fmt.Errorf("same auth already exists as profile %q", profile.Label)
		}
	}
	return nil
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
	return a.sameLogicalIdentity(other)
}

func (a authIdentity) sameLogicalIdentity(other authIdentity) bool {
	if a.AccountID != "" || other.AccountID != "" {
		return a.AccountID != "" && a.AccountID == other.AccountID
	}
	return a.APIKeyHash != "" && a.APIKeyHash == other.APIKeyHash
}

func (a authIdentity) hasUsableIdentity() bool {
	return a.AccountID != "" || a.APIKeyHash != ""
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
