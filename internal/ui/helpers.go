package ui

import (
	"strings"
	"unicode"

	profilemgr "codex-manage/internal/profiles"
)

func isPrintableRune(r rune) bool {
	return unicode.IsPrint(r)
}

func sanitizeInputText(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {
		switch r {
		case '\r', '\n', '\t':
			b.WriteRune(' ')
		default:
			if isPrintableRune(r) {
				b.WriteRune(r)
			}
		}
	}

	return b.String()
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
