package releasecontrol

import "testing"

func TestValidateApprovedControl(t *testing.T) {
	t.Parallel()
	sha := "0123456789abcdef0123456789abcdef01234567"
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	controlToken := "run-1@" + digest
	started := "run-1@2026-07-15T08:00:01Z@" + digest
	valid := ApprovedControlInput{
		EventName: "workflow_dispatch", Mode: ModeServerDark, ArtifactSHA: sha, RunID: "run-1",
		PreflightControlToken: controlToken, RepositoryControlToken: controlToken, EnvironmentLock: "run-1",
		EnvironmentStarted: started,
		StageHead:          sha, ArtifactReachable: true,
	}
	parsed, err := ValidateApprovedControl(valid)
	if err != nil {
		t.Fatalf("valid approved control rejected: %v", err)
	}
	if parsed.RunID != "run-1" || parsed.BaselineDigest != digest || parsed.StartedAt.IsZero() {
		t.Fatalf("parsed started tuple = %+v", parsed)
	}

	tests := []struct {
		name   string
		mutate func(*ApprovedControlInput)
	}{
		{"preflight-token-changed", func(input *ApprovedControlInput) { input.PreflightControlToken = "other@" + digest }},
		{"repository-token-changed", func(input *ApprovedControlInput) { input.RepositoryControlToken = "other@" + digest }},
		{"invalid-matching-token", func(input *ApprovedControlInput) {
			input.PreflightControlToken = "run-1"
			input.RepositoryControlToken = "run-1"
		}},
		{"environment-lock-changed", func(input *ApprovedControlInput) { input.EnvironmentLock = "other" }},
		{"baseline-digest-changed", func(input *ApprovedControlInput) {
			input.EnvironmentStarted = "run-1@2026-07-15T08:00:01Z@" + "b" + digest[1:]
		}},
		{"tuple-run-mismatch", func(input *ApprovedControlInput) {
			input.EnvironmentStarted = "other@2026-07-15T08:00:01Z@" + digest
		}},
		{"bad-timestamp", func(input *ApprovedControlInput) {
			input.EnvironmentStarted = "run-1@2026-13-15T08:00:01Z@" + digest
		}},
		{"non-z-timestamp", func(input *ApprovedControlInput) {
			input.EnvironmentStarted = "run-1@2026-07-15T08:00:01+08:00@" + digest
		}},
		{"uppercase-digest", func(input *ApprovedControlInput) {
			input.EnvironmentStarted = "run-1@2026-07-15T08:00:01Z@" + "A" + digest[1:]
		}},
		{"artifact-unreachable", func(input *ApprovedControlInput) { input.ArtifactReachable = false }},
		{"invalid-stage-head", func(input *ApprovedControlInput) { input.StageHead = "short" }},
		{"unknown-event", func(input *ApprovedControlInput) { input.EventName = "schedule" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := valid
			test.mutate(&input)
			if _, err := ValidateApprovedControl(input); err == nil {
				t.Fatal("invalid approved control accepted")
			}
		})
	}
}

func TestFinalStandardRequiresExactStageHead(t *testing.T) {
	t.Parallel()
	stageHead := "0123456789abcdef0123456789abcdef01234567"
	ancestor := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	started := "run_FINAL@2026-07-15T08:00:01.123Z@" + digest
	controlToken := "run_FINAL@" + digest
	input := ApprovedControlInput{
		EventName: "workflow_dispatch", Mode: ModeStandard, ArtifactSHA: ancestor, RunID: "run_FINAL",
		PreflightControlToken: controlToken, RepositoryControlToken: controlToken, EnvironmentLock: "run_FINAL",
		EnvironmentStarted: started,
		StageHead:          stageHead, ArtifactReachable: true,
	}
	if _, err := ValidateApprovedControl(input); err == nil {
		t.Fatal("FINAL standard accepted a stage ancestor")
	}
	input.ArtifactSHA = stageHead
	if _, err := ValidateApprovedControl(input); err != nil {
		t.Fatalf("FINAL standard rejected exact stage HEAD: %v", err)
	}
	input.RunID = "run-1"
	input.PreflightControlToken = "run-1@" + digest
	input.RepositoryControlToken = "run-1@" + digest
	input.EnvironmentLock = "run-1"
	input.EnvironmentStarted = "run-1@2026-07-15T08:00:01Z@" + digest
	input.ArtifactSHA = ancestor
	if _, err := ValidateApprovedControl(input); err != nil {
		t.Fatalf("non-FINAL standard rejected reachable ancestor: %v", err)
	}
}

func TestApprovedPushRequiresAllControlsCleared(t *testing.T) {
	t.Parallel()
	if _, err := ValidateApprovedControl(ApprovedControlInput{EventName: "push"}); err != nil {
		t.Fatalf("clear push rejected: %v", err)
	}
	for _, mutate := range []func(*ApprovedControlInput){
		func(input *ApprovedControlInput) {
			input.PreflightControlToken = "run@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		func(input *ApprovedControlInput) {
			input.RepositoryControlToken = "run@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		func(input *ApprovedControlInput) { input.EnvironmentLock = "run" },
		func(input *ApprovedControlInput) { input.EnvironmentStarted = "tuple" },
	} {
		input := ApprovedControlInput{EventName: "push"}
		mutate(&input)
		if _, err := ValidateApprovedControl(input); err == nil {
			t.Fatal("push accepted active release control")
		}
	}
}
