package migration

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultAdvisoryLockTimeout is the fail-closed bound for taking the
	// migration session advisory lock (ADR 0003 / issue #35).
	DefaultAdvisoryLockTimeout = 30 * time.Second

	// MigrationAdvisoryLockKey is the session advisory lock used to serialise
	// migrate/rollback. Keep stable for ops muscle memory.
	MigrationAdvisoryLockKey int64 = 8675309
)

// ResolveMigrationDSNInput is the pure env surface for migration connection policy.
type ResolveMigrationDSNInput struct {
	// AppEnv is typically APP_ENV (production, stage, local, development, test).
	AppEnv string
	// MigrationDatabaseURL is MIGRATION_DATABASE_URL (preferred, required on stage/prod).
	MigrationDatabaseURL string
	// DatabaseURL is DATABASE_URL (fallback only outside stage/production).
	DatabaseURL string
}

// ResolveMigrationDSN chooses and validates the DSN used for migrate/rollback.
// It does not open a database connection and never returns password material in errors.
func ResolveMigrationDSN(in ResolveMigrationDSNInput) (string, error) {
	appEnv := strings.ToLower(strings.TrimSpace(in.AppEnv))
	// Fail closed outside local-style envs: production, stage, staging, or any
	// unknown APP_ENV must not fall back to a possibly pooled app DATABASE_URL.
	requireDedicated := !isLocalStyleAppEnv(appEnv)

	raw := strings.TrimSpace(in.MigrationDatabaseURL)
	source := "MIGRATION_DATABASE_URL"
	if raw == "" {
		if requireDedicated {
			label := appEnv
			if label == "" {
				label = "non-local APP_ENV"
			}
			return "", fmt.Errorf("MIGRATION_DATABASE_URL is required on %s (direct/unpooled migration endpoint; do not use the app pooler DATABASE_URL)", label)
		}
		raw = strings.TrimSpace(in.DatabaseURL)
		source = "DATABASE_URL"
		if raw == "" {
			return "", fmt.Errorf("migration database URL is required: set MIGRATION_DATABASE_URL or DATABASE_URL")
		}
	}

	host, err := hostnameFromDatabaseURL(raw)
	if err != nil {
		return "", fmt.Errorf("invalid %s: %w", source, err)
	}
	if IsPoolerHostname(host) {
		return "", fmt.Errorf("%s host %q looks like a connection pooler; migrations require a direct/unpooled endpoint (ADR 0003)", source, redactHost(host))
	}
	return raw, nil
}

func isLocalStyleAppEnv(appEnv string) bool {
	switch appEnv {
	// Empty APP_ENV is treated as local for developer convenience, but pooler
	// hosts are still rejected for whichever DSN is selected.
	case "", "local", "development", "dev", "test":
		return true
	default:
		return false
	}
}

// IsPoolerHostname reports whether host is a Neon-style (or similar) pooler endpoint.
func IsPoolerHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	// Strip optional port for matching.
	if i := strings.LastIndex(h, "]"); i >= 0 && strings.HasPrefix(h, "[") {
		// [::1]:5432
		h = h[1:i]
	} else if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h, "]") {
		// avoid cutting IPv6 without brackets poorly; plain host:port only
		if strings.Count(h, ":") == 1 {
			h = h[:i]
		}
	}
	return strings.Contains(h, "-pooler")
}

// ParseAdvisoryLockTimeout parses MIGRATION_ADVISORY_LOCK_TIMEOUT.
// Empty → default 30s. Invalid non-empty → error (fail closed).
func ParseAdvisoryLockTimeout(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultAdvisoryLockTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid MIGRATION_ADVISORY_LOCK_TIMEOUT %q: %w", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("MIGRATION_ADVISORY_LOCK_TIMEOUT must be positive, got %s", d)
	}
	return d, nil
}

func hostnameFromDatabaseURL(raw string) (string, error) {
	// Support postgresql:// and postgres://
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse URL failed")
	}
	if parsed.Host == "" {
		// Sometimes users pass key=value forms; still reject unparseable for migration policy.
		return "", fmt.Errorf("missing host")
	}
	return parsed.Hostname(), nil
}

func redactHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "(empty)"
	}
	// Host alone is OK to log; never attach userinfo.
	return host
}

// AdvisoryLockClassAndObj splits the bigint advisory key the same way PostgreSQL does
// for pg_advisory_lock(int8) in pg_locks (classid high 32, objid low 32).
func AdvisoryLockClassAndObj(key int64) (classid, objid int32) {
	return int32(key >> 32), int32(key & 0xffffffff)
}
