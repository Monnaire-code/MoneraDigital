package releasecontrol

import (
	"strings"
	"testing"
	"time"
)

func TestBuildCutoverRunTemplateUsesExactImmutableInputs(t *testing.T) {
	now := time.Date(2026, 7, 15, 4, 5, 6, 0, time.UTC)
	template, err := BuildCutoverRunTemplate(CutoverRunTemplateInput{
		Now: now, EntropyHex: "12ab34cd",
		CurrentSHA: strings.Repeat("a", 40), ASHA: strings.Repeat("b", 40), V2SHA: strings.Repeat("c", 40), BSHA: strings.Repeat("d", 40),
		ExpectedMigrationCeiling: "052", EvidenceDir: "/tmp/company-fund-evidence",
		Fixture: CutoverFixtureIdentity{TransactionKey: "fixture-tx", OccurrenceKey: "fixture-occurrence"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if template.RunID != "CF_CUTOVER_20260715T040506Z_12ab34cd" || template.CurrentSHA == template.ASHA || template.ASHA == template.V2SHA || template.V2SHA == template.BSHA || template.Fixture.RunID != template.RunID || !template.Fixture.PermanentTest {
		t.Fatalf("template = %#v", template)
	}
	if _, err := ValidateControl(ControlInput{EventName: "push", Ref: StageRef, Mode: ModeStandard, ArtifactSHA: strings.Repeat("a", 40), RunID: template.RunID}); err == nil {
		t.Fatal("ordinary push must not carry a cutover run template")
	}
}

func TestCanonicalAccountPolicyExportIsOrderIndependentAndHashable(t *testing.T) {
	records := []CanonicalAccountPolicyRecord{
		{AccountID: 2, Channel: "SAFEHERON", ProviderAccountKey: "b", Address: "0xdef", NetworkFamily: "EVM", AccountEnabled: true, AssetKey: "ETHEREUM_USDT", PolicyEnabled: true},
		{AccountID: 1, Channel: "SAFEHERON", ProviderAccountKey: "a", Address: "0xabc", NetworkFamily: "EVM", AccountEnabled: true, AssetKey: "ETHEREUM_ETH", PolicyEnabled: false},
	}
	first, err := BuildCanonicalAccountPolicyExport(records)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildCanonicalAccountPolicyExport([]CanonicalAccountPolicyRecord{records[1], records[0]})
	if err != nil || first.SHA256 != second.SHA256 || string(first.JSON) != string(second.JSON) || len(first.SHA256) != 64 {
		t.Fatalf("exports differ: %#v / %#v / %v", first, second, err)
	}
	if _, err := BuildCanonicalAccountPolicyExport([]CanonicalAccountPolicyRecord{{AccountID: 0, Channel: "SAFEHERON", ProviderAccountKey: "a"}}); err == nil {
		t.Fatal("incomplete canonical account accepted")
	}
}

func TestFreezeHashChangeResetsStableWindow(t *testing.T) {
	start := time.Date(2026, 7, 15, 5, 0, 0, 0, time.UTC)
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	samples := []CutoverHashSample{
		{At: start, SHA256: hashA}, {At: start.Add(5 * time.Second), SHA256: hashA},
		{At: start.Add(10 * time.Second), SHA256: hashB}, {At: start.Add(15 * time.Second), SHA256: hashB},
		{At: start.Add(20 * time.Second), SHA256: hashB},
	}
	if _, err := ValidateStableAccountHash(samples, 15*time.Second); err == nil {
		t.Fatal("hash change must reset the stable window")
	}
	result, err := ValidateStableAccountHash(append(samples, CutoverHashSample{At: start.Add(25 * time.Second), SHA256: hashB}), 15*time.Second)
	if err != nil || !result.StableSince.Equal(start.Add(10*time.Second)) || result.SHA256 != hashB {
		t.Fatalf("stable result = %#v, %v", result, err)
	}
}

func TestValidateDrainUsesEffectiveMaxWindowAndThreeTerminalSamples(t *testing.T) {
	exitAt := time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC)
	hash := strings.Repeat("c", 64)
	config := CutoverDrainConfig{ProviderLeaseWindow: 10 * time.Second, SyncLeaseWindow: 15 * time.Second, InFlightWindow: 20 * time.Second}
	evidence := CutoverDrainEvidence{
		OldWorkersWereEnabled: true, OldMainPID: "123", OldInvocationID: "invocation-old", OldWorkersExited: true, OldWorkersExitedAt: exitAt, FrozenAccountHash: hash,
		Samples: []CutoverDrainSample{
			{At: exitAt.Add(10 * time.Second), ProviderLeases: 1, AccountHash: hash},
			{At: exitAt.Add(15 * time.Second), AccountHash: hash},
			{At: exitAt.Add(20 * time.Second), AccountHash: hash},
			{At: exitAt.Add(25 * time.Second), AccountHash: hash},
		},
	}
	result, err := ValidateCutoverDrain(config, evidence)
	if err != nil || result.MaxWindow != 20*time.Second || result.TerminalZeroSamples != 3 {
		t.Fatalf("drain = %#v, %v", result, err)
	}
	evidence.Samples[len(evidence.Samples)-1].OldAppSessions = 1
	if _, err := ValidateCutoverDrain(config, evidence); err == nil {
		t.Fatal("terminal old session must block drain")
	}
}

func TestFixtureContractsKeepExactScopeAndPermanentTestMarker(t *testing.T) {
	seed, err := BuildCutoverFixtureSeed(CutoverFixtureSeedInput{
		RunID: "CF_CUTOVER_20260715T040506Z_12ab34cd", TransactionKey: "fixture-tx", OccurrenceKey: "fixture-occ", EventOneID: "event-1", EventTwoID: "event-2",
	})
	if err != nil || !seed.PermanentTest || seed.Scope != seed.RunID+"/"+seed.TransactionKey+"/"+seed.OccurrenceKey {
		t.Fatalf("seed = %#v, %v", seed, err)
	}
	if err := VerifyCutoverFixture(seed, CutoverFixtureObservation{WorkersEnabled: false, PendingEvents: 2, ClaimedEvents: 0, Transactions: 0}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCutoverFixture(seed, CutoverFixtureObservation{WorkersEnabled: true, PendingEvents: 0, ClaimedEvents: 2, Transactions: 1, Movements: 1}); err != nil {
		t.Fatal(err)
	}
	mark := MarkCutoverFixtureTest(seed)
	if !mark.PermanentTest || mark.AllowDelete || mark.AllowMerge {
		t.Fatalf("fixture mark = %#v", mark)
	}
	if err := ValidateCutoverFixtureRetry(seed.RunID, seed.RunID); err == nil {
		t.Fatal("fixture retry must use a new run id")
	}
}

func TestReleaseFailureMatrixCoversAllControlledFailures(t *testing.T) {
	matrix := CompanyFundReleaseFailureMatrix()
	want := []string{"migration-a", "workers-off", "server-dark", "workers-on-health", "migration-b-atomic", "migration-b-non-atomic"}
	if len(matrix) != len(want) {
		t.Fatalf("failure matrix = %#v", matrix)
	}
	for index, id := range want {
		if matrix[index].ID != id {
			t.Fatalf("failure[%d] = %#v", index, matrix[index])
		}
	}
	atomic := matrix[4]
	if !atomic.ServerUnchanged || !atomic.EnvironmentUnchanged || !atomic.InvocationUnchanged || !atomic.SchemaARetained || !atomic.SameV2Retained || !atomic.RetainControlLock {
		t.Fatalf("migration B atomic failure contract = %#v", atomic)
	}
	nonAtomic := matrix[5]
	if !nonAtomic.ServerUnchanged || !nonAtomic.ServiceStopped || !nonAtomic.WorkersQuiesced || !nonAtomic.InvocationUnchanged || !nonAtomic.SameV2Retained || !nonAtomic.RetainControlLock || !nonAtomic.ManualQuiesceRequired {
		t.Fatalf("migration B failure contracts = %#v / %#v", matrix[4], matrix[5])
	}
}

func TestReleaseEvidenceRejectsSecrets(t *testing.T) {
	if err := ValidateReleaseEvidenceJSON([]byte(`{"run_id":"CF_CUTOVER_20260715T040506Z_12ab34cd","counts":{"leases":0}}`)); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`{"token":"secret"}`, `{"webhook":"https://example.invalid"}`, `{"signature":"abc"}`,
		`{"credential":"abc"}`, `{"private_key":"abc"}`, `{"dsn":"postgres://user:pass@host/db"}`,
	} {
		if err := ValidateReleaseEvidenceJSON([]byte(payload)); err == nil {
			t.Fatalf("secret-bearing evidence accepted: %s", payload)
		}
	}
}

func TestBuildCutoverRunTemplateRejectsIncompleteControlInputs(t *testing.T) {
	valid := CutoverRunTemplateInput{
		Now: time.Date(2026, 7, 15, 4, 5, 6, 0, time.UTC), EntropyHex: "12ab34cd",
		CurrentSHA: strings.Repeat("a", 40), ASHA: strings.Repeat("b", 40), V2SHA: strings.Repeat("c", 40), BSHA: strings.Repeat("d", 40),
		ExpectedMigrationCeiling: "052", EvidenceDir: "/tmp/company-fund-evidence",
		Fixture: CutoverFixtureIdentity{TransactionKey: "fixture-tx", OccurrenceKey: "fixture-occurrence"},
	}
	mutations := []func(*CutoverRunTemplateInput){
		func(input *CutoverRunTemplateInput) { input.Now = time.Time{} },
		func(input *CutoverRunTemplateInput) { input.Now = input.Now.In(time.FixedZone("offset", 3600)) },
		func(input *CutoverRunTemplateInput) { input.CurrentSHA = "short" },
		func(input *CutoverRunTemplateInput) { input.ASHA = "short" },
		func(input *CutoverRunTemplateInput) { input.V2SHA = "short" },
		func(input *CutoverRunTemplateInput) { input.BSHA = "short" },
		func(input *CutoverRunTemplateInput) { input.ExpectedMigrationCeiling = "migration A" },
		func(input *CutoverRunTemplateInput) { input.ExpectedMigrationCeiling = "053" },
		func(input *CutoverRunTemplateInput) { input.ASHA = input.CurrentSHA },
		func(input *CutoverRunTemplateInput) { input.ASHA = input.V2SHA },
		func(input *CutoverRunTemplateInput) { input.CurrentSHA = input.V2SHA },
		func(input *CutoverRunTemplateInput) { input.BSHA = input.V2SHA },
		func(input *CutoverRunTemplateInput) { input.EntropyHex = "ABCDEF12" },
		func(input *CutoverRunTemplateInput) { input.EntropyHex = "abc" },
		func(input *CutoverRunTemplateInput) { input.Fixture.TransactionKey = "" },
		func(input *CutoverRunTemplateInput) { input.Fixture.OccurrenceKey = "" },
		func(input *CutoverRunTemplateInput) { input.EvidenceDir = "relative" },
		func(input *CutoverRunTemplateInput) { input.EvidenceDir = "/" },
	}
	for index, mutate := range mutations {
		input := valid
		mutate(&input)
		if _, err := BuildCutoverRunTemplate(input); err == nil {
			t.Fatalf("invalid template input %d accepted", index)
		}
	}
	valid.EntropyHex = ""
	if _, err := BuildCutoverRunTemplate(valid); err != nil {
		t.Fatalf("generated entropy template failed: %v", err)
	}
}

func TestCutoverAccountFreezeAndDrainRejectUnsafeEvidence(t *testing.T) {
	start := time.Date(2026, 7, 15, 5, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	validSamples := []CutoverHashSample{{At: start, SHA256: hash}, {At: start.Add(5 * time.Second), SHA256: hash}, {At: start.Add(10 * time.Second), SHA256: hash}}
	if _, err := ValidateStableAccountHash(validSamples[:2], 5*time.Second); err == nil {
		t.Fatal("too few freeze samples accepted")
	}
	if _, err := ValidateStableAccountHash(validSamples, 4*time.Second); err == nil {
		t.Fatal("short freeze window accepted")
	}
	badFirst := append([]CutoverHashSample(nil), validSamples...)
	badFirst[0].At = time.Time{}
	if _, err := ValidateStableAccountHash(badFirst, 5*time.Second); err == nil {
		t.Fatal("zero first timestamp accepted")
	}
	badSpacing := append([]CutoverHashSample(nil), validSamples...)
	badSpacing[1].At = start.Add(time.Second)
	if _, err := ValidateStableAccountHash(badSpacing, 5*time.Second); err == nil {
		t.Fatal("dense freeze samples accepted")
	}
	badHash := append([]CutoverHashSample(nil), validSamples...)
	badHash[1].SHA256 = "short"
	if _, err := ValidateStableAccountHash(badHash, 5*time.Second); err == nil {
		t.Fatal("invalid freeze hash accepted")
	}
	if _, err := ValidateStableAccountHash(validSamples, 15*time.Second); err == nil {
		t.Fatal("incomplete freeze window accepted")
	}

	config := CutoverDrainConfig{ProviderLeaseWindow: 10 * time.Second, SyncLeaseWindow: 5 * time.Second, InFlightWindow: 15 * time.Second}
	valid := CutoverDrainEvidence{OldWorkersWereEnabled: true, OldMainPID: "123", OldInvocationID: "old", OldWorkersExited: true, OldWorkersExitedAt: start, FrozenAccountHash: hash, Samples: []CutoverDrainSample{
		{At: start.Add(15 * time.Second), AccountHash: hash}, {At: start.Add(20 * time.Second), AccountHash: hash}, {At: start.Add(25 * time.Second), AccountHash: hash},
	}}
	mutations := []func(*CutoverDrainEvidence){
		func(e *CutoverDrainEvidence) { e.OldWorkersWereEnabled = false },
		func(e *CutoverDrainEvidence) { e.OldMainPID = "" },
		func(e *CutoverDrainEvidence) { e.OldInvocationID = "" },
		func(e *CutoverDrainEvidence) { e.OldWorkersExited = false },
		func(e *CutoverDrainEvidence) { e.FrozenAccountHash = "short" },
		func(e *CutoverDrainEvidence) { e.Samples = e.Samples[:2] },
		func(e *CutoverDrainEvidence) { e.Samples[0].At = start.Add(-time.Second) },
		func(e *CutoverDrainEvidence) { e.Samples[0].AccountHash = strings.Repeat("b", 64) },
		func(e *CutoverDrainEvidence) { e.Samples[1].At = e.Samples[0].At.Add(time.Second) },
		func(e *CutoverDrainEvidence) {
			e.Samples[0].At, e.Samples[1].At, e.Samples[2].At = start, start.Add(5*time.Second), start.Add(10*time.Second)
		},
		func(e *CutoverDrainEvidence) { e.Samples[2].InFlight = 1 },
	}
	for index, mutate := range mutations {
		evidence := valid
		evidence.Samples = append([]CutoverDrainSample(nil), valid.Samples...)
		mutate(&evidence)
		if _, err := ValidateCutoverDrain(config, evidence); err == nil {
			t.Fatalf("unsafe drain evidence %d accepted", index)
		}
	}
	if _, err := (CutoverDrainConfig{ProviderLeaseWindow: -1}).effectiveMaxWindow(); err == nil {
		t.Fatal("negative drain window accepted")
	}
	if _, err := ValidateCutoverDrain(CutoverDrainConfig{ProviderLeaseWindow: -1}, valid); err == nil {
		t.Fatal("invalid drain config accepted")
	}
	if window, err := (CutoverDrainConfig{}).effectiveMaxWindow(); err != nil || window != 5*time.Minute {
		t.Fatalf("default drain window = %v, %v", window, err)
	}
}

func TestCutoverFixtureAndEvidenceValidationFailureBranches(t *testing.T) {
	validID := "CF_CUTOVER_20260715T040506Z_12ab34cd"
	validSeed, err := BuildCutoverFixtureSeed(CutoverFixtureSeedInput{RunID: validID, TransactionKey: "tx", OccurrenceKey: "occ", EventOneID: "e1", EventTwoID: "e2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []CutoverFixtureSeedInput{
		{}, {RunID: "invalid", TransactionKey: "tx", OccurrenceKey: "occ", EventOneID: "e1", EventTwoID: "e2"},
		{RunID: validID, TransactionKey: "tx", OccurrenceKey: "occ", EventOneID: "same", EventTwoID: "same"},
	} {
		if _, err := BuildCutoverFixtureSeed(input); err == nil {
			t.Fatal("invalid fixture seed accepted")
		}
	}
	badScope := validSeed
	badScope.Scope = "wrong"
	if err := VerifyCutoverFixture(badScope, CutoverFixtureObservation{}); err == nil {
		t.Fatal("invalid fixture scope accepted")
	}
	if err := VerifyCutoverFixture(validSeed, CutoverFixtureObservation{PendingEvents: 1}); err == nil {
		t.Fatal("workers-off fixture mismatch accepted")
	}
	if err := VerifyCutoverFixture(validSeed, CutoverFixtureObservation{WorkersEnabled: true, ClaimedEvents: 2, Transactions: 2, Movements: 2}); err == nil {
		t.Fatal("workers-on duplicate movement accepted")
	}
	nextID := "CF_CUTOVER_20260715T040507Z_89abcdef"
	if err := ValidateCutoverFixtureRetry(validID, nextID); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"bad", nextID}, {validID, "bad"}, {validID, validID}} {
		if err := ValidateCutoverFixtureRetry(pair[0], pair[1]); err == nil {
			t.Fatal("invalid fixture retry accepted")
		}
	}
	if err := ValidateReleaseEvidenceJSON([]byte(`{`)); err == nil {
		t.Fatal("invalid evidence JSON accepted")
	}
	if err := ValidateReleaseEvidenceJSON([]byte(`{"rows":[{"count":1},"safe"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReleaseEvidenceJSON([]byte(`{"material":"-----BEGIN PRIVATE KEY-----"}`)); err == nil {
		t.Fatal("private key evidence accepted")
	}
	if err := ValidateReleaseEvidenceJSON([]byte(`{"rows":[{"password":"secret"}]}`)); err == nil {
		t.Fatal("nested secret evidence accepted")
	}
	for _, id := range []string{"CF_CUTOVER_bad_12ab34cd", "CF_CUTOVER_20260715T040506Z_ABCDEF12", "CF_X_20260715T040506Z_12ab34cd"} {
		if isCutoverRunID(id) {
			t.Fatalf("invalid run id accepted: %s", id)
		}
	}
}
