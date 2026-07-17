package releasecontrol

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanForMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode Mode
		want []Action
	}{
		{ModeMigrationOnly, []Action{ActionInstallMigrate, ActionMigrate}},
		{ModeWorkersOffCurrent, []Action{ActionVerifyInstalledSHA, ActionWorkersOff, ActionRestart, ActionHealth}},
		{ModeServerDark, []Action{ActionRequireWorkersOff, ActionInstallServer, ActionWriteManifest, ActionRestart, ActionHealth}},
		{ModeWorkersOnInstalled, []Action{ActionVerifyInstalledSHA, ActionWorkersOn, ActionRestart, ActionHealth}},
		{ModeStandard, []Action{ActionInstallMigrate, ActionMigrate, ActionInstallServer, ActionWriteManifest, ActionRestart, ActionHealth}},
	}

	for _, test := range tests {
		test := test
		t.Run(string(test.mode), func(t *testing.T) {
			t.Parallel()
			plan, err := PlanForMode(test.mode)
			if err != nil {
				t.Fatalf("PlanForMode(%q) error = %v", test.mode, err)
			}
			if !reflect.DeepEqual(plan.Actions, test.want) {
				t.Fatalf("PlanForMode(%q) actions = %v, want %v", test.mode, plan.Actions, test.want)
			}
		})
	}
}

func TestValidateControl(t *testing.T) {
	t.Parallel()
	sha := "0123456789abcdef0123456789abcdef01234567"
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	controlToken := "cutover-20260715@" + digest

	control, err := ValidateControl(ControlInput{
		EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeServerDark,
		ArtifactSHA: sha, RunID: "cutover-20260715", ControlLock: controlToken,
		StageHead: sha, ArtifactReachable: true,
	})
	if err != nil {
		t.Fatalf("valid manual control rejected: %v", err)
	}
	if control.ControlToken != controlToken || control.BaselineDigest != digest {
		t.Fatalf("control output = %+v", control)
	}
	if _, err := ValidateControl(ControlInput{
		EventName: "workflow_dispatch", Ref: "refs/heads/main", Mode: ModeServerDark,
		ArtifactSHA: sha, RunID: "cutover-20260715", ControlLock: controlToken,
		StageHead: sha, ArtifactReachable: true,
	}); err == nil {
		t.Fatal("manual main ref accepted")
	}
	if _, err := ValidateControl(ControlInput{
		EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeServerDark,
		ArtifactSHA: sha[:12], RunID: "cutover-20260715", ControlLock: controlToken,
		StageHead: sha, ArtifactReachable: true,
	}); err == nil {
		t.Fatal("short artifact SHA accepted")
	}
	if _, err := ValidateControl(ControlInput{
		EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeServerDark,
		ArtifactSHA: sha, RunID: "wrong-run", ControlLock: controlToken,
		StageHead: sha, ArtifactReachable: true,
	}); err == nil {
		t.Fatal("manual lock mismatch accepted")
	}

	push, err := ValidateControl(ControlInput{
		EventName: "push", Ref: StageRef, Mode: ModeStandard, ArtifactSHA: sha,
	})
	if err != nil {
		t.Fatalf("unlocked stage push rejected: %v", err)
	}
	if push.Mode != ModeStandard || push.ArtifactSHA != sha {
		t.Fatalf("push output = %+v", push)
	}
	if _, err := ValidateControl(ControlInput{
		EventName: "push", Ref: StageRef, Mode: ModeStandard, ArtifactSHA: sha,
		ControlLock: "cutover-20260715",
	}); err == nil {
		t.Fatal("stage push accepted while cutover lock is set")
	}
}

func TestReleaseControlRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	sha := "0123456789abcdef0123456789abcdef01234567"
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	token := func(run string) string { return run + "@" + digest }
	if mode, err := ParseMode(" standard "); err != nil || mode != ModeStandard {
		t.Fatalf("ParseMode(valid) = %q, %v", mode, err)
	}
	if _, err := ParseMode("unknown"); err == nil {
		t.Fatal("ParseMode accepted unknown mode")
	}
	if _, err := PlanForMode(Mode("unknown")); err == nil {
		t.Fatal("PlanForMode accepted unknown mode")
	}

	tests := []ControlInput{
		{EventName: "push", Ref: "refs/heads/main", Mode: ModeStandard, ArtifactSHA: sha},
		{EventName: "push", Ref: StageRef, Mode: ModeStandard, ArtifactSHA: "ABC"},
		{EventName: "push", Ref: StageRef, Mode: Mode("unknown"), ArtifactSHA: sha},
		{EventName: "push", Ref: StageRef, Mode: ModeServerDark, ArtifactSHA: sha},
		{EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeServerDark, ArtifactSHA: sha, RunID: "bad id", ControlLock: token("bad-id"), StageHead: sha, ArtifactReachable: true},
		{EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeMigrationOnly, ArtifactSHA: sha, RunID: "run-1", ControlLock: token("run-1"), StageHead: sha, ArtifactReachable: true},
		{EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeServerDark, ArtifactSHA: sha, RunID: "run-1", ControlLock: token("run-1"), StageHead: sha},
		{EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeServerDark, ArtifactSHA: sha, RunID: "run-1", ControlLock: token("run-1"), StageHead: "short", ArtifactReachable: true},
		{EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeStandard, ArtifactSHA: sha, RunID: "run-1_FINAL", ControlLock: token("run-1_FINAL"), StageHead: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArtifactReachable: true},
		{EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeServerDark, ArtifactSHA: sha, RunID: "run-1", ControlLock: "run-1", StageHead: sha, ArtifactReachable: true},
		{EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeServerDark, ArtifactSHA: sha, RunID: "run-1", ControlLock: "run-1@" + "A" + digest[1:], StageHead: sha, ArtifactReachable: true},
		{Ref: StageRef, Mode: ModeStandard, ArtifactSHA: sha},
		{EventName: "schedule", Ref: StageRef, Mode: ModeStandard, ArtifactSHA: sha},
	}
	for i, input := range tests {
		if _, err := ValidateControl(input); err == nil {
			t.Errorf("invalid input %d accepted: %+v", i, input)
		}
	}
	if _, err := ValidateControl(ControlInput{
		EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeMigrationOnly,
		ArtifactSHA: sha, RunID: "run-1", ControlLock: token("run-1"), MigrationCeiling: "051",
		StageHead: sha, ArtifactReachable: true,
	}); err != nil {
		t.Fatalf("valid migration-only rejected: %v", err)
	}
	if _, err := ValidateControl(ControlInput{
		EventName: "workflow_dispatch", Ref: StageRef, Mode: ModeStandard,
		ArtifactSHA: sha, RunID: "run-1_FINAL", ControlLock: token("run-1_FINAL"),
		StageHead: sha, ArtifactReachable: true,
	}); err != nil {
		t.Fatalf("valid FINAL standard rejected: %v", err)
	}
}

func TestParseControlToken(t *testing.T) {
	t.Parallel()
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	token, err := ParseControlToken("run-1@" + digest)
	if err != nil {
		t.Fatal(err)
	}
	if token.RunID != "run-1" || token.BaselineDigest != digest || token.Raw != "run-1@"+digest {
		t.Fatalf("token = %+v", token)
	}
	for _, value := range []string{"", "run-1", "bad id@" + digest, "run-1@short", "run-1@" + "A" + digest[1:], "run-1@" + digest + "@extra"} {
		if _, err := ParseControlToken(value); err == nil {
			t.Errorf("ParseControlToken(%q) succeeded", value)
		}
	}
}

func TestManifestRoundTrip(t *testing.T) {
	t.Parallel()
	sha := "0123456789abcdef0123456789abcdef01234567"
	path := filepath.Join(t.TempDir(), "release-manifest.json")
	if err := WriteManifest(path, Manifest{ServerSHA: sha}); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ServerSHA != sha {
		t.Fatalf("manifest SHA = %q", manifest.ServerSHA)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode = %v", info.Mode().Perm())
	}
}

func TestManifestErrors(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if _, err := ReadManifest(filepath.Join(tmp, "missing")); err == nil {
		t.Fatal("missing manifest accepted")
	}
	invalidJSON := filepath.Join(tmp, "invalid-json")
	if err := os.WriteFile(invalidJSON, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(invalidJSON); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	invalidSHA := filepath.Join(tmp, "invalid-sha")
	if err := os.WriteFile(invalidSHA, []byte(`{"server_sha":"short"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(invalidSHA); err == nil {
		t.Fatal("invalid manifest SHA accepted")
	}
	if err := WriteManifest(filepath.Join(tmp, "bad"), Manifest{ServerSHA: "short"}); err == nil {
		t.Fatal("invalid write SHA accepted")
	}
	if err := WriteManifest(filepath.Join(tmp, "missing", "manifest"), Manifest{ServerSHA: "0123456789abcdef0123456789abcdef01234567"}); err == nil {
		t.Fatal("write to missing directory succeeded")
	}
}
