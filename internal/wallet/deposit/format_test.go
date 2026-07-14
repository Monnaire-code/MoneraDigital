package deposit

import "testing"

func TestFormatUserID(t *testing.T) {
	tests := []struct {
		id   int
		want string
	}{
		{0, "N/A"},
		{1, "1"},
		{42, "42"},
		{-1, "-1"},
	}
	for _, tt := range tests {
		if got := formatUserID(tt.id); got != tt.want {
			t.Errorf("formatUserID(%d)=%q, want %q", tt.id, got, tt.want)
		}
	}
}
