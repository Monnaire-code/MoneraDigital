package releasecontrol

import "testing"

func TestValidateBootstrapChangedFiles(t *testing.T) {
	t.Parallel()
	if err := ValidateBootstrapChangedFiles(BootstrapD0, []string{StageWorkflowPath}); err != nil {
		t.Fatalf("valid D0 rejected: %v", err)
	}
	if err := ValidateBootstrapChangedFiles(BootstrapB0, []string{
		StageWorkflowPath,
		"scripts/deploy-remote.sh",
		"scripts/db-promote/README.md",
		"cmd/company-fund-release/main.go",
		"cmd/company-fund-release/main_test.go",
		"cmd/migrate/main.go",
		"cmd/server/main.go",
		"docs/company-fund-stage-release-control.md",
		"internal/buildinfo/buildinfo.go",
		"internal/buildinfo/buildinfo_test.go",
		"internal/db/db.go",
		"internal/migration/migrator.go",
		"internal/migration/migrator_ceiling_test.go",
		"internal/releasecontrol/approved_control.go",
		"internal/releasecontrol/approved_control_test.go",
		"internal/releasecontrol/bootstrap.go",
		"internal/releasecontrol/bootstrap_test.go",
		"internal/releasecontrol/deploy_script_test.go",
		"internal/releasecontrol/releasecontrol.go",
		"internal/releasecontrol/releasecontrol_test.go",
		"internal/releasecontrol/workflow_contract_test.go",
	}); err != nil {
		t.Fatalf("valid B0 rejected: %v", err)
	}

	tests := []struct {
		name  string
		phase BootstrapPhase
		paths []string
	}{
		{"d0-business-file", BootstrapD0, []string{StageWorkflowPath, "internal/companyfund/identity.go"}},
		{"b0-business-file", BootstrapB0, []string{StageWorkflowPath, "internal/companyfund/identity.go"}},
		{"b0-migration", BootstrapB0, []string{StageWorkflowPath, "internal/migration/migrations/052_expand.go"}},
		{"missing-workflow", BootstrapB0, []string{"scripts/deploy-remote.sh"}},
		{"duplicate", BootstrapD0, []string{StageWorkflowPath, StageWorkflowPath}},
		{"path-traversal", BootstrapB0, []string{StageWorkflowPath, "../secret"}},
		{"empty", BootstrapD0, nil},
		{"unknown-phase", BootstrapPhase("X"), []string{StageWorkflowPath}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateBootstrapChangedFiles(test.phase, test.paths); err == nil {
				t.Fatal("unsafe changed-file set accepted")
			}
		})
	}
}
