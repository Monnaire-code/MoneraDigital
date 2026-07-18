package releasecontrol

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDeployRemoteModeTrace(t *testing.T) {
	t.Parallel()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"

	tests := []struct {
		mode string
		want []string
		deny []string
	}{
		{"migration-only", []string{"install-migrate", "migrate"}, []string{"install-server", "env-", "restart", "health"}},
		{"workers-off-current", []string{"verify-installed-sha", "env-workers-off", "stop", "verify-inactive"}, []string{"install-server", "install-migrate", "migrate", "restart", "health"}},
		{"server-dark", []string{"require-workers-off", "install-server", "write-manifest", "restart", "health"}, []string{"install-migrate", "migrate", "env-workers-on"}},
		{"workers-on-installed", []string{"verify-installed-sha", "env-routing-routing-authoritative", "env-workers-on", "restart", "health"}, []string{"install-server", "install-migrate", "migrate"}},
		{"standard", []string{"install-migrate", "migrate", "install-server", "write-manifest", "restart", "health"}, nil},
	}

	for _, test := range tests {
		test := test
		t.Run(test.mode, func(t *testing.T) {
			t.Parallel()
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
			if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\nDATABASE_URL=postgresql://test@localhost/test\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appDir, "release-manifest.json"), []byte(`{"server_sha":"`+sha+`","migration_ceiling":"058","routing_mode":"capture-only","safe_artifact":true}`), 0o600); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("bash", script, "--env", "test", "--release-mode", test.mode, "--artifact-sha", sha, "--expected-migration-ceiling", "A")
			cmd.Env = append(os.Environ(),
				"MONERA_DEPLOY_FAKE=1",
				"MONERA_DEPLOY_APP_DIR="+appDir,
				"MONERA_DEPLOY_SRC="+deployDir,
				"MONERA_DEPLOY_TRACE="+tracePath,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("deploy mode %s failed: %v\n%s", test.mode, err, output)
			}
			traceBytes, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatal(err)
			}
			trace := string(traceBytes)
			last := -1
			for _, token := range test.want {
				index := strings.Index(trace, token)
				if index < 0 {
					t.Errorf("trace missing %q:\n%s", token, trace)
				}
				if index < last {
					t.Errorf("trace token %q is out of order:\n%s", token, trace)
				}
				last = index
			}
			for _, token := range test.deny {
				if strings.Contains(trace, token) {
					t.Errorf("trace contains forbidden %q:\n%s", token, trace)
				}
			}
		})
	}
}

func TestDeployRemoteRejectsStalePackageIdentityBeforeSideEffects(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	sha := "0123456789abcdef0123456789abcdef01234567"
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	deployDir := filepath.Join(tmp, "package")
	tracePath := filepath.Join(tmp, "trace")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(root, "scripts", "deploy-remote.sh"))
	if err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(deployDir, "deploy-remote.sh")
	if err := os.WriteFile(runner, source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "artifact-sha"), []byte(strings.Repeat("a", 40)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", runner, "--env", "test", "--release-mode", "migration-only", "--artifact-sha", sha, "--expected-migration-ceiling", "052")
	cmd.Env = append(os.Environ(),
		"MONERA_DEPLOY_FAKE=1",
		"MONERA_DEPLOY_TEST_REQUIRE_APPROVED_SOURCE=1",
		"MONERA_DEPLOY_APP_DIR="+appDir,
		"MONERA_DEPLOY_SRC="+deployDir,
		"MONERA_DEPLOY_TRACE="+tracePath,
	)
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "artifact identity") {
		t.Fatalf("stale artifact result = %v, %s", err, output)
	}
	if trace, _ := os.ReadFile(tracePath); len(trace) != 0 {
		t.Fatalf("stale artifact reached side effects: %s", trace)
	}
}

func TestControlledReleaseStateRejectsOutOfOrderAndPersistsEveryPhase(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	newSHA := "0123456789abcdef0123456789abcdef01234567"
	oldSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	deployDir := filepath.Join(tmp, "deploy")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=true\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\nDATABASE_URL=postgresql://test@localhost/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "release-manifest.json"), []byte(`{"server_sha":"`+oldSHA+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(expectSuccess bool, args ...string) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts", "deploy-remote.sh")}, args...)...)
		cmd.Env = append(os.Environ(), "MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_ENFORCE_RELEASE_STATE=1", "MONERA_DEPLOY_APP_DIR="+appDir, "MONERA_DEPLOY_SRC="+deployDir)
		output, err := cmd.CombinedOutput()
		if expectSuccess && err != nil {
			t.Fatalf("controlled phase failed: %v\n%s", err, output)
		}
		if !expectSuccess && err == nil {
			t.Fatalf("out-of-order controlled phase succeeded: %s", output)
		}
	}
	run(false, "--env", "test", "--release-mode", "migration-only", "--artifact-sha", newSHA, "--expected-migration-ceiling", "057")
	run(true, "--env", "test", "--release-mode", "migration-only", "--artifact-sha", newSHA, "--expected-migration-ceiling", "056")
	assertFileContent(t, filepath.Join(appDir, "release-state.tsv"), newSHA+"\tmigration-056\n")
	run(true, "--env", "test", "--release-mode", "migration-only", "--artifact-sha", newSHA, "--expected-migration-ceiling", "057")
	run(false, "--env", "test", "--release-mode", "workers-off-current", "--artifact-sha", newSHA, "--installed-server-sha", oldSHA)
	run(true, "--env", "test", "--release-mode", "migration-only", "--artifact-sha", newSHA, "--expected-migration-ceiling", "058")
	run(true, "--env", "test", "--release-mode", "workers-off-current", "--artifact-sha", newSHA, "--installed-server-sha", oldSHA)
	run(true, "--env", "test", "--release-mode", "server-dark", "--artifact-sha", newSHA)
	run(true, "--env", "test", "--release-mode", "workers-on-installed", "--artifact-sha", newSHA)
	assertFileContent(t, filepath.Join(appDir, "release-state.tsv"), newSHA+"\tworkers-on-installed\n")
	assertFileContent(t, filepath.Join(appDir, ".env"), "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=true\nSAFEHERON_TRANSACTION_ROUTING_MODE=routing-authoritative\nDATABASE_URL=postgresql://test@localhost/test\n")
}

func TestDeployRemoteWorkersOffAcceptsLegacyEmbeddedSHAWithoutManifest(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	sha := "0123456789abcdef0123456789abcdef01234567"
	packageSHA := "89abcdef0123456789abcdef0123456789abcdef"
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	tracePath := filepath.Join(tmp, "trace")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=true\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "monera-server"), []byte("legacy-binary-version="+sha+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "deploy-remote.sh"), "--env", "test", "--release-mode", "workers-off-current", "--artifact-sha", packageSHA, "--installed-server-sha", sha)
	cmd.Env = append(os.Environ(),
		"MONERA_DEPLOY_FAKE=1",
		"MONERA_DEPLOY_APP_DIR="+appDir,
		"MONERA_DEPLOY_TRACE="+tracePath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("legacy workers-off failed: %v\n%s", err, output)
	}
	assertFileContent(t, filepath.Join(appDir, ".env"), "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\n")
	assertFileContent(t, filepath.Join(appDir, ".service-state"), "stopped\n")
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(trace), "verify-legacy-embedded-sha") {
		t.Fatalf("legacy provenance verification was not traced:\n%s", trace)
	}
}

func TestDeployRemoteFailureContracts(t *testing.T) {
	t.Parallel()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"

	tests := []struct {
		mode, fail string
		want, deny []string
	}{
		{"migration-only", "migrate", []string{"install-migrate", "migrate"}, []string{"install-server", "env-", "restart"}},
		{"server-dark", "health", []string{"require-workers-off", "install-server", "restart", "health", "fail-closed-server", "stop"}, []string{"migrate", "env-workers-on", "rollback-server"}},
		{"workers-on-installed", "health", []string{"verify-installed-sha", "env-routing-routing-authoritative", "env-workers-on", "restart", "health", "restart"}, []string{"install-server", "migrate"}},
		{"standard", "migrate", []string{"install-migrate", "migrate", "rollback-migrate"}, []string{"install-server", "restart", "health"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.mode+"-"+test.fail, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			appDir := filepath.Join(tmp, "app")
			deployDir := filepath.Join(tmp, "deploy")
			tracePath := filepath.Join(tmp, "trace")
			inspectorPath := writeFakeSchemaInspector(t, tmp, "A", false)
			if err := os.MkdirAll(appDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(deployDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\nDATABASE_URL=postgresql://test@localhost/test\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appDir, "release-manifest.json"), []byte(`{"server_sha":"`+sha+`","migration_ceiling":"058","routing_mode":"capture-only","safe_artifact":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script, "--env", "test", "--release-mode", test.mode, "--artifact-sha", sha, "--expected-migration-ceiling", "A")
			cmd.Env = append(os.Environ(), "MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_FAIL_ACTION="+test.fail, "MONERA_DEPLOY_APP_DIR="+appDir, "MONERA_DEPLOY_SRC="+deployDir, "MONERA_DEPLOY_TRACE="+tracePath, "MONERA_DEPLOY_SCHEMA_INSPECTOR_COMMAND="+inspectorPath)
			if err := cmd.Run(); err == nil {
				t.Fatal("failure injection unexpectedly succeeded")
			}
			traceBytes, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatal(err)
			}
			trace := string(traceBytes)
			last := -1
			for _, token := range test.want {
				index := strings.Index(trace[last+1:], token)
				if index < 0 {
					t.Fatalf("trace missing ordered %q after offset %d:\n%s", token, last, trace)
				}
				last += index + 1
			}
			for _, token := range test.deny {
				if strings.Contains(trace, token) {
					t.Errorf("trace contains forbidden %q:\n%s", token, trace)
				}
			}
		})
	}
}

func TestDeployRemoteRejectsUnsafeStateBeforeSideEffects(t *testing.T) {
	t.Parallel()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name, mode, env, manifest string
	}{
		{"dark-with-workers-on", "server-dark", "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=true\n", `{"server_sha":"` + sha + `"}`},
		{"dark-missing-enabled", "server-dark", "COMPANY_FUND_START_BACKGROUND_WORKERS=false\n", `{"server_sha":"` + sha + `"}`},
		{"dark-invalid-enabled", "server-dark", "COMPANY_FUND_ENABLED=TRUE\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\n", `{"server_sha":"` + sha + `"}`},
		{"dark-duplicate-enabled", "server-dark", "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\n", `{"server_sha":"` + sha + `"}`},
		{"dark-duplicate-workers", "server-dark", "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\n", `{"server_sha":"` + sha + `"}`},
		{"dark-nonnormalized-workers", "server-dark", "COMPANY_FUND_ENABLED=true\n COMPANY_FUND_START_BACKGROUND_WORKERS=false\n", `{"server_sha":"` + sha + `"}`},
		{"workers-off-wrong-sha", "workers-off-current", "COMPANY_FUND_START_BACKGROUND_WORKERS=true\n", `{"server_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`},
		{"workers-on-missing-manifest", "workers-on-installed", "COMPANY_FUND_START_BACKGROUND_WORKERS=false\n", ""},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			appDir := filepath.Join(tmp, "app")
			tracePath := filepath.Join(tmp, "trace")
			if err := os.MkdirAll(appDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte(test.env+"SAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.manifest != "" {
				if err := os.WriteFile(filepath.Join(appDir, "release-manifest.json"), []byte(test.manifest), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", script, "--env", "test", "--release-mode", test.mode, "--artifact-sha", sha)
			cmd.Env = append(os.Environ(), "MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_APP_DIR="+appDir, "MONERA_DEPLOY_TRACE="+tracePath)
			if err := cmd.Run(); err == nil {
				t.Fatal("unsafe state unexpectedly succeeded")
			}
			trace, _ := os.ReadFile(tracePath)
			for _, token := range []string{"install-server", "install-migrate", "env-workers-", "restart", "migrate"} {
				if strings.Contains(string(trace), token) {
					t.Errorf("unsafe state reached %q:\n%s", token, trace)
				}
			}
		})
	}
}

func TestDeployRemoteServerFailureRetainsSafeArtifactAndStops(t *testing.T) {
	t.Parallel()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"
	oldSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	deployDir := filepath.Join(tmp, "deploy")
	serviceFile := filepath.Join(tmp, "monera-digital.service")
	serviceState := filepath.Join(tmp, "service-state")
	tracePath := filepath.Join(tmp, "trace")
	for _, dir := range []string{appDir, deployDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(appDir, ".env"):                  "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\n",
		filepath.Join(appDir, "monera-server"):         "old-server\n",
		filepath.Join(appDir, "release-manifest.json"): `{"server_sha":"` + oldSHA + `"}` + "\n",
		filepath.Join(deployDir, "monera-server.fake"): "new-server\n",
		serviceFile:  "old-unit\n",
		serviceState: "running\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", script, "--env", "test", "--release-mode", "server-dark", "--artifact-sha", sha)
	cmd.Env = append(os.Environ(),
		"MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_FAIL_ACTION=health",
		"MONERA_DEPLOY_APP_DIR="+appDir, "MONERA_DEPLOY_SRC="+deployDir,
		"MONERA_DEPLOY_SERVICE_FILE="+serviceFile, "MONERA_DEPLOY_SERVICE_STATE="+serviceState,
		"MONERA_DEPLOY_TRACE="+tracePath,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("health failure unexpectedly succeeded: %s", output)
	}
	assertFileContent(t, filepath.Join(appDir, "monera-server"), "new-server\n")
	assertFileContent(t, filepath.Join(appDir, "release-manifest.json"), `{"server_sha":"`+sha+`","migration_ceiling":"058","routing_mode":"capture-only","safe_artifact":true}`+"\n")
	assertFileContent(t, serviceState, "stopped\n")
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"fail-closed-server", "daemon-reload", "restart", "health", "stop"} {
		if !strings.Contains(string(trace), token) {
			t.Errorf("rollback trace missing %q:\n%s", token, trace)
		}
	}
	for _, token := range []string{"rollback-server", "restore-service"} {
		if strings.Contains(string(trace), token) {
			t.Errorf("fail-closed trace unexpectedly contains %q:\n%s", token, trace)
		}
	}
}

func TestDeployRemoteWorkersOnRollbackLeavesWorkersOffAndVerifiedOrStopped(t *testing.T) {
	t.Parallel()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	script := filepath.Join(root, "scripts", "deploy-remote.sh")
	sha := "0123456789abcdef0123456789abcdef01234567"
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	serviceState := filepath.Join(tmp, "service-state")
	tracePath := filepath.Join(tmp, "trace")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "release-manifest.json"), []byte(`{"server_sha":"`+sha+`","migration_ceiling":"058","routing_mode":"capture-only","safe_artifact":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "--env", "test", "--release-mode", "workers-on-installed", "--artifact-sha", sha)
	cmd.Env = append(os.Environ(),
		"MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_FAIL_ACTION=health",
		"MONERA_DEPLOY_APP_DIR="+appDir, "MONERA_DEPLOY_SERVICE_STATE="+serviceState,
		"MONERA_DEPLOY_TRACE="+tracePath,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("health failure unexpectedly succeeded: %s", output)
	}
	env, err := os.ReadFile(filepath.Join(appDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(env), "COMPANY_FUND_START_BACKGROUND_WORKERS=false") != 1 || strings.Contains(string(env), "COMPANY_FUND_START_BACKGROUND_WORKERS=true") {
		t.Fatalf("workers rollback did not leave one normalized false assignment:\n%s", env)
	}
	assertFileContent(t, serviceState, "stopped\n")
}

func TestDeployRemoteMigrationAWithoutProvenanceHardStopsAndPreservesServerArtifact(t *testing.T) {
	t.Parallel()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	sha := "0123456789abcdef0123456789abcdef01234567"
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	deployDir := filepath.Join(tmp, "deploy")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{
		".env":                  "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=true\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\nDATABASE_URL=postgresql://test@localhost/test\n",
		"monera-server":         "installed-server\n",
		"release-manifest.json": `{"server_sha":"` + sha + `"}` + "\n",
	}
	for name, content := range before {
		if err := os.WriteFile(filepath.Join(appDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "deploy-remote.sh"), "--env", "test", "--release-mode", "migration-only", "--artifact-sha", sha, "--expected-migration-ceiling", "052")
	inspectorPath := writeFakeSchemaInspector(t, tmp, "A", false)
	cmd.Env = append(os.Environ(), "MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_FAIL_ACTION=migrate", "MONERA_DEPLOY_APP_DIR="+appDir, "MONERA_DEPLOY_SRC="+deployDir, "MONERA_DEPLOY_SCHEMA_INSPECTOR_COMMAND="+inspectorPath)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("migration failure unexpectedly succeeded: %s", output)
	}
	assertFileContent(t, filepath.Join(appDir, "monera-server"), before["monera-server"])
	assertFileContent(t, filepath.Join(appDir, "release-manifest.json"), before["release-manifest.json"])
	assertFileContent(t, filepath.Join(appDir, ".env"), "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=false\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\nDATABASE_URL=postgresql://test@localhost/test\n")
	assertFileContent(t, filepath.Join(appDir, ".service-state"), "stopped\n")
	assertFileContent(t, filepath.Join(appDir, ".schema-marker"), "A recorded=false\n")
	assertFileContent(t, filepath.Join(appDir, ".manual-quiesce-required"), "migration-a-non-atomic unknown state=A recorded=false\n")
}

func TestDeployRemoteMigrationBAtomicFailurePreservesControlledState(t *testing.T) {
	t.Parallel()
	fixture := newMigrationBFailureFixture(t, "true")
	before := fixture.readControlledState(t)
	output, err := fixture.command("atomic").CombinedOutput()
	if err == nil {
		t.Fatalf("atomic migration B failure unexpectedly succeeded: %s", output)
	}
	after := fixture.readControlledState(t)
	for path, want := range before {
		if got := after[path]; got != want {
			t.Errorf("atomic failure changed %s: got %q want %q", path, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.appDir, ".manual-quiesce-required")); !os.IsNotExist(err) {
		t.Fatalf("atomic failure created manual quiesce marker: %v", err)
	}
	trace, err := os.ReadFile(fixture.tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(trace), "migration-b-atomic-failure") || strings.Contains(string(trace), "rollback-") || strings.Contains(string(trace), "stop") || strings.Contains(string(trace), "verify-inactive") {
		t.Fatalf("atomic failure trace does not prove hard stop without rollback:\n%s", trace)
	}
}

func TestDeployRemoteIndeterminateCommitWithSchemaAIsSafeFailure(t *testing.T) {
	fixture := newMigrationBFailureFixture(t, "true")
	if output, err := fixture.commandExit("migration-only", "atomic", "75").CombinedOutput(); err == nil {
		t.Fatalf("indeterminate commit with Schema A succeeded: %s", output)
	}
	trace, err := os.ReadFile(fixture.tracePath)
	if err != nil || !strings.Contains(string(trace), "migration-b-atomic-failure") || strings.Contains(string(trace), "stop") || strings.Contains(string(trace), "migration-b-commit-reconciled") {
		t.Fatalf("indeterminate Schema A trace = %s, %v", trace, err)
	}
}

func TestDeployRemoteMigrationAIndeterminateCommitRequiresSchemaAndProvenance(t *testing.T) {
	for _, testCase := range []struct {
		name, kind string
		wantOK     bool
		wantTrace  string
	}{
		{name: "A and 052 provenance reconcile", kind: "a-reconciled", wantOK: true, wantTrace: "migration-a-commit-reconciled"},
		{name: "A without 052 provenance hard stops", kind: "a-missing", wantTrace: "migration-a-non-atomic-hard-stop"},
		{name: "partial without provenance hard stops", kind: "a-rollback", wantTrace: "migration-a-non-atomic-hard-stop"},
		{name: "unknown inspector hard stops", kind: "inspector-failure", wantTrace: "migration-a-non-atomic-hard-stop"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMigrationBFailureFixture(t, "false")
			output, err := fixture.commandCeiling("migration-only", testCase.kind, "75", "052").CombinedOutput()
			if (err == nil) != testCase.wantOK {
				t.Fatalf("success=%t want=%t output=%s", err == nil, testCase.wantOK, output)
			}
			trace, readErr := os.ReadFile(fixture.tracePath)
			if readErr != nil || !strings.Contains(string(trace), testCase.wantTrace) {
				t.Fatalf("trace=%s err=%v", trace, readErr)
			}
		})
	}
}

func TestDeployRemoteMigrationAOrdinaryPartialStateHardStops(t *testing.T) {
	fixture := newMigrationBFailureFixture(t, "false")
	if output, err := fixture.commandCeiling("migration-only", "a-rollback", "1", "052").CombinedOutput(); err == nil {
		t.Fatalf("ordinary migration A failure succeeded: %s", output)
	}
	trace, err := os.ReadFile(fixture.tracePath)
	if err != nil || !strings.Contains(string(trace), "migration-a-non-atomic-hard-stop") || !strings.Contains(string(trace), "stop") {
		t.Fatalf("rollback trace=%s err=%v", trace, err)
	}
}

func TestDeployRemoteMigrationInspectorUsesOnlyOneCanonicalInstalledDatabaseURL(t *testing.T) {
	fixture := newMigrationBFailureFixture(t, "true")
	before := fixture.readControlledState(t)
	output, err := fixture.command("atomic", "DATABASE_URL=postgresql://wrong@localhost/wrong").CombinedOutput()
	if err == nil {
		t.Fatalf("atomic migration failure unexpectedly succeeded: %s", output)
	}
	after := fixture.readControlledState(t)
	for path, want := range before {
		if after[path] != want {
			t.Fatalf("process DATABASE_URL bypass changed %s: got %q want %q", path, after[path], want)
		}
	}
	trace, err := os.ReadFile(fixture.tracePath)
	if err != nil || !strings.Contains(string(trace), "migration-b-atomic-failure") || strings.Contains(string(trace), "stop") {
		t.Fatalf("installed DATABASE_URL was not authoritative: %s, %v", trace, err)
	}
}

func TestDeployRemoteMigrationInspectorRejectsUnsafeInstalledDatabaseURLAsUnknown(t *testing.T) {
	for _, testCase := range []struct {
		name, environment string
	}{
		{name: "missing", environment: "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=true\n"},
		{name: "duplicate", environment: "DATABASE_URL=postgresql://test@localhost/test\nDATABASE_URL=postgresql://test@localhost/test\n"},
		{name: "export", environment: "export DATABASE_URL=postgresql://test@localhost/test\n"},
		{name: "quoted", environment: "DATABASE_URL=\"postgresql://test@localhost/test\"\n"},
		{name: "command substitution", environment: "DATABASE_URL=$(printf postgresql://test@localhost/test)\n"},
		{name: "whitespace", environment: "DATABASE_URL=postgresql://test@localhost/test extra\n"},
		{name: "not a PostgreSQL URL", environment: "DATABASE_URL=not-a-url\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMigrationBFailureFixture(t, "true")
			if err := os.WriteFile(filepath.Join(fixture.appDir, ".env"), []byte("SAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\n"+testCase.environment), 0o600); err != nil {
				t.Fatal(err)
			}
			output, commandErr := fixture.command("atomic").CombinedOutput()
			if commandErr == nil {
				t.Fatalf("unsafe installed DATABASE_URL unexpectedly classified A: %s", output)
			}
			if strings.Contains(string(output), "postgresql://") {
				t.Fatalf("unsafe DATABASE_URL leaked to output: %s", output)
			}
			trace, err := os.ReadFile(fixture.tracePath)
			if err != nil || !strings.Contains(string(trace), "migration-schema-UNKNOWN-recorded-unknown") || !strings.Contains(string(trace), "stop") {
				t.Fatalf("unsafe installed DATABASE_URL did not fail UNKNOWN: %s, %v", trace, err)
			}
		})
	}
}

func TestDeployRemoteMigrationBNonAtomicFailureHardStopsWithWorkersOff(t *testing.T) {
	t.Parallel()
	fixture := newMigrationBFailureFixture(t, "true")
	before := fixture.readControlledState(t)
	output, err := fixture.command("non-atomic").CombinedOutput()
	if err == nil {
		t.Fatalf("non-atomic migration B failure unexpectedly succeeded: %s", output)
	}
	after := fixture.readControlledState(t)
	for _, path := range []string{"monera-server", "release-manifest.json", ".main-pid", ".invocation-id", ".cutover-lock"} {
		if after[path] != before[path] {
			t.Errorf("non-atomic failure changed %s: got %q want %q", path, after[path], before[path])
		}
	}
	if after[".schema-marker"] != "PARTIAL recorded=false\n" {
		t.Fatalf("non-atomic failure did not preserve partial schema evidence: %q", after[".schema-marker"])
	}
	environment := after[".env"]
	if strings.Count(environment, "COMPANY_FUND_START_BACKGROUND_WORKERS=false") != 1 || strings.Contains(environment, "COMPANY_FUND_START_BACKGROUND_WORKERS=true") {
		t.Fatalf("non-atomic failure did not leave one normalized workers=false:\n%s", environment)
	}
	marker, err := os.ReadFile(filepath.Join(fixture.appDir, ".manual-quiesce-required"))
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != "migration-b-non-atomic invocation-001 state=PARTIAL recorded=false\n" {
		t.Fatalf("manual quiesce marker = %q", marker)
	}
	trace, err := os.ReadFile(fixture.tracePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"migration-b-non-atomic-hard-stop", "stop", "verify-inactive"} {
		if !strings.Contains(string(trace), token) {
			t.Fatalf("non-atomic failure trace missing %q:\n%s", token, trace)
		}
	}
	if strings.Contains(string(trace), "rollback-") || strings.Contains(string(trace), "install-server") {
		t.Fatalf("non-atomic failure trace does not prove hard stop:\n%s", trace)
	}
	if after[".service-state"] != "stopped\n" {
		t.Fatalf("non-atomic failure service state = %q", after[".service-state"])
	}
}

func TestDeployRemoteMigrationBNonAtomicStopAndInactiveFailuresAlarmAndHardStop(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name, injected, alarm, finalState string
	}{
		{name: "stop failure", injected: "MONERA_DEPLOY_FAKE_STOP_FAILURE=1", alarm: "alarm-service-stop-failed", finalState: "running\n"},
		{name: "inactive verification failure", injected: "MONERA_DEPLOY_FAKE_INACTIVE_FAILURE=1", alarm: "alarm-inactive-verification-failed", finalState: "stopped\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMigrationBFailureFixture(t, "true")
			parts := strings.SplitN(testCase.injected, "=", 2)
			output, err := fixture.command("non-atomic", testCase.injected).CombinedOutput()
			if err == nil {
				t.Fatalf("%s unexpectedly succeeded: %s", testCase.name, output)
			}
			trace, readErr := os.ReadFile(fixture.tracePath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(trace), "stop") || !strings.Contains(string(trace), testCase.alarm) {
				t.Fatalf("%s trace lacks stop/alarm (%s=%s):\n%s", testCase.name, parts[0], parts[1], trace)
			}
			if !strings.Contains(string(trace), "verify-inactive") {
				t.Fatalf("%s did not verify inactive state after stop attempt:\n%s", testCase.name, trace)
			}
			assertFileContent(t, filepath.Join(fixture.appDir, ".service-state"), testCase.finalState)
			assertFileContent(t, filepath.Join(fixture.appDir, ".manual-quiesce-required"), "migration-b-non-atomic invocation-001 state=PARTIAL recorded=false\n")
			environment, readErr := os.ReadFile(filepath.Join(fixture.appDir, ".env"))
			if readErr != nil || strings.Count(string(environment), "COMPANY_FUND_START_BACKGROUND_WORKERS=false") != 1 {
				t.Fatalf("%s environment was not fail-closed: %q, %v", testCase.name, environment, readErr)
			}
		})
	}
}

func TestDeployRemoteMigrationInspectorClassificationUsesOneHardStopStateMachine(t *testing.T) {
	for _, testCase := range []struct {
		kind, state, exitCode string
	}{
		{kind: "b-missing", state: "B recorded=false", exitCode: "1"},
		{kind: "inspector-failure", state: "UNKNOWN recorded=unknown", exitCode: "1"},
		{kind: "b-missing", state: "B recorded=false", exitCode: "75"},
		{kind: "non-atomic", state: "PARTIAL recorded=false", exitCode: "75"},
		{kind: "inspector-failure", state: "UNKNOWN recorded=unknown", exitCode: "75"},
	} {
		t.Run(testCase.kind+"-exit-"+testCase.exitCode, func(t *testing.T) {
			fixture := newMigrationBFailureFixture(t, "true")
			if output, err := fixture.commandExit("migration-only", testCase.kind, testCase.exitCode).CombinedOutput(); err == nil {
				t.Fatalf("%s unexpectedly succeeded: %s", testCase.kind, output)
			}
			assertFileContent(t, filepath.Join(fixture.appDir, ".schema-marker"), testCase.state+"\n")
			trace, err := os.ReadFile(fixture.tracePath)
			if err != nil || !strings.Contains(string(trace), "migration-b-non-atomic-hard-stop") || !strings.Contains(string(trace), "stop") || !strings.Contains(string(trace), "verify-inactive") {
				t.Fatalf("%s did not use shared hard-stop state machine: %s, %v", testCase.kind, trace, err)
			}
		})
	}
}

func TestDeployRemoteMigrationBCommittedProvenanceReconcilesFailedProcess(t *testing.T) {
	fixture := newMigrationBFailureFixture(t, "true")
	if output, err := fixture.commandExit("migration-only", "reconciled", "75").CombinedOutput(); err != nil {
		t.Fatalf("committed B reconciliation failed: %v\n%s", err, output)
	}
	trace, err := os.ReadFile(fixture.tracePath)
	if err != nil || !strings.Contains(string(trace), "migration-b-commit-reconciled") || strings.Contains(string(trace), "stop") {
		t.Fatalf("committed B was not reconciled safely: %s, %v", trace, err)
	}
}

func TestDeployRemoteOrdinaryProcessFailureCannotReconcileCommittedSchemaB(t *testing.T) {
	fixture := newMigrationBFailureFixture(t, "true")
	if output, err := fixture.command("reconciled").CombinedOutput(); err == nil {
		t.Fatalf("ordinary failure reconciled B and succeeded: %s", output)
	}
	trace, err := os.ReadFile(fixture.tracePath)
	if err != nil || !strings.Contains(string(trace), "migration-b-unexpected-commit") || strings.Contains(string(trace), "migration-b-commit-reconciled") || strings.Contains(string(trace), "install-server") {
		t.Fatalf("ordinary B failure trace = %s, %v", trace, err)
	}
}

func TestDeployRemotePreRunnerFailuresNeverInvokeSchemaInspector(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		extra string
	}{
		{name: "artifact install failure", extra: "MONERA_DEPLOY_FAIL_ACTION=install-migrate"},
		{name: "print ceiling failure", extra: "MONERA_DEPLOY_FAKE_PRINT_CEILING_EXIT_CODE=1"},
		{name: "ceiling mismatch", extra: "MONERA_DEPLOY_FAKE_MIGRATION_CEILING=052"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newMigrationBFailureFixture(t, "true")
			if output, err := fixture.commandExit("standard", "reconciled", "75", testCase.extra).CombinedOutput(); err == nil {
				t.Fatalf("pre-runner failure succeeded: %s", output)
			}
			if _, err := os.Stat(fixture.inspectorMarker); !os.IsNotExist(err) {
				t.Fatalf("schema inspector was invoked: %v", err)
			}
			trace, err := os.ReadFile(fixture.tracePath)
			if err != nil || strings.Contains(string(trace), "migration-runner-started") || strings.Contains(string(trace), "migration-b-commit-reconciled") || strings.Contains(string(trace), "install-server") {
				t.Fatalf("pre-runner trace = %s, %v", trace, err)
			}
		})
	}
}

func TestDeployRemoteRealInspectorOverrideIsStaticallyForbidden(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	source, err := os.ReadFile(filepath.Join(root, "scripts", "deploy-remote.sh"))
	if err != nil {
		t.Fatal(err)
	}
	realInspector := `local inspector="$APP_DIR/company-fund-release"`
	fakeOnlyOverride := `if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" && -n "${MONERA_DEPLOY_SCHEMA_INSPECTOR_COMMAND:-}" ]]; then`
	if !strings.Contains(string(source), realInspector) || !strings.Contains(string(source), fakeOnlyOverride) {
		t.Fatal("real schema inspector is not fixed to installed company-fund-release")
	}
}

func TestDeployRemoteStandardFailureUsesOrdinaryMigrationRollback(t *testing.T) {
	fixture := newMigrationBFailureFixture(t, "true")
	if output, err := fixture.commandMode("standard", "non-atomic").CombinedOutput(); err == nil {
		t.Fatalf("standard non-atomic failure unexpectedly succeeded: %s", output)
	}
	trace, err := os.ReadFile(fixture.tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(trace), "rollback-migrate") || strings.Contains(string(trace), "install-server") {
		t.Fatalf("standard failure did not use ordinary migration rollback:\n%s", trace)
	}
	if _, err := os.Stat(fixture.inspectorMarker); !os.IsNotExist(err) {
		t.Fatalf("standard failure invoked A/B schema inspector: %v", err)
	}
}

type migrationBFailureFixture struct {
	root, appDir, deployDir, tracePath, inspectorPath, inspectorMarker, sha string
}

func newMigrationBFailureFixture(t *testing.T, workers string) migrationBFailureFixture {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	tmp := t.TempDir()
	fixture := migrationBFailureFixture{
		root: root, appDir: filepath.Join(tmp, "app"), deployDir: filepath.Join(tmp, "deploy"),
		tracePath: filepath.Join(tmp, "trace"), inspectorPath: filepath.Join(tmp, "schema-inspector"), inspectorMarker: filepath.Join(tmp, "inspector-called"), sha: "0123456789abcdef0123456789abcdef01234567",
	}
	for _, dir := range []string{fixture.appDir, fixture.deployDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		".env":                  "COMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=" + workers + "\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\nDATABASE_URL=postgresql://test@localhost/test\n",
		"monera-server":         "v2-server\n",
		"release-manifest.json": `{"server_sha":"` + fixture.sha + `"}` + "\n",
		".schema-marker":        "A\n",
		".cutover-lock":         "lock-owner\n",
		".main-pid":             "1234\n",
		".invocation-id":        "invocation-001\n",
		".service-state":        "running\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(fixture.appDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inspector := `#!/bin/bash
[[ "${DATABASE_URL:-}" == "postgresql://test@localhost/test" ]] || exit 1
[[ -z "${MONERA_DEPLOY_INSPECTOR_MARKER:-}" ]] || : > "$MONERA_DEPLOY_INSPECTOR_MARKER"
case "${MONERA_DEPLOY_MIGRATION_FAILURE_KIND:-}" in
  atomic) printf '{"state":"A","migration_052_recorded":true,"migration_053_recorded":false}\n'; exit 1 ;;
  non-atomic) printf '{"state":"PARTIAL","migration_052_recorded":true,"migration_053_recorded":false}\n'; exit 1 ;;
  b-missing) printf '{"state":"B","migration_052_recorded":true,"migration_053_recorded":false}\n'; exit 1 ;;
  reconciled) printf '{"state":"B","migration_052_recorded":true,"migration_053_recorded":true}\n'; exit 0 ;;
  a-reconciled) printf '{"state":"A","migration_052_recorded":true,"migration_053_recorded":false}\n'; exit 0 ;;
  a-missing) printf '{"state":"A","migration_052_recorded":false,"migration_053_recorded":false}\n'; exit 1 ;;
  a-rollback) printf '{"state":"PARTIAL","migration_052_recorded":false,"migration_053_recorded":false}\n'; exit 1 ;;
  inspector-failure) exit 1 ;;
esac
`
	if err := os.WriteFile(fixture.inspectorPath, []byte(inspector), 0o700); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture migrationBFailureFixture) command(kind string, extraEnvironment ...string) *exec.Cmd {
	return fixture.commandExit("migration-only", kind, "1", extraEnvironment...)
}

func (fixture migrationBFailureFixture) commandMode(mode, kind string, extraEnvironment ...string) *exec.Cmd {
	return fixture.commandExit(mode, kind, "1", extraEnvironment...)
}

func (fixture migrationBFailureFixture) commandExit(mode, kind, exitCode string, extraEnvironment ...string) *exec.Cmd {
	return fixture.commandCeiling(mode, kind, exitCode, "053", extraEnvironment...)
}

func (fixture migrationBFailureFixture) commandCeiling(mode, kind, exitCode, ceiling string, extraEnvironment ...string) *exec.Cmd {
	cmd := exec.Command("bash", filepath.Join(fixture.root, "scripts", "deploy-remote.sh"), "--env", "test", "--release-mode", mode, "--artifact-sha", fixture.sha, "--expected-migration-ceiling", ceiling)
	cmd.Env = append(os.Environ(),
		"MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_FAKE_MIGRATION_EXIT_CODE="+exitCode, "MONERA_DEPLOY_MIGRATION_FAILURE_KIND="+kind,
		"MONERA_DEPLOY_APP_DIR="+fixture.appDir, "MONERA_DEPLOY_SRC="+fixture.deployDir,
		"MONERA_DEPLOY_TRACE="+fixture.tracePath,
		"MONERA_DEPLOY_SCHEMA_INSPECTOR_COMMAND="+fixture.inspectorPath,
		"MONERA_DEPLOY_INSPECTOR_MARKER="+fixture.inspectorMarker,
	)
	cmd.Env = append(cmd.Env, extraEnvironment...)
	return cmd
}

func (fixture migrationBFailureFixture) readControlledState(t *testing.T) map[string]string {
	t.Helper()
	state := make(map[string]string)
	for _, name := range []string{".env", "monera-server", "release-manifest.json", ".schema-marker", ".cutover-lock", ".main-pid", ".invocation-id", ".service-state"} {
		data, err := os.ReadFile(filepath.Join(fixture.appDir, name))
		if err != nil {
			t.Fatal(err)
		}
		state[name] = string(data)
	}
	return state
}

func TestDeployRemoteWorkersOffFailureRestoresExactEnvironmentAndManifest(t *testing.T) {
	t.Parallel()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	sha := "0123456789abcdef0123456789abcdef01234567"
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	environment := "UNRELATED=value\nCOMPANY_FUND_ENABLED=true\nCOMPANY_FUND_START_BACKGROUND_WORKERS=true\nSAFEHERON_TRANSACTION_ROUTING_MODE=capture-only\n"
	manifest := `{"server_sha":"` + sha + `","marker":"before"}` + "\n"
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte(environment), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "release-manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "deploy-remote.sh"), "--env", "test", "--release-mode", "workers-off-current", "--artifact-sha", sha)
	cmd.Env = append(os.Environ(), "MONERA_DEPLOY_FAKE=1", "MONERA_DEPLOY_FAKE_INACTIVE_FAILURE=1", "MONERA_DEPLOY_APP_DIR="+appDir)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("workers-off failure unexpectedly succeeded: %s", output)
	}
	assertFileContent(t, filepath.Join(appDir, ".env"), environment)
	assertFileContent(t, filepath.Join(appDir, "release-manifest.json"), manifest)
	assertFileContent(t, filepath.Join(appDir, ".service-state"), "stopped\n")
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func writeFakeSchemaInspector(t *testing.T, dir, state string, recorded bool) string {
	t.Helper()
	path := filepath.Join(dir, "schema-inspector")
	content := "#!/bin/bash\nprintf '{\"state\":\"" + state + "\",\"migration_052_recorded\":false,\"migration_053_recorded\":" + fmt.Sprintf("%t", recorded) + "}\\n'\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
