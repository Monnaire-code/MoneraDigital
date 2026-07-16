package companyfund

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSafeheronAliasProductionSurfaceHasNoPatchWriterBypass(t *testing.T) {
	for _, file := range []string{"safeheron_alias_repair.go", "safeheron_alias_repair_repository.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"ApplySafeheronAliasRepair", "SafeheronAliasRepairWriter", "ApplySafeheronAliasPatches"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("production alias repair bypass %q remains in %s", forbidden, file)
			}
		}
	}
}

func TestSafeheronAliasLiveProbeRequiresExactManifestAndNormalizedWorkersOff(t *testing.T) {
	dir := t.TempDir()
	sha := strings.Repeat("a", 40)
	manifest := filepath.Join(dir, "release-manifest.json")
	environment := filepath.Join(dir, ".env")
	if err := os.WriteFile(manifest, []byte(`{"server_sha":"`+sha+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environment, []byte("COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := SafeheronAliasLiveProbe{ManifestPath: manifest, EnvironmentPath: environment, ExpectedV2SHA: sha}
	before, err := CaptureSafeheronAliasLiveProbe(probe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environment, []byte("COMPANY_FUND_START_BACKGROUND_WORKERS=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureSafeheronAliasLiveProbe(probe); err == nil {
		t.Fatal("workers-on live environment accepted")
	}
	if err := os.WriteFile(environment, []byte("COMPANY_FUND_START_BACKGROUND_WORKERS=false\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureSafeheronAliasLiveProbe(probe); err == nil {
		t.Fatal("duplicate workers setting accepted")
	}
	if err := os.WriteFile(environment, []byte("COMPANY_FUND_START_BACKGROUND_WORKERS=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"server_sha":"`+strings.Repeat("b", 40)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureSafeheronAliasLiveProbe(probe); err == nil {
		t.Fatal("wrong installed manifest SHA accepted")
	}
	for _, forged := range []string{
		`{"server_sha":"` + sha + `","server_sha":"` + sha + `"}`,
		`{"server_sha":"` + sha + `","untrusted":"accepted"}`,
	} {
		if err := os.WriteFile(manifest, []byte(forged), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := CaptureSafeheronAliasLiveProbe(probe); err == nil {
			t.Fatalf("forged installed manifest accepted: %s", forged)
		}
	}
	if err := ValidateSafeheronAliasLiveProbeUnchanged(before, before); err != nil {
		t.Fatal(err)
	}
	drifted := before
	drifted.EnvironmentDigest = strings.Repeat("c", 64)
	if err := ValidateSafeheronAliasLiveProbeUnchanged(before, drifted); err == nil {
		t.Fatal("pre/post live probe drift accepted")
	}
}

func TestSafeheronAliasLiveProbeFailsClosedOnUnreadableOrMalformedInputs(t *testing.T) {
	dir := t.TempDir()
	sha := strings.Repeat("a", 40)
	manifest := filepath.Join(dir, "release-manifest.json")
	environment := filepath.Join(dir, ".env")
	probe := SafeheronAliasLiveProbe{ManifestPath: manifest, EnvironmentPath: environment, ExpectedV2SHA: sha}
	if _, err := CaptureSafeheronAliasLiveProbe(SafeheronAliasLiveProbe{}); err == nil {
		t.Fatal("empty live probe accepted")
	}
	if _, err := CaptureSafeheronAliasLiveProbe(probe); err == nil {
		t.Fatal("missing manifest accepted")
	}
	for _, malformed := range []string{
		`[]`, `{}`, `{"server_sha":1}`, `{"server_sha":"` + sha + `"} {}`,
	} {
		if err := os.WriteFile(manifest, []byte(malformed), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := CaptureSafeheronAliasLiveProbe(probe); err == nil {
			t.Fatalf("malformed manifest accepted: %s", malformed)
		}
	}
	if err := os.WriteFile(manifest, []byte(`{"server_sha":"`+sha+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureSafeheronAliasLiveProbe(probe); err == nil {
		t.Fatal("missing environment accepted")
	}
	if err := os.WriteFile(environment, []byte("export COMPANY_FUND_START_BACKGROUND_WORKERS=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureSafeheronAliasLiveProbe(probe); err == nil {
		t.Fatal("exported non-normalized workers setting accepted")
	}
}

func TestSafeheronAliasTimedEvidenceRejectsForgedOrDenseSamples(t *testing.T) {
	evidence := validSafeheronAliasRepairRequest().Evidence
	evidence.AccountHashSamples[1].At = evidence.AccountHashSamples[0].At.Add(time.Second)
	if err := evidence.validate(); err == nil {
		t.Fatal("dense freeze samples accepted")
	}
	evidence = validSafeheronAliasRepairRequest().Evidence
	evidence.DrainSamples[2].InFlight = 1
	if err := evidence.validate(); err == nil {
		t.Fatal("non-zero terminal drain sample accepted")
	}
	evidence = validSafeheronAliasRepairRequest().Evidence
	evidence.AccountHashStableFor = 20 * time.Second
	if err := evidence.validate(); err == nil {
		t.Fatal("incomplete account hash stable window accepted")
	}
	evidence = validSafeheronAliasRepairRequest().Evidence
	evidence.AccountHashStableFor = 20 * time.Second
	evidence.AccountHashSamples[2].At = evidence.AccountHashSamples[0].At.Add(20 * time.Second)
	if err := evidence.validate(); err == nil {
		t.Fatal("incomplete terminal drain window accepted")
	}
}

func TestSafeheronAliasEvidenceFreshnessUsesTrustedClock(t *testing.T) {
	evidence := validSafeheronAliasRepairRequest().Evidence
	last := evidence.DrainSamples[len(evidence.DrainSamples)-1].At
	evidence.AccountHashSamples[len(evidence.AccountHashSamples)-1].At = last
	for _, testCase := range []struct {
		name    string
		now     time.Time
		wantErr bool
	}{
		{name: "age boundary", now: last.Add(SafeheronAliasEvidenceFreshnessWindow)},
		{name: "stale", now: last.Add(SafeheronAliasEvidenceFreshnessWindow + time.Nanosecond), wantErr: true},
		{name: "future account and drain", now: last.Add(-time.Nanosecond), wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateSafeheronAliasEvidenceFreshness(evidence, testCase.now)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("freshness error = %v, wantErr=%t", err, testCase.wantErr)
			}
		})
	}
	if err := validateSafeheronAliasEvidenceFreshness(SafeheronAliasRepairEvidence{}, time.Time{}); err == nil {
		t.Fatal("missing trusted clock and samples accepted")
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*SafeheronAliasRepairEvidence)
	}{
		{name: "account stale only", mutate: func(value *SafeheronAliasRepairEvidence) {
			value.AccountHashSamples[len(value.AccountHashSamples)-1].At = last.Add(-time.Nanosecond)
		}},
		{name: "drain stale only", mutate: func(value *SafeheronAliasRepairEvidence) {
			value.DrainSamples[len(value.DrainSamples)-1].At = last.Add(-time.Nanosecond)
		}},
		{name: "account future only", mutate: func(value *SafeheronAliasRepairEvidence) {
			value.AccountHashSamples[len(value.AccountHashSamples)-1].At = last.Add(SafeheronAliasEvidenceFreshnessWindow + time.Nanosecond)
		}},
		{name: "drain future only", mutate: func(value *SafeheronAliasRepairEvidence) {
			value.DrainSamples[len(value.DrainSamples)-1].At = last.Add(SafeheronAliasEvidenceFreshnessWindow + time.Nanosecond)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := evidence
			candidate.AccountHashSamples = append([]SafeheronAliasHashSample(nil), evidence.AccountHashSamples...)
			candidate.DrainSamples = append([]SafeheronAliasDrainSample(nil), evidence.DrainSamples...)
			testCase.mutate(&candidate)
			if err := validateSafeheronAliasEvidenceFreshness(candidate, last.Add(SafeheronAliasEvidenceFreshnessWindow)); err == nil {
				t.Fatal("independently stale/future final sample accepted")
			}
		})
	}
}
