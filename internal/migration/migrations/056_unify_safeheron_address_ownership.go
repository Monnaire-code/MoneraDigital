package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type UnifySafeheronAddressOwnership struct{}

func (*UnifySafeheronAddressOwnership) Version() string { return "056" }

func (*UnifySafeheronAddressOwnership) Description() string {
	return "Add company account monitoring boundaries and authoritative Safeheron address ownership"
}

func (*UnifySafeheronAddressOwnership) RequiredPreexistingVersion() string { return "055" }

func (*UnifySafeheronAddressOwnership) RequiredExpectedCeiling() string { return "056" }

func (*UnifySafeheronAddressOwnership) Up(*sql.DB) error {
	return fmt.Errorf("056 is controlled; run it through Migrator.MigrateWithExpectedCeiling")
}

func (*UnifySafeheronAddressOwnership) UpTx(tx *sql.Tx) error {
	return runMigration056(tx)
}

func (*UnifySafeheronAddressOwnership) Down(*sql.DB) error {
	return fmt.Errorf("056 is forward-only; address ownership must be changed by a new migration")
}

var _ migration.Migration = (*UnifySafeheronAddressOwnership)(nil)
var _ migration.ControlledMigration = (*UnifySafeheronAddressOwnership)(nil)

type migration056Preflight struct {
	poolDuplicates           int64
	companyDuplicates        int64
	crossDomainConflicts     int64
	invalidIdentities        int64
	unexpectedOwnershipTable int64
}

func (result migration056Preflight) unsafe() bool {
	return result.poolDuplicates != 0 ||
		result.companyDuplicates != 0 ||
		result.crossDomainConflicts != 0 ||
		result.invalidIdentities != 0 ||
		result.unexpectedOwnershipTable != 0
}

func runMigration056(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration056TimeoutsSQL); err != nil {
		return fmt.Errorf("configure migration 056 timeouts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, migration056LockSourcesSQL); err != nil {
		return fmt.Errorf("lock migration 056 ownership sources: %w", err)
	}

	var preflight migration056Preflight
	if err := tx.QueryRowContext(ctx, migration056PreflightSQL).Scan(
		&preflight.poolDuplicates,
		&preflight.companyDuplicates,
		&preflight.crossDomainConflicts,
		&preflight.invalidIdentities,
		&preflight.unexpectedOwnershipTable,
	); err != nil {
		return fmt.Errorf("preflight migration 056 address ownership: %w", err)
	}
	if preflight.unsafe() {
		return fmt.Errorf(
			"preflight rejected address ownership state: pool_duplicates=%d company_duplicates=%d cross_domain_conflicts=%d invalid_identities=%d unexpected_ownership_table=%d",
			preflight.poolDuplicates,
			preflight.companyDuplicates,
			preflight.crossDomainConflicts,
			preflight.invalidIdentities,
			preflight.unexpectedOwnershipTable,
		)
	}

	if _, err := tx.ExecContext(ctx, migration056SchemaSQL); err != nil {
		return fmt.Errorf("apply migration 056 address ownership schema: %w", err)
	}
	return nil
}

const migration056TimeoutsSQL = `SET LOCAL search_path = pg_catalog, public; SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '30s'; SET LOCAL idle_in_transaction_session_timeout = '30s';`

const migration056LockSourcesSQL = `LOCK TABLE public.address_pool IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE public.company_fund_accounts IN SHARE ROW EXCLUSIVE MODE;`

const migration056PreflightSQL = `
WITH pool AS (
  SELECT id,
         upper(btrim(network_family)) AS network_family,
         CASE WHEN upper(btrim(network_family)) = 'EVM'
              THEN lower(btrim(address)) ELSE btrim(address) END AS normalized_address
  FROM public.address_pool
),
company AS (
  SELECT id,
         upper(btrim(network_family)) AS network_family,
         CASE WHEN upper(btrim(network_family)) = 'EVM'
              THEN lower(btrim(normalized_address)) ELSE btrim(normalized_address) END AS normalized_address
  FROM public.company_fund_accounts
  WHERE channel = 'SAFEHERON'
),
pool_duplicates AS (
  SELECT network_family, normalized_address
  FROM pool
  GROUP BY network_family, normalized_address
  HAVING count(*) > 1
),
company_duplicates AS (
  SELECT network_family, normalized_address
  FROM company
  GROUP BY network_family, normalized_address
  HAVING count(*) > 1
)
SELECT
  (SELECT count(*) FROM pool_duplicates) AS pool_duplicate_count,
  (SELECT count(*) FROM company_duplicates) AS company_duplicate_count,
  (SELECT count(*) FROM pool JOIN company USING (network_family, normalized_address)) AS cross_domain_conflict_count,
  (
    SELECT count(*) FROM (
      SELECT network_family, normalized_address FROM pool
      UNION ALL
      SELECT network_family, normalized_address FROM company
    ) identities
    WHERE network_family IS NULL OR network_family = ''
       OR normalized_address IS NULL OR normalized_address = ''
  ) AS invalid_identity_count,
  CASE WHEN to_regclass('public.safeheron_address_ownerships') IS NULL THEN 0 ELSE 1 END
    AS unexpected_ownership_table_count`

const migration056SchemaSQL = `
ALTER TABLE public.company_fund_accounts
  ADD COLUMN monitoring_started_at TIMESTAMPTZ,
  ADD COLUMN first_enabled_at TIMESTAMPTZ;

UPDATE public.company_fund_accounts
SET monitoring_started_at = COALESCE(
      (SELECT min(log.created_at) FROM public.admin_operation_logs log
       WHERE log.module='company_fund'
         AND log.request_data->>'resourceType'='account'
         AND log.request_data->>'resourceId'=company_fund_accounts.id::text
         AND log.request_data->'payload'->>'afterEnabled'='true'),
      CASE WHEN is_enabled THEN created_at ELSE transaction_timestamp() END
    ),
    first_enabled_at = COALESCE(
      (SELECT min(log.created_at) FROM public.admin_operation_logs log
       WHERE log.module='company_fund'
         AND log.request_data->>'resourceType'='account'
         AND log.request_data->>'resourceId'=company_fund_accounts.id::text
         AND log.request_data->'payload'->>'afterEnabled'='true'),
      CASE WHEN is_enabled THEN created_at ELSE NULL END
    );

ALTER TABLE public.company_fund_accounts
  ALTER COLUMN monitoring_started_at SET DEFAULT now(),
  ALTER COLUMN monitoring_started_at SET NOT NULL;

CREATE FUNCTION public.safeheron_canonical_network_family(input_network_family TEXT)
RETURNS TEXT
LANGUAGE SQL
IMMUTABLE
STRICT
AS $$
  SELECT upper(btrim(input_network_family));
$$;

CREATE FUNCTION public.safeheron_canonical_address(input_network_family TEXT, input_address TEXT)
RETURNS TEXT
LANGUAGE SQL
IMMUTABLE
STRICT
AS $$
  SELECT CASE
    WHEN public.safeheron_canonical_network_family(input_network_family) = 'EVM'
      THEN lower(btrim(input_address))
    ELSE btrim(input_address)
  END;
$$;

UPDATE public.address_pool
SET network_family = public.safeheron_canonical_network_family(network_family),
    address = public.safeheron_canonical_address(network_family, address);

UPDATE public.company_fund_accounts
SET network_family = public.safeheron_canonical_network_family(network_family),
    normalized_address = public.safeheron_canonical_address(network_family, normalized_address)
WHERE channel = 'SAFEHERON';

CREATE TABLE public.safeheron_address_ownerships (
  id BIGSERIAL PRIMARY KEY,
  network_family VARCHAR(64) NOT NULL,
  normalized_address VARCHAR(256) NOT NULL,
  owner_kind VARCHAR(32) NOT NULL
    CHECK (owner_kind IN ('CUSTOMER_POOL', 'COMPANY_ACCOUNT')),
  address_pool_id INTEGER UNIQUE
    REFERENCES public.address_pool(id) ON DELETE RESTRICT,
  company_fund_account_id BIGINT UNIQUE
    REFERENCES public.company_fund_accounts(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (network_family, normalized_address),
  CHECK (
    (owner_kind = 'CUSTOMER_POOL'
      AND address_pool_id IS NOT NULL
      AND company_fund_account_id IS NULL)
    OR
    (owner_kind = 'COMPANY_ACCOUNT'
      AND company_fund_account_id IS NOT NULL
      AND address_pool_id IS NULL)
  )
);

INSERT INTO public.safeheron_address_ownerships
  (network_family, normalized_address, owner_kind, address_pool_id)
SELECT network_family, address, 'CUSTOMER_POOL', id
FROM public.address_pool
ORDER BY id;

INSERT INTO public.safeheron_address_ownerships
  (network_family, normalized_address, owner_kind, company_fund_account_id)
SELECT network_family, normalized_address, 'COMPANY_ACCOUNT', id
FROM public.company_fund_accounts
WHERE channel = 'SAFEHERON'
ORDER BY id;

CREATE FUNCTION public.safeheron_guard_authoritative_ownership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  source_network TEXT;
  source_address TEXT;
BEGIN
	IF TG_OP = 'DELETE' THEN
	    RAISE EXCEPTION 'Safeheron authoritative ownership rows cannot be deleted directly';
  END IF;
  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION 'Safeheron authoritative ownership rows are immutable';
  END IF;
  IF NEW.owner_kind = 'CUSTOMER_POOL' THEN
    SELECT network_family, address INTO source_network, source_address
    FROM public.address_pool WHERE id=NEW.address_pool_id;
  ELSE
    SELECT network_family, normalized_address INTO source_network, source_address
    FROM public.company_fund_accounts
    WHERE id=NEW.company_fund_account_id AND channel='SAFEHERON';
  END IF;
  IF source_network IS NULL
     OR NEW.network_family IS DISTINCT FROM source_network
     OR NEW.normalized_address IS DISTINCT FROM source_address THEN
    RAISE EXCEPTION 'Safeheron ownership must exactly match its authoritative source';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER trg_safeheron_authoritative_ownership_guard
BEFORE INSERT OR UPDATE OR DELETE ON public.safeheron_address_ownerships
FOR EACH ROW EXECUTE FUNCTION public.safeheron_guard_authoritative_ownership();

CREATE FUNCTION public.safeheron_guard_address_pool_identity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  canonical_network TEXT;
  canonical_address TEXT;
BEGIN
  canonical_network := public.safeheron_canonical_network_family(NEW.network_family);
  canonical_address := public.safeheron_canonical_address(canonical_network, NEW.address);
  IF canonical_network = '' OR canonical_address = '' THEN
    RAISE EXCEPTION 'address_pool Safeheron identity cannot be empty';
  END IF;
  IF TG_OP = 'UPDATE' AND (
    canonical_network IS DISTINCT FROM OLD.network_family
    OR canonical_address IS DISTINCT FROM OLD.address
  ) THEN
    RAISE EXCEPTION 'address_pool Safeheron identity is immutable';
  END IF;
  NEW.network_family := canonical_network;
  NEW.address := canonical_address;
  RETURN NEW;
END;
$$;

CREATE FUNCTION public.safeheron_claim_address_pool_ownership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  INSERT INTO public.safeheron_address_ownerships
    (network_family, normalized_address, owner_kind, address_pool_id)
  VALUES (NEW.network_family, NEW.address, 'CUSTOMER_POOL', NEW.id);
  RETURN NEW;
END;
$$;

CREATE FUNCTION public.safeheron_guard_company_account_identity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  canonical_network TEXT;
  canonical_address TEXT;
BEGIN
  IF NEW.channel <> 'SAFEHERON' AND (TG_OP = 'INSERT' OR OLD.channel <> 'SAFEHERON') THEN
    RETURN NEW;
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.channel IS DISTINCT FROM NEW.channel THEN
    RAISE EXCEPTION 'company fund account channel identity is immutable';
  END IF;
  canonical_network := public.safeheron_canonical_network_family(NEW.network_family);
  canonical_address := public.safeheron_canonical_address(canonical_network, NEW.normalized_address);
  IF canonical_network = '' OR canonical_address = '' THEN
    RAISE EXCEPTION 'company fund Safeheron identity cannot be empty';
  END IF;
  IF TG_OP = 'UPDATE' AND (
    canonical_network IS DISTINCT FROM OLD.network_family
    OR canonical_address IS DISTINCT FROM OLD.normalized_address
  ) THEN
    RAISE EXCEPTION 'company fund Safeheron identity is immutable';
  END IF;
  NEW.network_family := canonical_network;
  NEW.normalized_address := canonical_address;
  RETURN NEW;
END;
$$;

CREATE FUNCTION public.safeheron_claim_company_account_ownership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.channel = 'SAFEHERON' THEN
    INSERT INTO public.safeheron_address_ownerships
      (network_family, normalized_address, owner_kind, company_fund_account_id)
    VALUES (NEW.network_family, NEW.normalized_address, 'COMPANY_ACCOUNT', NEW.id);
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION public.safeheron_guard_company_account_monitoring()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    NEW.monitoring_started_at := COALESCE(NEW.monitoring_started_at, now());
    IF NEW.is_enabled THEN
      NEW.first_enabled_at := COALESCE(NEW.first_enabled_at, now());
    ELSIF NEW.first_enabled_at IS NOT NULL THEN
      RAISE EXCEPTION 'disabled company fund account cannot start with first_enabled_at';
    END IF;
    RETURN NEW;
  END IF;

  IF OLD.first_enabled_at IS NOT NULL
     AND NEW.first_enabled_at IS DISTINCT FROM OLD.first_enabled_at THEN
    RAISE EXCEPTION 'company fund account first_enabled_at is immutable';
  END IF;
  IF OLD.first_enabled_at IS NULL AND NOT OLD.is_enabled AND NEW.is_enabled THEN
    NEW.first_enabled_at := now();
  ELSIF OLD.first_enabled_at IS NULL
        AND NEW.first_enabled_at IS NOT NULL THEN
    RAISE EXCEPTION 'first_enabled_at can only be set by the first enable transition';
  END IF;
  IF OLD.monitoring_started_at IS DISTINCT FROM NEW.monitoring_started_at
     AND OLD.first_enabled_at IS NOT NULL
     AND current_setting('monera.company_fund_monitoring_backfill', true) IS DISTINCT FROM 'on' THEN
    RAISE EXCEPTION 'monitoring_started_at requires the dedicated history backfill action';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER trg_address_pool_identity_immutable
BEFORE INSERT OR UPDATE OF network_family, address ON public.address_pool
FOR EACH ROW EXECUTE FUNCTION public.safeheron_guard_address_pool_identity();

CREATE TRIGGER trg_address_pool_claim_ownership
AFTER INSERT ON public.address_pool
FOR EACH ROW EXECUTE FUNCTION public.safeheron_claim_address_pool_ownership();

CREATE TRIGGER trg_company_fund_accounts_identity_immutable
BEFORE INSERT OR UPDATE OF channel, network_family, normalized_address
ON public.company_fund_accounts
FOR EACH ROW EXECUTE FUNCTION public.safeheron_guard_company_account_identity();

CREATE TRIGGER trg_company_fund_accounts_first_enabled
BEFORE INSERT OR UPDATE OF is_enabled, first_enabled_at, monitoring_started_at
ON public.company_fund_accounts
FOR EACH ROW EXECUTE FUNCTION public.safeheron_guard_company_account_monitoring();

CREATE TRIGGER trg_company_fund_accounts_claim_ownership
AFTER INSERT ON public.company_fund_accounts
FOR EACH ROW EXECUTE FUNCTION public.safeheron_claim_company_account_ownership();

`
