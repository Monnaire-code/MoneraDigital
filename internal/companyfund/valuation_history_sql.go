package companyfund

const companyFundValuationHistoryColumns = `
id,
transaction_id,
valuation_version,
usd_value::TEXT,
provider_reported_usd_value::TEXT,
calculated_usd_value::TEXT,
usd_unit_price::TEXT,
usd_valuation_status,
COALESCE(usd_valuation_reason_code, ''),
COALESCE(usd_valuation_basis, ''),
usd_valuation_time,
usd_valuation_price_at,
COALESCE(usd_valuation_source, ''),
COALESCE(usd_valuation_method, ''),
COALESCE(usd_valuation_granularity, ''),
COALESCE(usd_provider_value_scope, ''),
COALESCE(usd_derivation_method, ''),
usd_rate_snapshot_id,
provider_transaction_fact_id,
dependency_fingerprint,
valuation_policy_version,
transition_trigger,
supersedes_history_id,
applied_at`

const selectCompanyFundTransactionForValuationSQL = `
SELECT transaction.id,
	transaction.current_valuation_history_id,
	COALESCE(history.dependency_fingerprint, '')
FROM company_fund_transactions AS transaction
LEFT JOIN company_fund_transaction_valuation_history AS history
	ON history.transaction_id = transaction.id
	AND history.id = transaction.current_valuation_history_id
WHERE transaction.id = $1
FOR UPDATE OF transaction`

// selectValuationHistoryByApplyIdentitySQL is the idempotency read for one
// immutable state transition. A pending state and its later final result may
// share dependency inputs, so transition_trigger remains part of this identity.
const selectValuationHistoryByApplyIdentitySQL = `
SELECT ` + companyFundValuationHistoryColumns + `
FROM company_fund_transaction_valuation_history
WHERE transaction_id = $1
  AND dependency_fingerprint = $2
  AND valuation_policy_version = $3
	AND transition_trigger = $4
ORDER BY valuation_version DESC
LIMIT 1
FOR UPDATE`

const selectLatestValuationHistoryForUpdateSQL = `
SELECT ` + companyFundValuationHistoryColumns + `
FROM company_fund_transaction_valuation_history
WHERE transaction_id = $1
ORDER BY valuation_version DESC
LIMIT 1
FOR UPDATE`

const insertCompanyFundValuationHistorySQL = `
INSERT INTO company_fund_transaction_valuation_history (
	transaction_id,
	valuation_version,
	usd_value,
	provider_reported_usd_value,
	calculated_usd_value,
	usd_unit_price,
	usd_valuation_status,
	usd_valuation_reason_code,
	usd_valuation_basis,
	usd_valuation_time,
	usd_valuation_price_at,
	usd_valuation_source,
	usd_valuation_method,
	usd_valuation_granularity,
	usd_provider_value_scope,
	usd_derivation_method,
	usd_rate_snapshot_id,
	provider_transaction_fact_id,
	dependency_fingerprint,
	valuation_policy_version,
	transition_trigger,
	supersedes_history_id
) VALUES (
	$1, $2, $3::numeric, $4::numeric, $5::numeric, $6::numeric,
	$7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
	$19, $20, $21, $22
)
RETURNING ` + companyFundValuationHistoryColumns

// updateCompanyFundTransactionValuationProjectionSQL deliberately enumerates
// only valuation projection columns. Finance classification, risk state and
// provider/raw event fields are structurally absent.
const updateCompanyFundTransactionValuationProjectionSQL = `
UPDATE company_fund_transactions
SET provider_reported_usd_value = $2::numeric,
	calculated_usd_value = $3::numeric,
	usd_value = $4::numeric,
	usd_unit_price = $5::numeric,
	usd_valuation_status = $6,
	usd_valuation_reason_code = $7,
	usd_valuation_basis = $8,
	usd_valuation_time = $9,
	usd_valuation_price_at = $10,
	usd_valuation_source = $11,
	usd_valuation_method = $12,
	usd_valuation_granularity = $13,
	usd_provider_value_scope = $14,
	usd_derivation_method = $15,
	usd_rate_snapshot_id = $16,
	current_valuation_history_id = $17,
	usd_valued_at = clock_timestamp(),
	usd_valuation_policy_version = $18,
	usd_valuation_version = $19,
	updated_at = clock_timestamp()
WHERE id = $1
RETURNING id`
