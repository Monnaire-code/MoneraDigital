package deployment

import (
	"os"
	"strings"
	"testing"
)

func TestProductionWorkflowFailsClosedWithoutLegacyBinaryRollback(t *testing.T) {
	workflowContent, err := os.ReadFile("../../.github/workflows/deploy-backend-prod.yml")
	if err != nil {
		t.Fatalf("read production workflow: %v", err)
	}
	deployScriptContent, err := os.ReadFile("../../scripts/deploy-remote.sh")
	if err != nil {
		t.Fatalf("read remote deploy script: %v", err)
	}
	workflow := string(workflowContent)
	controlledRelease := workflow + "\n" + string(deployScriptContent)
	for _, forbidden := range []string{"${BIN}.bak", "Rolled back to backup", "rollback restart"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("production workflow still contains unsafe rollback marker %q", forbidden)
		}
	}
	for _, required := range []string{
		"SAFEHERON_TRANSACTION_ROUTING_MODE",
		"capture-only",
		"routing-authoritative",
		`sudo systemctl stop "$SERVICE_NAME"`,
		`trace "fail-closed-server"`,
	} {
		if !strings.Contains(controlledRelease, required) {
			t.Fatalf("production workflow is missing fail-closed marker %q", required)
		}
	}
}

func TestRemoteMigrationUsesOneExactVersion(t *testing.T) {
	content, err := os.ReadFile("../../scripts/deploy-remote.sh")
	if err != nil {
		t.Fatalf("read remote deploy script: %v", err)
	}
	script := string(content)
	if !strings.Contains(script, `-print-ceiling -exact-version "$EXPECTED_MIGRATION_CEILING"`) {
		t.Fatal("remote deployment must inspect the selected exact migration instead of the artifact-wide ceiling")
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

func TestProductionReleasePersistsAndEnforcesPhaseOrder(t *testing.T) {
	content, err := os.ReadFile("../../scripts/deploy-remote.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, required := range []string{
		"require_release_start",
		`write_release_state "migration-$EXPECTED_MIGRATION_CEILING"`,
		"require_release_state migration-059",
		"require_release_state workers-off-current",
		"require_release_state server-dark",
		"write_release_state workers-on-installed",
		"require_safe_dark_manifest",
		"set_routing_mode routing-authoritative",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("controlled production release is missing %q", required)
		}
	}
}
