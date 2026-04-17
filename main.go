package main

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

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

type mode int

const (
	modeNormal mode = iota
	modeInput
	modeConfirm
)

type action int

const (
	actionNone action = iota
	actionSave
	actionRename
	actionDelete
	actionLogout
)

const currentProfileMarkerName = "current-profile"

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

type appModel struct {
	codexDir           string
	authFile           string
	profileDir         string
	legacyProfileDir   string
	currentProfileFile string

	profiles []string
	cursor   int

	currentProfile string
	authActive     bool

	width  int
	height int

	mode          mode
	pendingAction action
	inputValue    string
	inputPrompt   string
	confirmPrompt string
	status        string
	errText       string

	quitting bool
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get home directory: %v\n", err)
		os.Exit(1)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "application error: %v\n", err)
		os.Exit(1)
	}
}

func newAppModel(home string) appModel {
	codexDir := filepath.Join(home, ".codex")
	managerDir := filepath.Join(codexDir, "auth_manager")
	return appModel{
		codexDir:           codexDir,
		authFile:           filepath.Join(codexDir, "auth.json"),
		profileDir:         filepath.Join(managerDir, "profiles"),
		legacyProfileDir:   managerDir,
		currentProfileFile: filepath.Join(managerDir, currentProfileMarkerName),
		status:             "Ready.",
	}
}

func (m appModel) Init() tea.Cmd {
	return nil
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch m.mode {
		case modeInput:
			return m.updateInput(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		default:
			return m.updateNormal(msg)
		}
	}

	return m, nil
}

func (m appModel) updateNormal(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if err := m.syncTrackedProfile(); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit

	case "up", "k":
		if len(m.profiles) > 0 && m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if len(m.profiles) > 0 && m.cursor < len(m.profiles)-1 {
			m.cursor++
		}
		return m, nil

	case "r":
		if len(m.profiles) == 0 {
			m.setError("No profiles to rename.")
			return m, nil
		}
		return m.enterInput(actionRename, fmt.Sprintf("Rename profile %q to:", m.selectedProfile()), m.selectedProfile()), nil

	case "s":
		if !m.authActive {
			m.setError("No active auth.json to save.")
			return m, nil
		}
		return m.enterInput(actionSave, "Save current auth as profile:", ""), nil

	case "d":
		if len(m.profiles) == 0 {
			m.setError("No profiles to delete.")
			return m, nil
		}
		return m.enterConfirm(actionDelete, fmt.Sprintf("Delete profile %q? [y/N]", m.selectedProfile())), nil

	case "l":
		if !m.authActive {
			m.setError("Already logged out.")
			return m, nil
		}
		return m.enterConfirm(actionLogout, "Remove current auth.json and log out? [y/N]"), nil

	case "enter":
		if len(m.profiles) == 0 {
			m.setError("No profiles to activate.")
			return m, nil
		}
		name := m.selectedProfile()
		if err := m.activateSelectedProfile(name); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.setStatus(fmt.Sprintf("Activated profile %q.", name))
		return m, nil

	case "F5", "ctrl+r":
		if err := m.syncTrackedProfile(); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		if err := m.reload(); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.setStatus("Refreshed.")
		return m, nil
	}

	return m, nil
}

func (m appModel) updateInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.cancelMode(), nil

	case "enter":
		value := strings.TrimSpace(m.inputValue)
		switch m.pendingAction {
		case actionSave:
			if err := saveCurrentAuth(m.authFile, m.profileDir, value); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			marker, err := markerForProfile(m.profileDir, value)
			if err != nil {
				return m.reloadAndExitWithError(err), nil
			}
			if err := writeCurrentProfileMarker(m.currentProfileFile, marker); err != nil {
				return m.reloadAndExitWithError(err), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			m.setStatus(fmt.Sprintf("Saved current auth as %q.", value))
			return m.exitMode(), nil

		case actionRename:
			oldName := m.selectedProfile()
			if err := renameProfile(m.profileDir, oldName, value); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if m.currentProfile == oldName {
				marker, err := markerForProfile(m.profileDir, value)
				if err != nil {
					return m.reloadAndExitWithError(err), nil
				}
				if err := writeCurrentProfileMarker(m.currentProfileFile, marker); err != nil {
					return m.reloadAndExitWithError(err), nil
				}
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			m.cursor = indexOf(m.profiles, value)
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.setStatus(fmt.Sprintf("Renamed %q to %q.", oldName, value))
			return m.exitMode(), nil
		}
		return m.exitMode(), nil

	case "backspace":
		runes := []rune(m.inputValue)
		if len(runes) > 0 {
			m.inputValue = string(runes[:len(runes)-1])
		}
		return m, nil
	}

	// Accept printable characters.
	runes := []rune(msg.String())
	if len(runes) == 1 && isPrintableRune(runes[0]) {
		m.inputValue += string(runes[0])
	}

	return m, nil
}

func (m appModel) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "esc", "n", "enter":
		return m.cancelMode(), nil

	case "y":
		switch m.pendingAction {
		case actionDelete:
			name := m.selectedProfile()
			if err := deleteProfile(m.profileDir, name); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			var markerErr error
			if m.currentProfile == name {
				markerErr = clearCurrentProfileMarker(m.currentProfileFile)
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if m.cursor >= len(m.profiles) && m.cursor > 0 {
				m.cursor--
			}
			if markerErr != nil {
				m.setError(markerErr.Error())
				return m.exitMode(), nil
			}
			m.setStatus(fmt.Sprintf("Deleted profile %q.", name))
			return m.exitMode(), nil

		case actionLogout:
			if err := m.syncTrackedProfile(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if err := logoutAuth(m.authFile); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			markerErr := clearCurrentProfileMarker(m.currentProfileFile)
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if markerErr != nil {
				m.setError(markerErr.Error())
				return m.exitMode(), nil
			}
			m.setStatus("Logged out.")
			return m.exitMode(), nil
		}
	}

	return m, nil
}

func (m appModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	header := m.renderHeader()
	list := m.renderList()
	footer := m.renderFooter()
	status := m.renderStatus()

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", list, "", footer, "", status)
	return tea.NewView(baseStyle.Render(content))
}

func (m appModel) renderHeader() string {
	current := "none"
	if m.authActive {
		if m.currentProfile != "" {
			current = m.currentProfile
		} else {
			current = "custom/unsaved"
		}
	}

	authState := "logged out"
	if m.authActive {
		authState = "active"
	}

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		headerTitle.Render("Codex Auth Manager"),
		"",
		fmt.Sprintf("Current profile: %s", headerValue.Render(current)),
		fmt.Sprintf("Auth status:     %s", currentTag.Render(authState)),
		fmt.Sprintf("Saved profiles:  %s", headerValue.Render(fmt.Sprintf("%d", len(m.profiles)))),
		fmt.Sprintf("Profile dir:     %s", lipgloss.NewStyle().Foreground(mutedColor).Render(m.profileDir)),
	)

	return panelStyle.Render(body)
}

func (m appModel) renderList() string {
	if len(m.profiles) == 0 {
		return emptyStyle.Render("No saved profiles.")
	}

	lines := make([]string, 0, len(m.profiles))
	for i, p := range m.profiles {
		prefix := "  "
		style := itemStyle

		if i == m.cursor {
			prefix = headerTitle.Render("›") + " "
			style = selectedItemStyle
		}

		label := p
		if p == m.currentProfile {
			label += currentTag.Render("  • current")
		}

		lines = append(lines, style.Render(prefix+label))
	}

	return panelStyle.Render(strings.Join(lines, "\n"))
}

func (m appModel) renderFooter() string {
	switch m.mode {
	case modeInput:
		return footerStyle.Render(fmt.Sprintf("%s %s", m.inputPrompt, m.inputValue+"█"))
	case modeConfirm:
		return footerStyle.Render(m.confirmPrompt)
	default:
		profileCommands := []string{
			formatKeyHint("↑/↓", "move"),
			formatKeyHint("enter", "activate"),
			formatKeyHint("r", "rename"),
			formatKeyHint("d", "delete"),
		}
		globalCommands := []string{
			formatKeyHint("s", "save"),
			formatKeyHint("l", "logout"),
			formatKeyHint("ctrl+r", "refresh"),
			formatKeyHint("q", "quit"),
		}

		return lipgloss.JoinVertical(
			lipgloss.Left,
			footerStyle.Render("UI: "+strings.Join(profileCommands, " • ")),
			footerStyle.Render("Global: "+strings.Join(globalCommands, " • ")),
		)
	}
}

func (m appModel) renderStatus() string {
	if m.errText != "" {
		return errorStyle.Render("Error: " + m.errText)
	}
	return statusStyle.Render(m.status)
}

func (m *appModel) reload() error {
	if err := os.MkdirAll(m.profileDir, 0o755); err != nil {
		return fmt.Errorf("failed to create profile directory: %w", err)
	}
	if err := os.MkdirAll(m.legacyProfileDir, 0o755); err != nil {
		return fmt.Errorf("failed to create legacy profile directory: %w", err)
	}
	if err := migrateLegacyProfiles(m.legacyProfileDir, m.profileDir); err != nil {
		return err
	}

	profiles, err := listProfiles(m.profileDir)
	if err != nil {
		return err
	}
	m.profiles = profiles

	if len(m.profiles) == 0 {
		m.cursor = 0
	} else if m.cursor >= len(m.profiles) {
		m.cursor = len(m.profiles) - 1
	}

	m.authActive = fileExists(m.authFile)
	if !m.authActive {
		m.currentProfile = ""
		return nil
	}

	marker, err := resolveCurrentProfileMarker(m.authFile, m.currentProfileFile, m.profileDir, m.profiles)
	if err != nil {
		return err
	}
	m.currentProfile = marker.Name

	return nil
}

func (m *appModel) syncTrackedProfile() error {
	if !m.authActive {
		return nil
	}
	marker, err := resolveCurrentProfileMarker(m.authFile, m.currentProfileFile, m.profileDir, m.profiles)
	if err != nil {
		return err
	}
	if marker.Name == "" {
		return nil
	}
	return syncProfileFromAuth(m.authFile, m.profileDir, marker)
}

func (m *appModel) activateSelectedProfile(name string) error {
	if err := m.syncTrackedProfile(); err != nil {
		return err
	}
	if err := activateProfile(m.authFile, []string{m.profileDir, m.legacyProfileDir}, name); err != nil {
		return err
	}

	marker, err := markerForProfile(m.profileDir, name)
	if err != nil {
		m.currentProfile = ""
		_ = clearCurrentProfileMarker(m.currentProfileFile)
		return err
	}
	if err := writeCurrentProfileMarker(m.currentProfileFile, marker); err != nil {
		m.currentProfile = ""
		_ = clearCurrentProfileMarker(m.currentProfileFile)
		return fmt.Errorf("activated profile %q, but failed to track it: %v", name, err)
	}

	return m.reload()
}

func (m *appModel) selectedProfile() string {
	if len(m.profiles) == 0 || m.cursor < 0 || m.cursor >= len(m.profiles) {
		return ""
	}
	return m.profiles[m.cursor]
}

func (m *appModel) setStatus(s string) {
	m.status = s
	m.errText = ""
}

func (m *appModel) setError(s string) {
	m.errText = s
}

func (m *appModel) clearMessages() {
	m.status = ""
	m.errText = ""
}

func (m *appModel) enterInput(nextAction action, prompt, value string) appModel {
	m.mode = modeInput
	m.pendingAction = nextAction
	m.inputPrompt = prompt
	m.inputValue = value
	m.confirmPrompt = ""
	m.clearMessages()
	return *m
}

func (m *appModel) enterConfirm(nextAction action, prompt string) appModel {
	m.mode = modeConfirm
	m.pendingAction = nextAction
	m.confirmPrompt = prompt
	m.inputPrompt = ""
	m.inputValue = ""
	m.clearMessages()
	return *m
}

func (m *appModel) cancelMode() appModel {
	m.mode = modeNormal
	m.pendingAction = actionNone
	m.inputPrompt = ""
	m.inputValue = ""
	m.confirmPrompt = ""
	m.setStatus("Cancelled.")
	return *m
}

func (m *appModel) exitMode() appModel {
	m.mode = modeNormal
	m.pendingAction = actionNone
	m.inputPrompt = ""
	m.inputValue = ""
	m.confirmPrompt = ""
	return *m
}

func (m *appModel) reloadAndExitWithError(err error) appModel {
	if reloadErr := m.reload(); reloadErr != nil {
		m.setError(reloadErr.Error())
	} else {
		m.setError(err.Error())
	}
	return m.exitMode()
}

var errNoMatchingProfile = errors.New("no matching saved profile")

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
	if err := writeCurrentProfileMarker(filepath.Join(filepath.Dir(profileDir), currentProfileMarkerName), currentProfileMarker{
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
		return errors.New("no auth.json found — nothing to save")
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
	if name == currentProfileMarkerName {
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
	if name == currentProfileMarkerName {
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

func isPrintableRune(r rune) bool {
	return r >= 32 && r != 127
}

func formatKeyHint(key, action string) string {
	return keyHintStyle.Render(key) + " " + action
}

func indexOf(xs []string, target string) int {
	for i, x := range xs {
		if x == target {
			return i
		}
	}
	return -1
}

var (
	accentColor  = lipgloss.Color("#8B5CF6")
	accentSoft   = lipgloss.Color("#C4B5FD")
	successColor = lipgloss.Color("#10B981")
	errorColor   = lipgloss.Color("#F87171")
	mutedColor   = lipgloss.Color("#94A3B8")
	panelBorder  = lipgloss.Color("#A78BFA")
	selectedGlow = lipgloss.Color("#DDD6FE")
	headerTitle  = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	headerValue  = lipgloss.NewStyle().Bold(true).Foreground(accentSoft)
	currentTag   = lipgloss.NewStyle().Foreground(successColor).Bold(true)
	baseStyle    = lipgloss.NewStyle().
			Padding(1, 2)

	panelStyle = lipgloss.NewStyle().
			BorderForeground(panelBorder).
			Border(lipgloss.RoundedBorder()).
			Padding(1, 2)

	itemStyle = lipgloss.NewStyle()

	selectedItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(selectedGlow)

	footerStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	keyHintStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(mutedColor)

	statusStyle = lipgloss.NewStyle().
			Foreground(successColor).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(errorColor)

	emptyStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)
)
