package main

import (
	"bytes"
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

type appModel struct {
	codexDir         string
	authFile         string
	profileDir       string
	legacyProfileDir string

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
		codexDir:         codexDir,
		authFile:         filepath.Join(codexDir, "auth.json"),
		profileDir:       filepath.Join(managerDir, "profiles"),
		legacyProfileDir: managerDir,
		status:           "Ready.",
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
		m.mode = modeInput
		m.pendingAction = actionRename
		m.inputValue = m.selectedProfile()
		m.inputPrompt = fmt.Sprintf("Rename profile %q to:", m.selectedProfile())
		m.clearMessages()
		return m, nil

	case "s":
		if !m.authActive {
			m.setError("No active auth.json to save.")
			return m, nil
		}
		m.mode = modeInput
		m.pendingAction = actionSave
		m.inputValue = ""
		m.inputPrompt = "Save current auth as profile:"
		m.clearMessages()
		return m, nil

	case "d":
		if len(m.profiles) == 0 {
			m.setError("No profiles to delete.")
			return m, nil
		}
		m.mode = modeConfirm
		m.pendingAction = actionDelete
		m.confirmPrompt = fmt.Sprintf("Delete profile %q? [y/N]", m.selectedProfile())
		m.clearMessages()
		return m, nil

	case "l":
		if !m.authActive {
			m.setError("Already logged out.")
			return m, nil
		}
		m.mode = modeConfirm
		m.pendingAction = actionLogout
		m.confirmPrompt = "Remove current auth.json and log out? [y/N]"
		m.clearMessages()
		return m, nil

	case "enter":
		if len(m.profiles) == 0 {
			m.setError("No profiles to activate.")
			return m, nil
		}
		name := m.selectedProfile()
		if err := activateProfile(m.authFile, m.profileDir, name); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		if err := m.reload(); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.setStatus(fmt.Sprintf("Activated profile %q.", name))
		return m, nil

	case "F5", "ctrl+r":
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
		m.mode = modeNormal
		m.pendingAction = actionNone
		m.inputPrompt = ""
		m.inputValue = ""
		m.setStatus("Cancelled.")
		return m, nil

	case "enter":
		value := strings.TrimSpace(m.inputValue)
		switch m.pendingAction {
		case actionSave:
			if err := saveCurrentAuth(m.authFile, m.profileDir, value); err != nil {
				m.setError(err.Error())
				return m.exitInput(), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitInput(), nil
			}
			m.setStatus(fmt.Sprintf("Saved current auth as %q.", value))
			return m.exitInput(), nil

		case actionRename:
			oldName := m.selectedProfile()
			if err := renameProfile(m.profileDir, oldName, value); err != nil {
				m.setError(err.Error())
				return m.exitInput(), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitInput(), nil
			}
			m.cursor = indexOf(m.profiles, value)
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.setStatus(fmt.Sprintf("Renamed %q to %q.", oldName, value))
			return m.exitInput(), nil
		}
		return m.exitInput(), nil

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
		m.mode = modeNormal
		m.pendingAction = actionNone
		m.confirmPrompt = ""
		m.setStatus("Cancelled.")
		return m, nil

	case "y":
		switch m.pendingAction {
		case actionDelete:
			name := m.selectedProfile()
			if err := deleteProfile(m.profileDir, name); err != nil {
				m.setError(err.Error())
				return m.exitConfirm(), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitConfirm(), nil
			}
			if m.cursor >= len(m.profiles) && m.cursor > 0 {
				m.cursor--
			}
			m.setStatus(fmt.Sprintf("Deleted profile %q.", name))
			return m.exitConfirm(), nil

		case actionLogout:
			if err := logoutAuth(m.authFile); err != nil {
				m.setError(err.Error())
				return m.exitConfirm(), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitConfirm(), nil
			}
			m.setStatus("Logged out.")
			return m.exitConfirm(), nil
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

	body := fmt.Sprintf(
		"Codex Auth Manager\n\nCurrent profile: %s\nAuth status:     %s\nSaved profiles:  %d\nProfile dir:     %s",
		current,
		authState,
		len(m.profiles),
		m.profileDir,
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
			prefix = "› "
			style = selectedItemStyle
		}

		label := p
		if p == m.currentProfile {
			label += "  • current"
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
		return footerStyle.Render("↑/↓ move • enter activate • r rename • d delete • s save • l logout • ctrl+r refresh • q quit")
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

	cur, err := currentProfile(m.authFile, m.profileDir, m.profiles)
	if err != nil && !errors.Is(err, errNoMatchingProfile) {
		return err
	}
	if errors.Is(err, errNoMatchingProfile) {
		m.currentProfile = ""
	} else {
		m.currentProfile = cur
	}

	return nil
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

func (m *appModel) exitInput() appModel {
	m.mode = modeNormal
	m.pendingAction = actionNone
	m.inputPrompt = ""
	m.inputValue = ""
	return *m
}

func (m *appModel) exitConfirm() appModel {
	m.mode = modeNormal
	m.pendingAction = actionNone
	m.confirmPrompt = ""
	return *m
}

var errNoMatchingProfile = errors.New("no matching saved profile")

func listProfiles(profileDir string) ([]string, error) {
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read profile directory: %w", err)
	}

	var profiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isProfileFilename(name) {
			continue
		}
		profiles = append(profiles, name)
	}

	sort.Strings(profiles)
	return profiles, nil
}

func currentProfile(authFile, profileDir string, profiles []string) (string, error) {
	for _, name := range profiles {
		ok, err := filesEqual(authFile, filepath.Join(profileDir, name))
		if err != nil {
			return "", err
		}
		if ok {
			return name, nil
		}
	}
	return "", errNoMatchingProfile
}

func activateProfile(authFile, profileDir, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("missing profile name")
	}

	src := filepath.Join(profileDir, name)
	if !fileExists(src) {
		return fmt.Errorf("profile %q not found", name)
	}

	if err := copyFile(src, authFile); err != nil {
		return fmt.Errorf("failed to activate profile %q: %w", name, err)
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

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isValidProfileName(name string) bool {
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
	if !isValidProfileName(name) {
		return false
	}
	return !strings.Contains(name, ".tmp-")
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

		src := filepath.Join(legacyDir, name)
		dst := filepath.Join(profileDir, name)

		if samePath(src, dst) || fileExists(dst) {
			continue
		}
		if !isLikelyAuthProfile(src) {
			continue
		}

		if err := moveFile(src, dst); err != nil {
			return fmt.Errorf("failed to migrate profile %q: %w", name, err)
		}
	}

	return nil
}

func isLikelyAuthProfile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return len(data) > 0 && json.Valid(data)
}

func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		return err
	}
	return nil
}

func samePath(a, b string) bool {
	aa := filepath.Clean(a)
	bb := filepath.Clean(b)
	return strings.EqualFold(aa, bb)
}

func isPrintableRune(r rune) bool {
	return r >= 32 && r != 127
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
	baseStyle = lipgloss.NewStyle().
			Padding(1, 2)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(1, 2)

	itemStyle = lipgloss.NewStyle()

	selectedItemStyle = lipgloss.NewStyle().
				Bold(true)

	footerStyle = lipgloss.NewStyle().
			Faint(true)

	statusStyle = lipgloss.NewStyle()

	errorStyle = lipgloss.NewStyle().
			Bold(true)

	emptyStyle = lipgloss.NewStyle().
			Faint(true)
)
