package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
