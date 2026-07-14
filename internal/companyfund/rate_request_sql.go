package companyfund

const rateBudgetPeriodColumns = `
id,
provider,
billing_anchor,
period_key,
period_start,
period_end,
call_limit,
reserved_calls,
used_calls,
plan_name,
license_reference,
config_version,
config_frozen_at,
first_reserved_at`

const rateRequestAttemptColumns = `
id,
budget_period_id,
provider,
logical_request_key,
request_kind,
normalized_bucket_start,
attempt_no,
request_state,
not_before,
lease_owner,
lease_expires_at,
reserved_at,
dispatched_at,
charged_at,
completed_at,
response_snapshot_group_id,
error_code,
error_detail`

const lockRateRequestLogicalKeySQL = `SELECT pg_advisory_xact_lock($1)`

const lockRateBudgetPeriodSQL = `SELECT pg_advisory_xact_lock($1)`

const insertRateBudgetPeriodSQL = `
INSERT INTO company_fund_rate_budget_periods (
	provider,
	billing_anchor,
	period_key,
	period_start,
	period_end,
	call_limit,
	plan_name,
	license_reference,
	config_version
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (provider, billing_anchor, period_key) DO NOTHING`

const selectRateBudgetPeriodForUpdateSQL = `
SELECT ` + rateBudgetPeriodColumns + `
FROM company_fund_rate_budget_periods
WHERE provider = $1
  AND billing_anchor = $2
  AND period_key = $3
FOR UPDATE`

const selectActiveRateRequestForUpdateSQL = `
SELECT ` + rateRequestAttemptColumns + `
FROM company_fund_rate_requests
WHERE provider = $1
  AND logical_request_key = $2
  AND request_state IN ('PENDING', 'LEASED', 'RETRY_WAIT', 'DISPATCHED')
ORDER BY attempt_no DESC
LIMIT 1
FOR UPDATE`

const selectLatestRateRequestForUpdateSQL = `
SELECT attempt_no, request_state
FROM company_fund_rate_requests
WHERE provider = $1
  AND logical_request_key = $2
ORDER BY attempt_no DESC
LIMIT 1
FOR UPDATE`

const reserveRateBudgetPeriodSQL = `
UPDATE company_fund_rate_budget_periods
SET reserved_calls = reserved_calls + 1,
    config_frozen_at = COALESCE(config_frozen_at, NOW()),
    first_reserved_at = COALESCE(first_reserved_at, NOW()),
    updated_at = NOW()
WHERE id = $1
  AND provider = $2
  AND reserved_calls < call_limit
RETURNING reserved_calls`

const insertRateRequestAttemptSQL = `
INSERT INTO company_fund_rate_requests (
	budget_period_id,
	provider,
	logical_request_key,
	request_kind,
	normalized_bucket_start,
	attempt_no,
	request_state,
	not_before
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING ` + rateRequestAttemptColumns

const claimNextRateRequestSQL = `
SELECT ` + rateRequestAttemptColumns + `
FROM company_fund_rate_requests
WHERE request_state = 'PENDING'
   OR (request_state = 'RETRY_WAIT' AND not_before <= clock_timestamp())
   OR (request_state = 'LEASED' AND lease_expires_at <= clock_timestamp())
ORDER BY COALESCE(not_before, reserved_at), id
FOR UPDATE SKIP LOCKED
LIMIT 1`

const updateClaimedRateRequestSQL = `
UPDATE company_fund_rate_requests
SET request_state = 'LEASED',
    lease_owner = $2,
    lease_expires_at = clock_timestamp() + ($3::bigint * INTERVAL '1 microsecond'),
    not_before = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND (
	request_state = 'PENDING'
	OR (request_state = 'RETRY_WAIT' AND not_before <= clock_timestamp())
	OR (request_state = 'LEASED' AND lease_expires_at <= clock_timestamp())
  )
RETURNING lease_expires_at`

const selectRateRequestLogicalKeySQL = `
SELECT provider, logical_request_key
FROM company_fund_rate_requests
WHERE id = $1`

const markRateRequestDispatchedSQL = `
UPDATE company_fund_rate_requests
SET request_state = 'DISPATCHED',
    lease_owner = NULL,
    lease_expires_at = NULL,
    dispatched_at = clock_timestamp(),
    charged_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE id = $1
  AND request_state = 'LEASED'
  AND lease_owner = $2
  AND lease_expires_at > clock_timestamp()
RETURNING budget_period_id, provider`

const consumeReservedRateBudgetCallSQL = `
UPDATE company_fund_rate_budget_periods
SET used_calls = used_calls + 1,
    updated_at = NOW()
WHERE id = $1
  AND provider = $2
  AND used_calls < reserved_calls
RETURNING used_calls`

const finalizeDispatchedRateRequestSQL = `
UPDATE company_fund_rate_requests
SET request_state = $2,
    completed_at = clock_timestamp(),
    response_snapshot_group_id = $3,
    error_code = $4,
    error_detail = $5,
    updated_at = clock_timestamp()
WHERE id = $1
  AND request_state = 'DISPATCHED'
RETURNING id`

const recoverStaleDispatchedRateRequestsSQL = `
WITH stale_requests AS (
	SELECT id
	FROM company_fund_rate_requests
	WHERE request_state = 'DISPATCHED'
	  AND dispatched_at <= clock_timestamp() - ($1::bigint * INTERVAL '1 microsecond')
	ORDER BY dispatched_at, id
	FOR UPDATE SKIP LOCKED
	LIMIT $2
), recovered AS (
	UPDATE company_fund_rate_requests AS request
	SET request_state = 'UNKNOWN',
		completed_at = clock_timestamp(),
		error_code = 'DISPATCH_RECOVERY_TIMEOUT',
		error_detail = 'dispatched request exceeded the recovery window without a recorded outcome',
		updated_at = clock_timestamp()
	FROM stale_requests
	WHERE request.id = stale_requests.id
	  AND request.request_state = 'DISPATCHED'
	  AND request.dispatched_at <= clock_timestamp() - ($1::bigint * INTERVAL '1 microsecond')
	RETURNING request.id
)
SELECT COUNT(*) FROM recovered`
