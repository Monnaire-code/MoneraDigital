package buildinfo

import (
	"strings"
	"testing"
)

func TestApplicationName(t *testing.T) {
	t.Parallel()
	sha := "0123456789abcdef0123456789abcdef01234567"
	if got := ApplicationName(sha, "0123456789abcdef0123456789abcdef"); got != "monera-digital/0123456789ab/0123456789abcdef0123456789abcdef" {
		t.Fatalf("ApplicationName() = %q", got)
	}
	if got := ApplicationName("dev", "unsafe/id"); got != "monera-digital/dev/unknown" {
		t.Fatalf("ApplicationName(dev) = %q", got)
	}
}

func TestDatabaseURL(t *testing.T) {
	t.Parallel()
	sha := "0123456789abcdef0123456789abcdef01234567"
	got, err := DatabaseURL("postgresql://user:pass@db.example/monera?sslmode=require", sha, "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	want := "application_name=monera-digital%2F0123456789ab%2F0123456789abcdef0123456789abcdef"
	if !strings.Contains(got, want) {
		t.Fatalf("DatabaseURL() = %q, missing %q", got, want)
	}
}

func TestDatabaseURLRejectsInvalidURL(t *testing.T) {
	t.Parallel()
	if _, err := DatabaseURL("postgresql://db/%zz", "dev", ""); err == nil {
		t.Fatal("DatabaseURL accepted an invalid URL escape")
	}
}
