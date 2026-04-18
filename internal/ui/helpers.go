package ui

import profilemgr "codex-manage/internal/profiles"

func isPrintableRune(r rune) bool {
	return r >= 32 && r != 127
}

func formatKeyHint(key, action string) string {
	return keyHintStyle.Render(key) + " " + action
}

func indexOfProfile(xs []profilemgr.ProfileSummary, target string) int {
	for i, x := range xs {
		if x.Name == target {
			return i
		}
	}
	return -1
}
