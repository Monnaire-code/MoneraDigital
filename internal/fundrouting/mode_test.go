package fundrouting

import "testing"

func TestParseModeRequiresExplicitSupportedValue(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want Mode
	}{
		{raw: "capture-only", want: ModeCaptureOnly},
		{raw: " routing-authoritative ", want: ModeRoutingAuthoritative},
	} {
		got, err := ParseMode(test.raw)
		if err != nil || got != test.want {
			t.Fatalf("ParseMode(%q) = %q, %v", test.raw, got, err)
		}
	}
	for _, raw := range []string{"", "legacy", "shadow"} {
		if _, err := ParseMode(raw); err == nil {
			t.Fatalf("ParseMode(%q) error = nil", raw)
		}
	}
}
