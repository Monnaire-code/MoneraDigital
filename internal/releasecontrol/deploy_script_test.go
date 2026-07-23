package releasecontrol

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDeployRemoteStandardPathTrace(t *testing.T) {
	t.Parallel()
	root := deployScriptRepositoryRoot(t)
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	deployDir := filepath.Join(tmp, "deploy")
	tracePath := filepath.Join(tmp, "trace")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=true\nSAFEHERON_TRANSACTION_ROUTING_MODE=routing-authoritative\nDATABASE_URL=postgresql://test@localhost/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "artifact-sha"), []byte(sha+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", script, "--env", "test", "--release-mode", "standard", "--artifact-sha", sha, "--expected-migration-ceiling", "060")
	cmd.Env = append(os.Environ(),
		"MONERA_DEPLOY_FAKE=1",
		"MONERA_DEPLOY_APP_DIR="+appDir,
		"MONERA_DEPLOY_SRC="+deployDir,
		"MONERA_DEPLOY_TRACE="+tracePath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("standard deploy failed: %v\n%s", err, output)
	}
	traceBytes, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	trace := string(traceBytes)
	for _, token := range []string{"install-migrate", "migrate", "install-server", "write-manifest", "restart", "health"} {
		if !strings.Contains(trace, token) {
			t.Fatalf("standard trace missing %q:\n%s", token, trace)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(appDir, "release-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"migration_ceiling":"060"`) || !strings.Contains(string(manifest), sha) {
		t.Fatalf("manifest = %s", manifest)
	}
}

func TestDeployRemoteRejectsNonStandardModes(t *testing.T) {
	t.Parallel()
	root := deployScriptRepositoryRoot(t)
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"
	for _, mode := range []string{"migration-only", "workers-off-current", "server-dark", "workers-on-installed"} {
		cmd := exec.Command("bash", script, "--env", "test", "--release-mode", mode, "--artifact-sha", sha, "--expected-migration-ceiling", "060")
		cmd.Env = append(os.Environ(), "MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_APP_DIR="+t.TempDir())
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("mode %s was accepted", mode)
		}
		if !strings.Contains(string(output), "only --release-mode standard is supported") {
			t.Fatalf("mode %s rejection = %s", mode, output)
		}
	}
}

func TestDeployRemoteDropsRetiredMultiModeSurface(t *testing.T) {
	t.Parallel()
	root := deployScriptRepositoryRoot(t)
	scriptPath := filepath.Join(root, "scripts", "deploy-remote.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	// Seam: retired cutover helpers must not remain as callable surface.
	for _, forbidden := range []string{
		"require_server_dark_env()",
		"set_workers()",
		"set_routing_mode()",
		"require_release_state()",
		"require_release_start()",
		"write_release_state()",
		"require_safe_dark_manifest()",
		"verify_installed_sha()",
		"classify_failed_migration()",
		"hard_stop_uncertain_migration()",
		"rollback_server_state()",
		"workers-off-current",
		"server-dark",
		"workers-on-installed",
		"migration-only",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("deploy-remote still contains retired multi-mode surface %q", forbidden)
		}
	}
	for _, required := range []string{
		`RELEASE_MODE" == "standard"`,
		"standard deploy requires --expected-migration-ceiling",
		"run_migration()",
		"write_manifest()",
		"install_binary()",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("deploy-remote missing standard-path surface %q", required)
		}
	}
}

func TestDeployRemoteRequiresMigrationCeiling(t *testing.T) {
	t.Parallel()
	root := deployScriptRepositoryRoot(t)
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"
	cmd := exec.Command("bash", script, "--env", "test", "--release-mode", "standard", "--artifact-sha", sha)
	cmd.Env = append(os.Environ(), "MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_APP_DIR="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "expected-migration-ceiling") {
		t.Fatalf("missing ceiling rejection = %v\n%s", err, output)
	}
}

func TestDeployRemoteRejectsStalePackageIdentityBeforeSideEffects(t *testing.T) {
	t.Parallel()
	root := deployScriptRepositoryRoot(t)
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	deployDir := filepath.Join(tmp, "deploy")
	tracePath := filepath.Join(tmp, "trace")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("DATABASE_URL=postgresql://test@localhost/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "artifact-sha"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script,
		"--env", "test",
		"--release-mode", "standard",
		"--artifact-sha", "0123456789abcdef0123456789abcdef01234567",
		"--expected-migration-ceiling", "060",
	)
	cmd.Env = append(os.Environ(),
		"MONERA_DEPLOY_FAKE=1",
		"MONERA_DEPLOY_FAKE_REQUIRE_APPROVED_SOURCE=1",
		"MONERA_DEPLOY_APP_DIR="+appDir,
		"MONERA_DEPLOY_SRC="+deployDir,
		"MONERA_DEPLOY_TRACE="+tracePath,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("stale package identity accepted: %s", output)
	}
	if _, err := os.Stat(tracePath); err == nil {
		t.Fatal("stale package identity performed side effects")
	}
}

func TestDeployRemoteStandardMigrationFailureRollsBackMigrateBinary(t *testing.T) {
	t.Parallel()
	root := deployScriptRepositoryRoot(t)
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	deployDir := filepath.Join(tmp, "deploy")
	tracePath := filepath.Join(tmp, "trace")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("DATABASE_URL=postgresql://test@localhost/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "monera-migrate"), []byte("old-migrate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "artifact-sha"), []byte(sha+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "monera-migrate.gz"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "--env", "test", "--release-mode", "standard", "--artifact-sha", sha, "--expected-migration-ceiling", "060")
	cmd.Env = append(os.Environ(),
		"MONERA_DEPLOY_FAKE=1",
		"MONERA_DEPLOY_FAKE_MIGRATION_EXIT_CODE=1",
		"MONERA_DEPLOY_APP_DIR="+appDir,
		"MONERA_DEPLOY_SRC="+deployDir,
		"MONERA_DEPLOY_TRACE="+tracePath,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("failed migration succeeded: %s", output)
	}
	trace, _ := os.ReadFile(tracePath)
	if !strings.Contains(string(trace), "rollback-migrate") {
		t.Fatalf("expected migrate rollback, trace=%s", trace)
	}
}

// TestDeployRemoteLoadsEnvFileBeforeMigration asserts the deploy runner loads
// /opt/monera-digital/.env into the migrate process environment, so ADR 0003
// variables (MIGRATION_DATABASE_URL, APP_ENV) reach monera-migrate. Without
// this, the merged migrate binary reads neither variable and fails closed.
// The table includes a realistic Neon DSN whose query string contains '&',
// which a naive shell `source` would misparse.
func TestDeployRemoteLoadsEnvFileBeforeMigration(t *testing.T) {
	t.Parallel()
	root := deployScriptRepositoryRoot(t)
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"

	cases := []struct {
		name    string
		envBody string
		wantSub string // substring that must appear in the probe
	}{
		{
			name: "plain_dsn",
			envBody: "DATABASE_URL=postgresql://test@localhost/test\n" +
				"MIGRATION_DATABASE_URL=postgresql://migrator:secret@ep-direct.neon.tech/neondb?sslmode=require\n" +
				"APP_ENV=test\n",
			wantSub: "ep-direct.neon.tech",
		},
		{
			name: "dsn_with_ampersand_query",
			envBody: "DATABASE_URL=postgresql://test@localhost/test\n" +
				"MIGRATION_DATABASE_URL=postgresql://neondb_owner:secret@ep-dawn-surf.neon.tech/neondb?sslmode=require&channel_binding=require\n" +
				"APP_ENV=test\n",
			wantSub: "channel_binding=require",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			appDir := filepath.Join(tmp, "app")
			deployDir := filepath.Join(tmp, "deploy")
			tracePath := filepath.Join(tmp, "trace")
			probePath := filepath.Join(tmp, "probe")
			if err := os.MkdirAll(appDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(deployDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte(tc.envBody), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(deployDir, "artifact-sha"), []byte(sha+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script, "--env", "test", "--release-mode", "standard", "--artifact-sha", sha, "--expected-migration-ceiling", "060")
			cmd.Env = append(os.Environ(),
				"MONERA_DEPLOY_FAKE=1",
				"MONERA_DEPLOY_APP_DIR="+appDir,
				"MONERA_DEPLOY_SRC="+deployDir,
				"MONERA_DEPLOY_TRACE="+tracePath,
				"MONERA_DEPLOY_FAKE_MIGRATION_ENV_PROBE="+probePath,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("deploy failed: %v\n%s", err, output)
			}
			probe, err := os.ReadFile(probePath)
			if err != nil {
				t.Fatalf("env probe not written; .env was not loaded before migrate: %v", err)
			}
			if !strings.Contains(string(probe), tc.wantSub) {
				t.Fatalf("expected %q in migrate env probe; probe=%s", tc.wantSub, probe)
			}
			if !strings.Contains(string(probe), "APP_ENV=test") {
				t.Fatalf("APP_ENV from .env did not reach migrate env; probe=%s", probe)
			}
		})
	}
}

func deployScriptRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
