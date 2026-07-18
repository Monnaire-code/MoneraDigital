package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const migration057PostgresIntegrationGate = "RUN_MIGRATION_057_POSTGRES_INTEGRATION"

func TestMigration057PostgresIntegration(t *testing.T) {
	if os.Getenv(migration057PostgresIntegrationGate) != "1" {
		t.Skip("set RUN_MIGRATION_057_POSTGRES_INTEGRATION=1 to run isolated PostgreSQL coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when migration 057 PostgreSQL integration is enabled")
	}

	db, schema := newMigration057PostgresFixture(t, databaseURL)
	qualify := func(statement string) string {
		return strings.ReplaceAll(statement, "public.", schema+".")
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var unexpected int64
	if err := tx.QueryRow(qualify(migration057PreflightSQL)).Scan(&unexpected); err != nil {
		t.Fatal(err)
	}
	if unexpected != 0 {
		t.Fatalf("unexpected preflight relation count = %d", unexpected)
	}
	if _, err := tx.Exec(qualify(migration057SchemaSQL)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	caseID := insertMigration057Case(t, db, schema, "routing-v1:one")

	t.Run("routing identity and raw movement slot are unique", func(t *testing.T) {
		if _, err := db.Exec(migration057CaseInsertSQL(schema), "routing-v1:one"); err == nil {
			t.Fatal("expected duplicate routing identity rejection")
		}
		secondCaseID := insertMigration057Case(t, db, schema, "routing-v1:two")
		if _, err := db.Exec(`INSERT INTO `+schema+`.safeheron_transaction_routing_case_sources
			(case_id, safeheron_webhook_event_id, source_line_slot_key, payload_digest, provider_status)
			VALUES ($1, 300, 'slot:0', $2, 'COMPLETED')`, caseID, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO `+schema+`.safeheron_transaction_routing_case_sources
			(case_id, safeheron_webhook_event_id, source_line_slot_key, payload_digest, provider_status)
			VALUES ($1, 300, 'slot:0', $2, 'COMPLETED')`, secondCaseID, strings.Repeat("b", 64)); err == nil {
			t.Fatal("expected duplicate raw movement slot rejection")
		}
	})

	t.Run("command reservation and idempotency are database enforced", func(t *testing.T) {
		var commandID int64
		err := db.QueryRow(`INSERT INTO `+schema+`.safeheron_transaction_routing_case_commands
			(case_id, command_type, initiator, actor_scope, actor_admin_user_id, reason,
			 idempotency_key, request_digest, expected_case_version)
			VALUES ($1, 'ASSIGN_CUSTOMER', 'ADMIN', 'admin:5', 5, 'verified customer ownership',
			        'assign-one', $2, 1)
			RETURNING id`, caseID, strings.Repeat("c", 64)).Scan(&commandID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE `+schema+`.safeheron_transaction_routing_cases
			SET pending_command_id = $1, version = version + 1 WHERE id = $2`, commandID, caseID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO `+schema+`.safeheron_transaction_routing_case_commands
			(case_id, command_type, initiator, actor_scope, actor_admin_user_id, reason,
			 idempotency_key, request_digest, expected_case_version)
			VALUES ($1, 'ASSIGN_CUSTOMER', 'ADMIN', 'admin:5', 5, 'duplicate',
			        'assign-one', $2, 2)`, caseID, strings.Repeat("d", 64)); err == nil {
			t.Fatal("expected command idempotency collision")
		}

		var actionID int64
		if err := db.QueryRow(`INSERT INTO `+schema+`.safeheron_transaction_routing_case_actions
			(command_id, action_type, projection_kind, target_user_id)
			VALUES ($1, 'APPLY_CUSTOMER', 'CUSTOMER', 1) RETURNING id`, commandID).Scan(&actionID); err != nil {
			t.Fatal(err)
		}
		resultTx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := resultTx.Exec(`INSERT INTO `+schema+`.safeheron_transaction_routing_case_results
			(case_id, action_id, projection_kind, deposit_id, result_digest)
			VALUES ($1, $2, 'CUSTOMER', 100, $3)`, caseID, actionID, strings.Repeat("e", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := resultTx.Exec(`UPDATE `+schema+`.safeheron_transaction_routing_cases
			SET decision = 'CUSTOMER', requires_customer_projection = true,
			    customer_user_id = 1, deposit_id = 100
			WHERE id = $1`, caseID); err != nil {
			t.Fatal(err)
		}
		if err := resultTx.Commit(); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM `+schema+`.safeheron_transaction_routing_case_results WHERE action_id=$1`, actionID); err == nil {
			t.Fatal("expected direct routing result deletion rejection")
		}
	})

	t.Run("pending command must belong to the same case", func(t *testing.T) {
		first := insertMigration057Case(t, db, schema, "routing-v1:pending-owner")
		second := insertMigration057Case(t, db, schema, "routing-v1:pending-other")
		var commandID int64
		if err := db.QueryRow(`INSERT INTO `+schema+`.safeheron_transaction_routing_case_commands
			(case_id,command_type,initiator,actor_scope,reason,idempotency_key,request_digest,expected_case_version)
			VALUES ($1,'AUTO_ROUTE','SYSTEM','system:test','cross-case invariant','test-cross-case',$2,1) RETURNING id`, first, strings.Repeat("9", 64)).Scan(&commandID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE `+schema+`.safeheron_transaction_routing_cases SET pending_command_id=$1 WHERE id=$2`, commandID, second); err == nil {
			t.Fatal("expected cross-case pending command rejection")
		}
	})

	t.Run("case check rejects result without compatible decision", func(t *testing.T) {
		invalidCaseID := insertMigration057Case(t, db, schema, "routing-v1:invalid-result")
		if _, err := db.Exec(`UPDATE `+schema+`.safeheron_transaction_routing_cases SET deposit_id = 100 WHERE id = $1`, invalidCaseID); err == nil {
			t.Fatal("expected OPEN case deposit result rejection")
		}
	})

	t.Run("alert intent and each sink delivery are unique", func(t *testing.T) {
		var alertID int64
		if err := db.QueryRow(`INSERT INTO `+schema+`.safeheron_transaction_routing_alerts
			(case_id, alert_type, transition_key, severity)
			VALUES ($1, 'OPEN', 'routing-v1:one:ADDRESS_UNASSIGNED', 'ERROR') RETURNING id`, caseID).Scan(&alertID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO `+schema+`.safeheron_transaction_routing_alerts
			(case_id, alert_type, transition_key, severity)
			VALUES ($1, 'OPEN', 'routing-v1:one:ADDRESS_UNASSIGNED', 'ERROR')`, caseID); err == nil {
			t.Fatal("expected duplicate alert intent rejection")
		}
		fingerprint := strings.Repeat("f", 64)
		var deliveryID int64
		if err := db.QueryRow(`INSERT INTO `+schema+`.safeheron_transaction_routing_alert_deliveries
			(alert_id, sink_kind, recipient_fingerprint)
			VALUES ($1, 'LARK', $2) RETURNING id`, alertID, fingerprint).Scan(&deliveryID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO `+schema+`.safeheron_transaction_routing_alert_deliveries
			(alert_id, sink_kind, recipient_fingerprint)
			VALUES ($1, 'LARK', $2)`, alertID, fingerprint); err == nil {
			t.Fatal("expected duplicate sink delivery rejection")
		}
		if _, err := db.Exec(`INSERT INTO `+schema+`.safeheron_transaction_routing_alert_delivery_attempts
			(delivery_id, attempt_number, attempt_kind, outcome)
			VALUES ($1, 1, 'AUTO', 'IN_PROGRESS')`, deliveryID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE `+schema+`.safeheron_transaction_routing_alert_delivery_attempts
			SET outcome = 'DELIVERY_UNKNOWN', finished_at = now()
			WHERE delivery_id = $1 AND attempt_number = 1`, deliveryID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE `+schema+`.safeheron_transaction_routing_alert_deliveries
			SET status = 'AMBIGUOUS', completed_at = now()
			WHERE id = $1`, deliveryID); err != nil {
			t.Fatal(err)
		}
	})
}

func migration057CaseInsertSQL(schema string) string {
	return `INSERT INTO ` + schema + `.safeheron_transaction_routing_cases
		(routing_identity_key, identity_algorithm_version, provider_transaction_group_key,
		 safeheron_tx_key, raw_coin_key, network_family, normalized_destination, amount,
		 direction, movement_kind, movement_index, duplicate_ordinal, reason_code)
		VALUES ($1, 'safeheron-routing-v1', 'group-1', 'tx-1', 'ETH', 'EVM',
		        '0xabc', 1, 'INFLOW', 'PRINCIPAL', 0, 0, 'ADDRESS_UNASSIGNED')
		RETURNING id`
}

func insertMigration057Case(t *testing.T, db *sql.DB, schema, identity string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(migration057CaseInsertSQL(schema), identity).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func newMigration057PostgresFixture(t *testing.T, databaseURL string) (*sql.DB, string) {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = db.Close() })
	schema := fmt.Sprintf("migration_057_%d", time.Now().UnixNano())
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	fixtureSQL := `
CREATE TABLE ` + schema + `.users (id INTEGER PRIMARY KEY);
CREATE TABLE ` + schema + `.deposits (id INTEGER PRIMARY KEY);
CREATE TABLE ` + schema + `.company_fund_accounts (id BIGINT PRIMARY KEY);
CREATE TABLE ` + schema + `.company_fund_transactions (id BIGINT PRIMARY KEY);
CREATE TABLE ` + schema + `.company_fund_provider_events (
  id BIGINT PRIMARY KEY,
  source_kind VARCHAR(64) NOT NULL
);
CREATE TABLE ` + schema + `.safeheron_webhook_events (
  id INTEGER PRIMARY KEY,
  event_id VARCHAR(256) NOT NULL UNIQUE
);
INSERT INTO ` + schema + `.users VALUES (1);
INSERT INTO ` + schema + `.deposits VALUES (100);
INSERT INTO ` + schema + `.company_fund_accounts VALUES (10);
INSERT INTO ` + schema + `.company_fund_transactions VALUES (200);
INSERT INTO ` + schema + `.safeheron_webhook_events VALUES (300, 'fixture-event-300');`
	if _, err := db.Exec(fixtureSQL); err != nil {
		t.Fatal(err)
	}
	return db, schema
}
