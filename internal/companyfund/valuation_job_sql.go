package companyfund

const companyFundValuationJobColumns = `
id,
transaction_id,
source_valuation_history_id,
trigger_kind,
COALESCE(trigger_id, ''),
target_dependency_fingerprint,
policy_version,
expected_current_state,
expected_current_history_id,
COALESCE(expected_current_dependency_fingerprint, ''),
job_state,
attempt_count,
next_attempt_at,
COALESCE(lease_owner, ''),
lease_expires_at,
COALESCE(last_error, ''),
completed_at,
created_at`

const selectValuationHistoryForValuationJobSQL = `
SELECT id
FROM company_fund_transaction_valuation_history
WHERE transaction_id = $1
  AND id = $2
FOR KEY SHARE`

const insertCompanyFundValuationJobSQL = `
INSERT INTO company_fund_valuation_jobs (
	transaction_id,
	source_valuation_history_id,
	trigger_kind,
	trigger_id,
	target_dependency_fingerprint,
	policy_version,
	expected_current_state,
	expected_current_history_id,
	expected_current_dependency_fingerprint
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (transaction_id, target_dependency_fingerprint, policy_version) DO NOTHING
RETURNING ` + companyFundValuationJobColumns

const selectCompanyFundValuationJobByTargetSQL = `
SELECT ` + companyFundValuationJobColumns + `
FROM company_fund_valuation_jobs
WHERE transaction_id = $1
  AND target_dependency_fingerprint = $2
  AND policy_version = $3
FOR KEY SHARE`

const claimNextCompanyFundValuationJobSQL = `
SELECT ` + companyFundValuationJobColumns + `
FROM company_fund_valuation_jobs
WHERE (
	job_state = 'PENDING'
	OR (job_state = 'RETRY_WAIT' AND next_attempt_at <= clock_timestamp())
	OR (job_state = 'LEASED' AND lease_expires_at <= clock_timestamp())
)
	AND NOT EXISTS (
		SELECT 1
		FROM company_fund_transactions AS movement
		JOIN company_fund_transaction_valuation_history AS current_history
			ON current_history.transaction_id = movement.id
			AND current_history.id = movement.current_valuation_history_id
		WHERE movement.id = company_fund_valuation_jobs.transaction_id
			AND current_history.usd_valuation_source = 'MANUAL'
	)
ORDER BY COALESCE(next_attempt_at, created_at), id
FOR UPDATE SKIP LOCKED
LIMIT 1`

const updateClaimedCompanyFundValuationJobSQL = `
UPDATE company_fund_valuation_jobs
SET job_state = 'LEASED',
	lease_owner = $2,
	lease_expires_at = clock_timestamp() + ($3::bigint * INTERVAL '1 microsecond'),
	next_attempt_at = NULL,
	attempt_count = attempt_count + 1,
	updated_at = clock_timestamp()
WHERE id = $1
  AND (
	job_state = 'PENDING'
	OR (job_state = 'RETRY_WAIT' AND next_attempt_at <= clock_timestamp())
	OR (job_state = 'LEASED' AND lease_expires_at <= clock_timestamp())
  )
RETURNING attempt_count, lease_expires_at`

const renewCompanyFundValuationJobLeaseSQL = `
UPDATE company_fund_valuation_jobs
SET lease_expires_at = clock_timestamp() + ($3::bigint * INTERVAL '1 microsecond'),
	updated_at = clock_timestamp()
WHERE id = $1
  AND job_state = 'LEASED'
  AND lease_owner = $2
  AND lease_expires_at > clock_timestamp()
RETURNING lease_expires_at`

const finalizeCompanyFundValuationJobSQL = `
UPDATE company_fund_valuation_jobs
SET job_state = $3,
	next_attempt_at = $4,
	lease_owner = NULL,
	lease_expires_at = NULL,
	completed_at = CASE WHEN $3 IN ('SUCCEEDED', 'SUPERSEDED', 'FAILED') THEN clock_timestamp() ELSE NULL END,
	last_error = $5,
	updated_at = clock_timestamp()
WHERE id = $1
  AND job_state = 'LEASED'
  AND lease_owner = $2
  AND lease_expires_at > clock_timestamp()
RETURNING id`
