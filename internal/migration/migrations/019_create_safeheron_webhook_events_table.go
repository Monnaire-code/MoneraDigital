package migrations

import (
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type CreateSafeheronWebhookEventsTable struct{}

func (m *CreateSafeheronWebhookEventsTable) Version() string {
	return "019"
}

func (m *CreateSafeheronWebhookEventsTable) Description() string {
	return "Create safeheron_webhook_events table for raw webhook event storage"
}

func (m *CreateSafeheronWebhookEventsTable) Up(db *sql.DB) error {
	createTable := `
	CREATE TABLE IF NOT EXISTS safeheron_webhook_events (
		id                SERIAL       PRIMARY KEY,
		event_id          VARCHAR(128) NOT NULL UNIQUE,
		event_type        VARCHAR(64)  NOT NULL,
		safeheron_tx_key  VARCHAR(128),
		customer_ref_id   VARCHAR(128),
		raw_payload       JSONB        NOT NULL,
		process_status    VARCHAR(16)  NOT NULL DEFAULT 'PENDING',
		process_attempts  INT          NOT NULL DEFAULT 0,
		error_message     TEXT,
		received_at       TIMESTAMP    NOT NULL DEFAULT NOW(),
		processed_at      TIMESTAMP
	);
	`
	_, err := db.Exec(createTable)
	if err != nil {
		return fmt.Errorf("failed to create safeheron_webhook_events table: %w", err)
	}

	indexes := `
	CREATE INDEX IF NOT EXISTS idx_webhook_status ON safeheron_webhook_events(process_status);
	CREATE INDEX IF NOT EXISTS idx_webhook_tx_key ON safeheron_webhook_events(safeheron_tx_key);
	CREATE INDEX IF NOT EXISTS idx_webhook_customer_ref ON safeheron_webhook_events(customer_ref_id);
	`
	_, err = db.Exec(indexes)
	if err != nil {
		return fmt.Errorf("failed to create safeheron_webhook_events indexes: %w", err)
	}

	return nil
}

func (m *CreateSafeheronWebhookEventsTable) Down(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS safeheron_webhook_events;`)
	if err != nil {
		return fmt.Errorf("failed to drop safeheron_webhook_events table: %w", err)
	}
	return nil
}

var _ migration.Migration = (*CreateSafeheronWebhookEventsTable)(nil)
