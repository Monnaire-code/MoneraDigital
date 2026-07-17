package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type CreateSafeheronRoutingCases struct{}

func (*CreateSafeheronRoutingCases) Version() string { return "057" }

func (*CreateSafeheronRoutingCases) Description() string {
	return "Create authoritative Safeheron movement routing, projection, and alert state"
}

func (*CreateSafeheronRoutingCases) RequiredPreexistingVersion() string { return "056" }

func (*CreateSafeheronRoutingCases) RequiredExpectedCeiling() string { return "057" }

func (*CreateSafeheronRoutingCases) Up(*sql.DB) error {
	return fmt.Errorf("057 is controlled; run it through Migrator.MigrateWithExpectedCeiling")
}

func (*CreateSafeheronRoutingCases) UpTx(tx *sql.Tx) error {
	return runMigration057(tx)
}

func (*CreateSafeheronRoutingCases) Down(*sql.DB) error {
	return fmt.Errorf("057 is forward-only; routing audit state must be changed by a new migration")
}

var _ migration.Migration = (*CreateSafeheronRoutingCases)(nil)
var _ migration.ControlledMigration = (*CreateSafeheronRoutingCases)(nil)

func runMigration057(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration057TimeoutsSQL); err != nil {
		return fmt.Errorf("configure migration 057 timeouts: %w", err)
	}
	var unexpectedRelations int64
	if err := tx.QueryRowContext(ctx, migration057PreflightSQL).Scan(&unexpectedRelations); err != nil {
		return fmt.Errorf("preflight migration 057 routing schema: %w", err)
	}
	if unexpectedRelations != 0 {
		return fmt.Errorf("preflight rejected routing schema: unexpected_relations=%d", unexpectedRelations)
	}
	if _, err := tx.ExecContext(ctx, migration057SchemaSQL); err != nil {
		return fmt.Errorf("apply migration 057 routing schema: %w", err)
	}
	return nil
}

const migration057TimeoutsSQL = `SET LOCAL search_path = pg_catalog, public; SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '30s'; SET LOCAL idle_in_transaction_session_timeout = '30s';`

const migration057PreflightSQL = `
SELECT count(*)
FROM unnest(ARRAY[
  'safeheron_transaction_routing_cases',
  'safeheron_transaction_routing_case_sources',
  'safeheron_transaction_routing_case_commands',
  'safeheron_transaction_routing_case_actions',
  'safeheron_transaction_routing_case_results',
  'safeheron_transaction_routing_alerts',
  'safeheron_transaction_routing_alert_deliveries',
  'safeheron_transaction_routing_alert_delivery_attempts',
  'safeheron_transaction_routing_recovery_runs'
]) AS relation_name
WHERE to_regclass('public.' || relation_name) IS NOT NULL`

const migration057SchemaSQL = `
ALTER TABLE public.company_fund_provider_events
  ADD COLUMN authorized_safeheron_occurrence_key VARCHAR(256),
  ADD COLUMN authorizing_routing_action_id BIGINT,
  ADD CONSTRAINT company_fund_provider_events_authorized_occurrence_check CHECK (
    (authorized_safeheron_occurrence_key IS NULL AND authorizing_routing_action_id IS NULL) OR (
      source_kind = 'EXISTING_SAFEHERON_WEBHOOK_REF'
      AND authorized_safeheron_occurrence_key ~ '^safeheron-occurrence-v1:[0-9a-f]{64}$'
      AND authorizing_routing_action_id IS NOT NULL
    )
  );

CREATE TABLE public.safeheron_transaction_routing_cases (
  id BIGSERIAL PRIMARY KEY,
  routing_identity_key VARCHAR(256) NOT NULL UNIQUE,
  identity_algorithm_version VARCHAR(64) NOT NULL,
  provider_transaction_group_key VARCHAR(256) NOT NULL,
  safeheron_tx_key VARCHAR(128) NOT NULL,
  raw_coin_key VARCHAR(256) NOT NULL,
  network_family VARCHAR(64) NOT NULL,
  normalized_source VARCHAR(256) NOT NULL DEFAULT '',
  normalized_destination VARCHAR(256) NOT NULL,
  amount NUMERIC(65, 18) NOT NULL CHECK (amount >= 0),
  direction VARCHAR(32) NOT NULL,
  movement_kind VARCHAR(64) NOT NULL,
  movement_index INTEGER NOT NULL CHECK (movement_index >= 0),
  duplicate_ordinal INTEGER NOT NULL CHECK (duplicate_ordinal >= 0),
  effective_event_time TIMESTAMPTZ,
  event_time_source VARCHAR(32),
  reason_code VARCHAR(64) NOT NULL,
  decision VARCHAR(24) NOT NULL DEFAULT 'OPEN'
    CHECK (decision IN ('OPEN', 'PARTIAL', 'CUSTOMER', 'COMPANY', 'DUAL', 'NOT_RELEVANT', 'DISMISSED')),
  requires_customer_projection BOOLEAN NOT NULL DEFAULT false,
  requires_company_projection BOOLEAN NOT NULL DEFAULT false,
  customer_user_id INTEGER REFERENCES public.users(id) ON DELETE RESTRICT,
  company_fund_account_id BIGINT REFERENCES public.company_fund_accounts(id) ON DELETE RESTRICT,
  deposit_id INTEGER REFERENCES public.deposits(id) ON DELETE RESTRICT,
  company_fund_transaction_id BIGINT REFERENCES public.company_fund_transactions(id) ON DELETE RESTRICT,
  pending_command_id BIGINT,
  version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((effective_event_time IS NULL) = (event_time_source IS NULL)),
  CHECK (
    (decision = 'OPEN' AND NOT requires_customer_projection AND NOT requires_company_projection)
    OR (decision = 'PARTIAL' AND (requires_customer_projection OR requires_company_projection))
    OR (decision = 'CUSTOMER' AND requires_customer_projection AND NOT requires_company_projection)
    OR (decision = 'COMPANY' AND NOT requires_customer_projection AND requires_company_projection)
    OR (decision = 'DUAL' AND requires_customer_projection AND requires_company_projection)
    OR (decision IN ('NOT_RELEVANT', 'DISMISSED') AND NOT requires_customer_projection AND NOT requires_company_projection)
  ),
  CHECK (deposit_id IS NULL OR (requires_customer_projection AND decision IN ('PARTIAL', 'CUSTOMER', 'DUAL'))),
  CHECK (company_fund_transaction_id IS NULL OR (requires_company_projection AND decision IN ('PARTIAL', 'COMPANY', 'DUAL'))),
  CHECK (customer_user_id IS NULL OR requires_customer_projection),
  CHECK (company_fund_account_id IS NULL OR requires_company_projection)
);

CREATE UNIQUE INDEX idx_safeheron_routing_cases_deposit_result
  ON public.safeheron_transaction_routing_cases (deposit_id)
  WHERE deposit_id IS NOT NULL;
CREATE UNIQUE INDEX idx_safeheron_routing_cases_company_result
  ON public.safeheron_transaction_routing_cases (company_fund_transaction_id)
  WHERE company_fund_transaction_id IS NOT NULL;
CREATE INDEX idx_safeheron_routing_cases_list
  ON public.safeheron_transaction_routing_cases (decision, updated_at DESC, id DESC);
CREATE INDEX idx_safeheron_routing_cases_reason
  ON public.safeheron_transaction_routing_cases (reason_code, decision);
CREATE INDEX idx_safeheron_routing_cases_tx_key
  ON public.safeheron_transaction_routing_cases (safeheron_tx_key);
CREATE INDEX idx_safeheron_routing_cases_destination
  ON public.safeheron_transaction_routing_cases (normalized_destination, network_family);

CREATE TABLE public.safeheron_transaction_routing_case_sources (
  id BIGSERIAL PRIMARY KEY,
  case_id BIGINT NOT NULL REFERENCES public.safeheron_transaction_routing_cases(id) ON DELETE RESTRICT,
  safeheron_webhook_event_id INTEGER NOT NULL REFERENCES public.safeheron_webhook_events(id) ON DELETE RESTRICT,
  source_line_slot_key VARCHAR(256) NOT NULL,
  payload_digest VARCHAR(64) NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
  provider_status VARCHAR(64) NOT NULL,
  provider_status_rank INTEGER NOT NULL DEFAULT 0,
  effective_event_time TIMESTAMPTZ,
  event_time_source VARCHAR(32),
  linked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (case_id, safeheron_webhook_event_id),
  UNIQUE (safeheron_webhook_event_id, source_line_slot_key),
  CHECK ((effective_event_time IS NULL) = (event_time_source IS NULL))
);

CREATE INDEX idx_safeheron_routing_case_sources_event
  ON public.safeheron_transaction_routing_case_sources (safeheron_webhook_event_id);

CREATE TABLE public.safeheron_transaction_routing_case_commands (
  id BIGSERIAL PRIMARY KEY,
  case_id BIGINT NOT NULL REFERENCES public.safeheron_transaction_routing_cases(id) ON DELETE RESTRICT,
  command_type VARCHAR(32) NOT NULL
    CHECK (command_type IN ('AUTO_ROUTE', 'ASSIGN_CUSTOMER', 'FINALIZE_COMPANY', 'DISMISS', 'REOPEN', 'REQUEUE')),
  initiator VARCHAR(16) NOT NULL CHECK (initiator IN ('SYSTEM', 'ADMIN')),
  actor_scope VARCHAR(128) NOT NULL,
  actor_admin_user_id BIGINT,
  reason TEXT,
  idempotency_key VARCHAR(128) NOT NULL,
  request_digest VARCHAR(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
  expected_case_version INTEGER NOT NULL CHECK (expected_case_version > 0),
  status VARCHAR(16) NOT NULL DEFAULT 'PENDING'
    CHECK (status IN ('PENDING', 'APPLIED', 'BLOCKED', 'CANCELLED')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  UNIQUE (actor_scope, idempotency_key),
  CHECK (
    (initiator = 'ADMIN' AND actor_admin_user_id IS NOT NULL AND reason IS NOT NULL AND btrim(reason) <> '')
    OR (initiator = 'SYSTEM' AND actor_admin_user_id IS NULL)
  ),
  CHECK (
    (status = 'PENDING' AND completed_at IS NULL)
    OR (status <> 'PENDING' AND completed_at IS NOT NULL)
  )
);

ALTER TABLE public.safeheron_transaction_routing_cases
  ADD CONSTRAINT safeheron_routing_cases_pending_command_fk
  FOREIGN KEY (pending_command_id)
  REFERENCES public.safeheron_transaction_routing_case_commands(id)
  ON DELETE RESTRICT
  DEFERRABLE INITIALLY IMMEDIATE;

CREATE FUNCTION public.safeheron_validate_pending_command_case()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  checked_case_id BIGINT;
  checked_command_id BIGINT;
BEGIN
  IF TG_TABLE_NAME='safeheron_transaction_routing_cases' THEN
    checked_case_id := NEW.id;
    checked_command_id := NEW.pending_command_id;
  ELSE
    checked_case_id := NEW.case_id;
    checked_command_id := NEW.id;
  END IF;
  IF checked_command_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM public.safeheron_transaction_routing_case_commands command
    WHERE command.id=checked_command_id AND command.case_id=checked_case_id
  ) THEN
    RAISE EXCEPTION 'pending routing command belongs to another case';
  END IF;
  IF TG_TABLE_NAME='safeheron_transaction_routing_case_commands' AND EXISTS (
    SELECT 1 FROM public.safeheron_transaction_routing_cases routing
    WHERE routing.pending_command_id=checked_command_id AND routing.id<>checked_case_id
  ) THEN
    RAISE EXCEPTION 'pending routing command belongs to another case';
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER trg_safeheron_pending_command_case
AFTER INSERT OR UPDATE OF pending_command_id ON public.safeheron_transaction_routing_cases
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.safeheron_validate_pending_command_case();

CREATE CONSTRAINT TRIGGER trg_safeheron_command_pending_case
AFTER UPDATE OF case_id ON public.safeheron_transaction_routing_case_commands
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.safeheron_validate_pending_command_case();

CREATE TABLE public.safeheron_transaction_routing_case_actions (
  id BIGSERIAL PRIMARY KEY,
  command_id BIGINT NOT NULL REFERENCES public.safeheron_transaction_routing_case_commands(id) ON DELETE RESTRICT,
  action_type VARCHAR(32) NOT NULL
    CHECK (action_type IN ('APPLY_CUSTOMER', 'APPLY_COMPANY', 'DISMISS', 'REOPEN', 'FINALIZE_COMPANY_ONLY')),
  projection_kind VARCHAR(16) NOT NULL CHECK (projection_kind IN ('CUSTOMER', 'COMPANY', 'CONTROL')),
  status VARCHAR(16) NOT NULL DEFAULT 'PENDING'
    CHECK (status IN ('PENDING', 'APPLIED', 'RETRYABLE', 'DEAD')),
  target_user_id INTEGER REFERENCES public.users(id) ON DELETE RESTRICT,
  target_company_fund_account_id BIGINT REFERENCES public.company_fund_accounts(id) ON DELETE RESTRICT,
  attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  next_attempt_at TIMESTAMPTZ,
  lease_owner VARCHAR(128),
  lease_expires_at TIMESTAMPTZ,
  last_error_code VARCHAR(64),
  last_error_detail TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  UNIQUE (command_id, projection_kind),
  CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
  CHECK (
    (status = 'RETRYABLE' AND next_attempt_at IS NOT NULL AND lease_owner IS NULL)
    OR (status <> 'RETRYABLE' AND next_attempt_at IS NULL)
  ),
  CHECK (
    (status IN ('APPLIED', 'DEAD') AND completed_at IS NOT NULL AND lease_owner IS NULL)
    OR (status IN ('PENDING', 'RETRYABLE') AND completed_at IS NULL)
  ),
  CHECK (
    (projection_kind = 'CUSTOMER' AND action_type = 'APPLY_CUSTOMER' AND target_user_id IS NOT NULL AND target_company_fund_account_id IS NULL)
    OR (projection_kind = 'COMPANY' AND action_type IN ('APPLY_COMPANY', 'FINALIZE_COMPANY_ONLY') AND target_company_fund_account_id IS NOT NULL AND target_user_id IS NULL)
    OR (projection_kind = 'CONTROL' AND action_type IN ('DISMISS', 'REOPEN') AND target_user_id IS NULL AND target_company_fund_account_id IS NULL)
  )
);

ALTER TABLE public.company_fund_provider_events
  ADD CONSTRAINT company_fund_provider_events_authorizing_action_fk
  FOREIGN KEY (authorizing_routing_action_id)
  REFERENCES public.safeheron_transaction_routing_case_actions(id)
  ON DELETE RESTRICT;

ALTER TABLE public.safeheron_webhook_events
  ADD COLUMN authorizing_routing_action_id BIGINT,
  ADD CONSTRAINT safeheron_webhook_events_routing_action_shape CHECK (
    (authorizing_routing_action_id IS NULL AND event_id NOT LIKE 'routing-customer:%')
    OR (authorizing_routing_action_id IS NOT NULL AND event_id = 'routing-customer:' || authorizing_routing_action_id::text)
  ),
  ADD CONSTRAINT safeheron_webhook_events_authorizing_action_fk
    FOREIGN KEY (authorizing_routing_action_id)
    REFERENCES public.safeheron_transaction_routing_case_actions(id)
    ON DELETE RESTRICT;

CREATE FUNCTION public.safeheron_validate_customer_event_authorization()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.authorizing_routing_action_id IS NOT NULL AND NOT EXISTS (
    SELECT 1
    FROM public.safeheron_transaction_routing_case_actions action
    JOIN public.safeheron_transaction_routing_case_commands command ON command.id=action.command_id
    JOIN public.safeheron_transaction_routing_cases routing
      ON routing.id=command.case_id AND routing.pending_command_id=command.id
    WHERE action.id=NEW.authorizing_routing_action_id
      AND action.action_type='APPLY_CUSTOMER'
      AND action.projection_kind='CUSTOMER'
  ) THEN
    RAISE EXCEPTION 'customer event routing authorization is not the current customer action';
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER trg_safeheron_customer_event_authorization
AFTER INSERT OR UPDATE OF authorizing_routing_action_id
ON public.safeheron_webhook_events
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.safeheron_validate_customer_event_authorization();

CREATE FUNCTION public.safeheron_validate_provider_event_authorization()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.authorizing_routing_action_id IS NOT NULL AND NOT EXISTS (
    SELECT 1
    FROM public.safeheron_transaction_routing_case_actions action
    JOIN public.safeheron_transaction_routing_case_commands command ON command.id=action.command_id
    JOIN public.safeheron_transaction_routing_cases routing ON routing.id=command.case_id
    WHERE action.id=NEW.authorizing_routing_action_id
      AND routing.routing_identity_key=NEW.authorized_safeheron_occurrence_key
  ) THEN
    RAISE EXCEPTION 'provider event routing authorization differs from action occurrence';
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER trg_safeheron_provider_event_authorization
AFTER INSERT OR UPDATE OF authorized_safeheron_occurrence_key,authorizing_routing_action_id
ON public.company_fund_provider_events
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.safeheron_validate_provider_event_authorization();

CREATE INDEX idx_safeheron_routing_actions_claim
  ON public.safeheron_transaction_routing_case_actions (status, next_attempt_at, lease_expires_at, id)
  WHERE status IN ('PENDING', 'RETRYABLE');

CREATE TABLE public.safeheron_transaction_routing_case_results (
  id BIGSERIAL PRIMARY KEY,
  case_id BIGINT NOT NULL REFERENCES public.safeheron_transaction_routing_cases(id) ON DELETE RESTRICT,
  action_id BIGINT NOT NULL UNIQUE REFERENCES public.safeheron_transaction_routing_case_actions(id) ON DELETE RESTRICT,
  projection_kind VARCHAR(16) NOT NULL CHECK (projection_kind IN ('CUSTOMER', 'COMPANY')),
  deposit_id INTEGER UNIQUE REFERENCES public.deposits(id) ON DELETE RESTRICT,
  company_fund_transaction_id BIGINT UNIQUE REFERENCES public.company_fund_transactions(id) ON DELETE RESTRICT,
  result_digest VARCHAR(64) NOT NULL CHECK (result_digest ~ '^[0-9a-f]{64}$'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (case_id, projection_kind),
  CHECK (
    (projection_kind = 'CUSTOMER' AND deposit_id IS NOT NULL AND company_fund_transaction_id IS NULL)
    OR (projection_kind = 'COMPANY' AND company_fund_transaction_id IS NOT NULL AND deposit_id IS NULL)
  )
);

CREATE FUNCTION public.safeheron_validate_routing_result_consistency()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  checked_case_id BIGINT;
  action_case_id BIGINT;
  case_deposit_id BIGINT;
  case_company_id BIGINT;
BEGIN
  IF TG_TABLE_NAME='safeheron_transaction_routing_cases' THEN
    checked_case_id := NEW.id;
  ELSIF TG_OP='DELETE' THEN
    checked_case_id := OLD.case_id;
  ELSE
    checked_case_id := NEW.case_id;
  END IF;
  SELECT deposit_id,company_fund_transaction_id INTO case_deposit_id,case_company_id
  FROM public.safeheron_transaction_routing_cases WHERE id=checked_case_id;
  IF TG_TABLE_NAME='safeheron_transaction_routing_case_results' AND TG_OP<>'DELETE' THEN
    SELECT command.case_id INTO action_case_id
    FROM public.safeheron_transaction_routing_case_actions action
    JOIN public.safeheron_transaction_routing_case_commands command ON command.id=action.command_id
    WHERE action.id=NEW.action_id;
    IF action_case_id IS DISTINCT FROM NEW.case_id THEN
      RAISE EXCEPTION 'routing result action belongs to another case';
    END IF;
  END IF;
  IF (case_deposit_id IS NULL AND EXISTS (
       SELECT 1 FROM public.safeheron_transaction_routing_case_results
       WHERE case_id=checked_case_id AND projection_kind='CUSTOMER'))
     OR (case_deposit_id IS NOT NULL AND NOT EXISTS (
       SELECT 1 FROM public.safeheron_transaction_routing_case_results
       WHERE case_id=checked_case_id AND projection_kind='CUSTOMER' AND deposit_id=case_deposit_id)) THEN
    RAISE EXCEPTION 'routing customer result and case link differ';
  END IF;
  IF (case_company_id IS NULL AND EXISTS (
       SELECT 1 FROM public.safeheron_transaction_routing_case_results
       WHERE case_id=checked_case_id AND projection_kind='COMPANY'))
     OR (case_company_id IS NOT NULL AND NOT EXISTS (
       SELECT 1 FROM public.safeheron_transaction_routing_case_results
       WHERE case_id=checked_case_id AND projection_kind='COMPANY' AND company_fund_transaction_id=case_company_id)) THEN
    RAISE EXCEPTION 'routing company result and case link differ';
  END IF;
  IF TG_OP='DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER trg_safeheron_routing_result_consistency
AFTER INSERT OR UPDATE OR DELETE ON public.safeheron_transaction_routing_case_results
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.safeheron_validate_routing_result_consistency();

CREATE CONSTRAINT TRIGGER trg_safeheron_routing_case_result_consistency
AFTER INSERT OR UPDATE OF deposit_id,company_fund_transaction_id
ON public.safeheron_transaction_routing_cases
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.safeheron_validate_routing_result_consistency();

CREATE TABLE public.safeheron_transaction_routing_alerts (
  id BIGSERIAL PRIMARY KEY,
  case_id BIGINT NOT NULL REFERENCES public.safeheron_transaction_routing_cases(id) ON DELETE RESTRICT,
  alert_type VARCHAR(32) NOT NULL CHECK (alert_type IN ('OPEN', 'ACTION_DEAD', 'SLA_ESCALATION', 'RECOVERY_SUMMARY')),
  transition_key VARCHAR(256) NOT NULL,
  severity VARCHAR(16) NOT NULL CHECK (severity IN ('INFO', 'WARN', 'ERROR', 'CRITICAL')),
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (case_id, alert_type, transition_key)
);

CREATE TABLE public.safeheron_transaction_routing_alert_deliveries (
  id BIGSERIAL PRIMARY KEY,
  alert_id BIGINT NOT NULL REFERENCES public.safeheron_transaction_routing_alerts(id) ON DELETE RESTRICT,
  sink_kind VARCHAR(16) NOT NULL CHECK (sink_kind IN ('LARK', 'EMAIL')),
  recipient_fingerprint VARCHAR(64) NOT NULL CHECK (recipient_fingerprint ~ '^[0-9a-f]{64}$'),
  status VARCHAR(24) NOT NULL DEFAULT 'PENDING'
    CHECK (status IN ('PENDING', 'DISPATCHING', 'SENT', 'FAILED_DEFINITE', 'AMBIGUOUS')),
  automatic_attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (automatic_attempt_count >= 0),
  next_attempt_at TIMESTAMPTZ,
  lease_owner VARCHAR(128),
  lease_expires_at TIMESTAMPTZ,
  last_error_code VARCHAR(64),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  UNIQUE (alert_id, sink_kind, recipient_fingerprint),
  CHECK (
    (status = 'DISPATCHING' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
    OR (status <> 'DISPATCHING' AND lease_owner IS NULL AND lease_expires_at IS NULL)
  ),
  CHECK (
    (status IN ('SENT', 'AMBIGUOUS') AND completed_at IS NOT NULL)
    OR (status IN ('PENDING', 'DISPATCHING', 'FAILED_DEFINITE') AND completed_at IS NULL)
  )
);

CREATE INDEX idx_safeheron_routing_alert_deliveries_claim
  ON public.safeheron_transaction_routing_alert_deliveries (status, next_attempt_at, lease_expires_at, id)
  WHERE status IN ('PENDING', 'DISPATCHING', 'FAILED_DEFINITE');

CREATE TABLE public.safeheron_transaction_routing_alert_delivery_attempts (
  id BIGSERIAL PRIMARY KEY,
  delivery_id BIGINT NOT NULL REFERENCES public.safeheron_transaction_routing_alert_deliveries(id) ON DELETE RESTRICT,
  attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
  attempt_kind VARCHAR(24) NOT NULL CHECK (attempt_kind IN ('AUTO', 'MANUAL_REPLAY')),
  outcome VARCHAR(32) NOT NULL
    CHECK (outcome IN ('IN_PROGRESS', 'SENT', 'DEFINITELY_NOT_SENT', 'DELIVERY_UNKNOWN')),
  actor_admin_user_id BIGINT,
  reason TEXT,
  idempotency_key VARCHAR(128),
  error_code VARCHAR(64),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  UNIQUE (delivery_id, attempt_number),
  CHECK (
    (attempt_kind = 'AUTO' AND actor_admin_user_id IS NULL AND reason IS NULL AND idempotency_key IS NULL)
    OR (attempt_kind = 'MANUAL_REPLAY' AND actor_admin_user_id IS NOT NULL AND reason IS NOT NULL AND btrim(reason) <> '' AND idempotency_key IS NOT NULL)
  ),
  CHECK (
    (outcome = 'IN_PROGRESS' AND finished_at IS NULL)
    OR (outcome <> 'IN_PROGRESS' AND finished_at IS NOT NULL)
  )
);

CREATE UNIQUE INDEX idx_safeheron_routing_alert_manual_replay_idempotency
  ON public.safeheron_transaction_routing_alert_delivery_attempts (delivery_id, idempotency_key)
  WHERE attempt_kind = 'MANUAL_REPLAY';

CREATE TABLE public.safeheron_transaction_routing_recovery_runs (
  id BIGSERIAL PRIMARY KEY,
  run_key VARCHAR(64) NOT NULL UNIQUE CHECK (run_key ~ '^[0-9a-f]{64}$'),
  occurrence_identity_digest VARCHAR(64) NOT NULL CHECK (occurrence_identity_digest ~ '^[0-9a-f]{64}$'),
  event_count INTEGER NOT NULL CHECK (event_count > 0),
  occurrence_count INTEGER NOT NULL CHECK (occurrence_count > 0),
  recovery_options JSONB NOT NULL,
  recovery_report JSONB NOT NULL,
  status VARCHAR(16) NOT NULL CHECK (status IN ('APPLIED')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`
