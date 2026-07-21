package deployment

import (
	"os"
	"strings"
	"testing"
)

func TestProductionWorkflowIsSingleStandardPath(t *testing.T) {
	workflowContent, err := os.ReadFile("../../.github/workflows/deploy-backend-prod.yml")
	if err != nil {
		t.Fatalf("read production workflow: %v", err)
	}
	deployScriptContent, err := os.ReadFile("../../scripts/deploy-remote.sh")
	if err != nil {
		t.Fatalf("read remote deploy script: %v", err)
	}
	workflow := string(workflowContent)
	script := string(deployScriptContent)
	for _, forbidden := range []string{
		"migration-only",
		"workers-off-current",
		"server-dark",
		"workers-on-installed",
		"migration-060",
		"release_phase",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("production workflow still exposes multi-mode surface %q", forbidden)
		}
	}
	for _, required := range []string{
		"workflow_dispatch",
		"expected_migration_ceiling",
		"--release-mode standard",
		"--expected-migration-ceiling",
		"--port 8081",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("production workflow missing standard-path marker %q", required)
		}
	}
	if !strings.Contains(script, `only --release-mode standard is supported`) {
		t.Fatal("deploy-remote must reject non-standard release modes")
	}
	if !strings.Contains(script, `standard deploy requires --expected-migration-ceiling`) {
		t.Fatal("deploy-remote must require controlled migration ceiling on standard path")
	}
	if strings.Contains(script, "workers-off-current)") || strings.Contains(script, "server-dark)") {
		t.Fatal("deploy-remote still implements multi-mode case branches")
	}
}

func TestRemoteMigrationUsesOneExactVersion(t *testing.T) {
	content, err := os.ReadFile("../../scripts/deploy-remote.sh")
	if err != nil {
		t.Fatalf("read remote deploy script: %v", err)
	}
	script := string(content)
	if !strings.Contains(script, `-print-ceiling -exact-version "$EXPECTED_MIGRATION_CEILING"`) {
		t.Fatal("remote deployment must inspect the selected exact migration")
	}
	if !strings.Contains(script, `./monera-migrate -exact-version "$EXPECTED_MIGRATION_CEILING"`) {
		t.Fatal("remote deployment must invoke one controlled exact migration version")
	}
}

func TestProductionWorkflowUsesInstalledServerPort(t *testing.T) {
	content, err := os.ReadFile("../../.github/workflows/deploy-backend-prod.yml")
	if err != nil {
		t.Fatalf("read production workflow: %v", err)
	}
	if !strings.Contains(string(content), `--port 8081`) {
		t.Fatal("production workflow must health-check the installed server port")
	}
}
