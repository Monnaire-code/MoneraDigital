package adaptiveschedule_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// shortDBTickerPattern flags fixed high-frequency time.NewTicker cadences that
// historically kept Neon awake. Background DB workers must use adaptiveschedule.
var shortDBTickerPattern = regexp.MustCompile(`time\.NewTicker\(\s*((?:\d+)\s*\*\s*time\.(?:Second|Millisecond)|time\.(?:Second|Millisecond)\s*(?:\*\s*\d+)?)`)

// fixedMinutePollResetPattern catches legacy timer.Reset(…Minute) / Reset(…Second)
// idle loops (for example company-fund reconciliation before adaptive migration).
var fixedMinutePollResetPattern = regexp.MustCompile(`\.(Reset|NewTicker)\(\s*[^)]*time\.(Second|Minute)\b`)

// bannedIdleLoopNames catches reintroduction of the pre-adaptive recon loop name.
var bannedIdleLoopNames = []string{
	"func (runtime *CompanyFundRuntime) runReconciliationLoop(",
}

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
	"internal/scheduler",
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
			// Lease heartbeats while a claim is held are not idle polls.
			if strings.HasSuffix(path, "provider_event_worker.go") {
				return nil
			}
			bodyBytes, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			body := string(bodyBytes)
			for _, match := range shortDBTickerPattern.FindAllString(body, -1) {
				t.Errorf("%s: fixed short ticker %q is banned; use adaptiveschedule with MaxIdle>=%s",
					path, match, 10*time.Minute)
			}
			// Only enforce Reset/NewTicker second-or-minute pattern on runtime.go
			// recon path and fundrouting workers — allow duration arithmetic elsewhere.
			if strings.Contains(path, "/runtime.go") || strings.Contains(path, "/fundrouting/") {
				for _, match := range fixedMinutePollResetPattern.FindAllString(body, -1) {
					// Adaptive schedule package construction uses durations, not Reset loops.
					if strings.Contains(body, "adaptiveschedule.New") && !strings.Contains(body, "timer.Reset") && !strings.Contains(body, "time.NewTicker") {
						continue
					}
					if strings.Contains(match, "timer.Reset") || strings.Contains(match, "NewTicker") {
						t.Errorf("%s: fixed poll pattern %q is banned; use adaptiveschedule", path, match)
					}
				}
			}
			for _, banned := range bannedIdleLoopNames {
				if strings.Contains(body, banned) {
					t.Errorf("%s: banned idle loop %q reintroduced", path, banned)
				}
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
