package adaptiveschedule_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// shortDBTickerPattern flags fixed high-frequency time.NewTicker / NewTimer
// cadences that historically kept Neon awake. Background DB workers must use
// adaptiveschedule (or an explicitly approved MaxIdle budget) instead.
var shortDBTickerPattern = regexp.MustCompile(`time\.NewTicker\(\s*((?:\d+)\s*\*\s*time\.(?:Second|Millisecond)|time\.(?:Second|Millisecond)\s*(?:\*\s*\d+)?)`)

// Packages that previously owned second/minute DB polling loops. Maintenance
// of this list is intentional: adding a package means its background path was
// reviewed for adaptive scheduling.
var guardedBackgroundPackages = []string{
	"internal/companyfund",
	"internal/container",
	"internal/fundrouting",
	"internal/wallet/deposit",
	"internal/wallet/pool",
	"internal/wallet/config",
}

func TestNoShortFixedDBTickersInGuardedBackgroundPackages(t *testing.T) {
	root := findModuleRoot(t)
	for _, rel := range guardedBackgroundPackages {
		dir := filepath.Join(root, rel)
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			// Allow the adaptive schedule package itself and explicit comments.
			if strings.Contains(string(body), "adaptiveschedule.") {
				// Still flag raw short tickers even if adaptive is also imported,
				// unless the short ticker is not present.
			}
			matches := shortDBTickerPattern.FindAllStringSubmatch(string(body), -1)
			for _, match := range matches {
				// time.NewTicker(time.Second) etc. are banned in guarded packages.
				t.Errorf("%s: fixed short ticker %q is banned; use adaptiveschedule with MaxIdle>=%s",
					path, match[0], 10*time.Minute)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", rel, err)
		}
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
