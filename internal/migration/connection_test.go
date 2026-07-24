package migration

import (
	"strings"
	"testing"
	"time"
)

func TestResolveMigrationDSN_PreferMigrationURL(t *testing.T) {
	got, err := ResolveMigrationDSN(ResolveMigrationDSNInput{
		AppEnv:               "local",
		MigrationDatabaseURL: "postgresql://user:p@ep-direct.example.com/db?sslmode=require",
		DatabaseURL:          "postgresql://user:p@ep-other-pooler.example.com/db?sslmode=require",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ep-direct.example.com") {
		t.Fatalf("expected dedicated URL, got %q", got)
	}
}

func TestResolveMigrationDSN_StageRequiresDedicated(t *testing.T) {
	_, err := ResolveMigrationDSN(ResolveMigrationDSNInput{
		AppEnv:      "stage",
		DatabaseURL: "postgresql://user:secret@ep-direct.example.com/db",
	})
	if err == nil {
		t.Fatal("expected fail closed without MIGRATION_DATABASE_URL on stage")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaked credential material: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "MIGRATION_DATABASE_URL") {
		t.Fatalf("error should mention required var: %v", err)
	}
}

func TestResolveMigrationDSN_ProductionRequiresDedicated(t *testing.T) {
	_, err := ResolveMigrationDSN(ResolveMigrationDSNInput{
		AppEnv:      "production",
		DatabaseURL: "postgresql://user:p@localhost:5432/db",
	})
	if err == nil {
		t.Fatal("expected fail closed on production")
	}
}

func TestResolveMigrationDSN_UnknownAppEnvRequiresDedicated(t *testing.T) {
	_, err := ResolveMigrationDSN(ResolveMigrationDSNInput{
		AppEnv:      "staging",
		DatabaseURL: "postgresql://user:p@localhost:5432/db",
	})
	if err == nil {
		t.Fatal("expected fail closed on staging")
	}
}

func TestResolveMigrationDSN_LocalFallbackDirect(t *testing.T) {
	got, err := ResolveMigrationDSN(ResolveMigrationDSNInput{
		AppEnv:      "local",
		DatabaseURL: "postgresql://user:p@localhost:5432/monera",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "localhost") {
		t.Fatalf("got %q", got)
	}
}

func TestResolveMigrationDSN_RejectPoolerEverywhere(t *testing.T) {
	cases := []ResolveMigrationDSNInput{
		{
			AppEnv:               "local",
			MigrationDatabaseURL: "postgresql://user:hunter2@ep-foo-pooler.c-2.us-east-1.aws.example.com/db",
		},
		{
			AppEnv:      "development",
			DatabaseURL: "postgresql://user:hunter2@EP-FOO-POOLER.test.local/db",
		},
		{
			AppEnv:               "production",
			MigrationDatabaseURL: "postgresql://user:hunter2@ep-x-pooler.example.com/db",
		},
	}
	for _, in := range cases {
		_, err := ResolveMigrationDSN(in)
		if err == nil {
			t.Fatalf("expected pooler reject for env=%s", in.AppEnv)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("leaked password: %v", err)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "pooler") {
			t.Fatalf("expected pooler wording: %v", err)
		}
	}
}

func TestResolveMigrationDSN_AcceptDirectNeon(t *testing.T) {
	got, err := ResolveMigrationDSN(ResolveMigrationDSNInput{
		AppEnv:               "production",
		MigrationDatabaseURL: "postgresql://user:p@ep-foo.us-east-1.aws.example.com/neondb?sslmode=require",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ep-foo.us-east-1.aws.example.com") {
		t.Fatalf("got %q", got)
	}
}

func TestResolveMigrationDSN_MissingBothLocal(t *testing.T) {
	_, err := ResolveMigrationDSN(ResolveMigrationDSNInput{AppEnv: "test"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIsPoolerHostname(t *testing.T) {
	if !IsPoolerHostname("ep-x-pooler.example.com") {
		t.Fatal("expected pooler")
	}
	if !IsPoolerHostname("EP-X-POOLER.example.com:5432") {
		t.Fatal("expected pooler with port")
	}
	if IsPoolerHostname("ep-x.example.com") {
		t.Fatal("direct should not match")
	}
	if IsPoolerHostname("localhost") {
		t.Fatal("localhost")
	}
}

func TestParseAdvisoryLockTimeout(t *testing.T) {
	d, err := ParseAdvisoryLockTimeout("")
	if err != nil || d != DefaultAdvisoryLockTimeout {
		t.Fatalf("default: d=%s err=%v", d, err)
	}
	d, err = ParseAdvisoryLockTimeout("2m")
	if err != nil || d != 2*time.Minute {
		t.Fatalf("2m: d=%s err=%v", d, err)
	}
	if _, err := ParseAdvisoryLockTimeout("nope"); err == nil {
		t.Fatal("expected invalid")
	}
	if _, err := ParseAdvisoryLockTimeout("0s"); err == nil {
		t.Fatal("expected non-positive reject")
	}
}

func TestAdvisoryLockClassAndObj(t *testing.T) {
	c, o := AdvisoryLockClassAndObj(MigrationAdvisoryLockKey)
	if c != 0 || o != int32(MigrationAdvisoryLockKey) {
		t.Fatalf("classid=%d objid=%d", c, o)
	}
}
