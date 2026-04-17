package profiles

import "testing"

func TestIsValidProfileName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"valid", true},
		{"valid.name", true},
		{"valid_name", true},
		{"valid-name", true},
		{"valid@name", true},
		{"user@example.com", true},
		{"", false},
		{"invalid name", false},
		{"invalid!", false},
		{CurrentProfileMarkerName, false},
	}

	for _, tt := range tests {
		if got := isValidProfileName(tt.name); got != tt.want {
			t.Errorf("isValidProfileName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
