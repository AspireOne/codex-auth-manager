package main

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
