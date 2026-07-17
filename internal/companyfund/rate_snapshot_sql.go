package companyfund

const rateSnapshotReturningColumns = `
id,
provider,
asset_identity_key,
COALESCE(provider_asset_id, ''),
COALESCE(provider_platform_id, ''),
COALESCE(asset_contract, ''),
base_currency,
quote_currency,
rate::TEXT,
method,
granularity,
bucket_start,
effective_at,
available_at,
fetched_at,
cutoff_at,
COALESCE(snapshot_group_id, ''),
policy_version,
COALESCE(provider_revision, ''),
internal_revision,
supersedes_snapshot_id,
numerator_snapshot_id,
denominator_snapshot_id,
source_provider_fact_id,
source_payload_digest,
is_eligible_leaf,
is_final,
originating_rate_request_id`

const rateSnapshotSelectedColumns = `
snapshot.id,
snapshot.provider,
snapshot.asset_identity_key,
COALESCE(snapshot.provider_asset_id, ''),
COALESCE(snapshot.provider_platform_id, ''),
COALESCE(snapshot.asset_contract, ''),
snapshot.base_currency,
snapshot.quote_currency,
snapshot.rate::TEXT,
snapshot.method,
snapshot.granularity,
snapshot.bucket_start,
snapshot.effective_at,
snapshot.available_at,
snapshot.fetched_at,
snapshot.cutoff_at,
COALESCE(snapshot.snapshot_group_id, ''),
snapshot.policy_version,
COALESCE(snapshot.provider_revision, ''),
snapshot.internal_revision,
snapshot.supersedes_snapshot_id,
snapshot.numerator_snapshot_id,
snapshot.denominator_snapshot_id,
snapshot.source_provider_fact_id,
snapshot.source_payload_digest,
snapshot.is_eligible_leaf,
snapshot.is_final,
snapshot.originating_rate_request_id,
CASE
	WHEN request.request_kind IN ('CURRENT', 'CONTRACT_CHECK') THEN 'CURRENT'
	WHEN request.request_kind = 'HISTORICAL' THEN 'HISTORICAL'
	WHEN request.request_kind = 'RETRY' AND request.normalized_bucket_start IS NULL THEN 'CURRENT'
	WHEN request.request_kind = 'RETRY' THEN 'HISTORICAL'
	ELSE ''
END`

const rateSnapshotSeriesAdvisoryLockSQL = `
SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`

// Every append takes this fixed graph lock before its narrower series lock.
// A correction's recursive descendant scan and a concurrently appended derived
// leaf must therefore share one serialization boundary.
const rateSnapshotDependencyGraphAdvisoryLockSQL = `
SELECT pg_advisory_xact_lock(hashtextextended('company-fund-rate-snapshot-dependency-graph-v1', 0))`

const selectRateSnapshotRequestProvenanceSQL = `
SELECT provider, request_kind, normalized_bucket_start
FROM company_fund_rate_requests
WHERE id = $1
FOR KEY SHARE`

const selectRateSnapshotFactProvenanceSQL = `
SELECT channel, source_payload_digest
FROM company_fund_provider_transaction_facts
WHERE id = $1
FOR KEY SHARE`

const selectRateSnapshotBySourceDigestSQL = `
SELECT ` + rateSnapshotSelectedColumns + `
FROM company_fund_rate_snapshots AS snapshot
LEFT JOIN company_fund_rate_requests AS request
	ON request.id = snapshot.originating_rate_request_id
WHERE snapshot.provider = $1
	AND snapshot.asset_identity_key = $2
	AND snapshot.quote_currency = $3
	AND snapshot.method = $4
	AND snapshot.granularity = $5
	AND snapshot.bucket_start = $6
	AND snapshot.policy_version = $7
	AND snapshot.source_payload_digest = $8
FOR KEY SHARE OF snapshot`

const selectEligibleRateSnapshotLeafSQL = `
SELECT ` + rateSnapshotSelectedColumns + `
FROM company_fund_rate_snapshots AS snapshot
LEFT JOIN company_fund_rate_requests AS request
	ON request.id = snapshot.originating_rate_request_id
WHERE snapshot.provider = $1
	AND snapshot.asset_identity_key = $2
	AND snapshot.quote_currency = $3
	AND snapshot.method = $4
	AND snapshot.granularity = $5
	AND snapshot.bucket_start = $6
	AND snapshot.policy_version = $7
	AND snapshot.is_eligible_leaf = true
FOR UPDATE OF snapshot`

const selectRateSnapshotByIDForUpdateSQL = `
SELECT ` + rateSnapshotSelectedColumns + `
FROM company_fund_rate_snapshots AS snapshot
LEFT JOIN company_fund_rate_requests AS request
	ON request.id = snapshot.originating_rate_request_id
WHERE snapshot.id = $1
FOR UPDATE OF snapshot`

// Retiring the leaf is the sole UPDATE performed by this repository. The DDL
// trigger allows exactly this state transition and the audit timestamp change.
const retireEligibleRateSnapshotLeafSQL = `
UPDATE company_fund_rate_snapshots
SET is_eligible_leaf = false,
	updated_at = NOW()
WHERE id = $1
	AND is_eligible_leaf = true
RETURNING id`

// retireDependentRateSnapshotLeavesSQL traverses all derived descendants with
// UNION (not UNION ALL) so legacy/corrupt graph cycles cannot loop forever.
// Only the final UPDATE changes an eligible leaf, which is the one mutation
// accepted by the rate-snapshot append-only trigger.
const retireDependentRateSnapshotLeavesSQL = `
WITH RECURSIVE dependent_snapshot_ids(id) AS (
	SELECT child.id
	FROM company_fund_rate_snapshots AS child
	WHERE child.method = 'COINGECKO_BTC_CROSS'
	  AND (child.numerator_snapshot_id = $1 OR child.denominator_snapshot_id = $1)
	UNION
	SELECT child.id
	FROM dependent_snapshot_ids AS dependency
	JOIN company_fund_rate_snapshots AS child
		ON child.numerator_snapshot_id = dependency.id
		OR child.denominator_snapshot_id = dependency.id
	WHERE child.method = 'COINGECKO_BTC_CROSS'
), locked_dependents AS (
	SELECT child.id
	FROM company_fund_rate_snapshots AS child
	JOIN dependent_snapshot_ids AS dependency ON dependency.id = child.id
	WHERE child.is_eligible_leaf = true
	ORDER BY child.id
	FOR UPDATE OF child
)
UPDATE company_fund_rate_snapshots AS snapshot
SET is_eligible_leaf = false,
	updated_at = clock_timestamp()
FROM locked_dependents
WHERE snapshot.id = locked_dependents.id
	AND snapshot.is_eligible_leaf = true
RETURNING snapshot.id`

const insertRateSnapshotSQL = `
INSERT INTO company_fund_rate_snapshots (
	provider,
	asset_identity_key,
	provider_asset_id,
	provider_platform_id,
	asset_contract,
	base_currency,
	quote_currency,
	rate,
	method,
	granularity,
	bucket_start,
	effective_at,
	available_at,
	fetched_at,
	cutoff_at,
	snapshot_group_id,
	policy_version,
	provider_revision,
	internal_revision,
	supersedes_snapshot_id,
	numerator_snapshot_id,
	denominator_snapshot_id,
	source_provider_fact_id,
	source_payload_digest,
	is_eligible_leaf,
	is_final,
	originating_rate_request_id
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8::numeric, $9, $10, $11,
	$12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23,
	$24, true, $25, $26
)
RETURNING ` + rateSnapshotReturningColumns

const selectLatestUsableRateSnapshotSQL = `
SELECT ` + rateSnapshotReturningColumns + `
FROM company_fund_rate_snapshots AS snapshot
WHERE snapshot.provider = $1
	AND snapshot.asset_identity_key = $2
	AND snapshot.provider_asset_id IS NOT DISTINCT FROM $3
	AND snapshot.provider_platform_id IS NOT DISTINCT FROM $4
	AND snapshot.asset_contract IS NOT DISTINCT FROM $5
	AND snapshot.base_currency = $6
	AND snapshot.quote_currency = $7
	AND snapshot.method = $8
	AND snapshot.granularity = $9
	AND snapshot.policy_version = $10
	AND snapshot.is_eligible_leaf = true
	AND snapshot.available_at <= $12
	AND COALESCE(snapshot.effective_at, snapshot.bucket_start) <= $11
	AND COALESCE(snapshot.effective_at, snapshot.bucket_start) >= $11 - ($13::bigint * INTERVAL '1 microsecond')
	AND (
		snapshot.method <> 'COINGECKO_BTC_CROSS'
		OR (
			EXISTS (
				SELECT 1
				FROM company_fund_rate_snapshots AS numerator
				WHERE numerator.id = snapshot.numerator_snapshot_id
					AND numerator.is_eligible_leaf = true
			)
			AND EXISTS (
				SELECT 1
				FROM company_fund_rate_snapshots AS denominator
				WHERE denominator.id = snapshot.denominator_snapshot_id
					AND denominator.is_eligible_leaf = true
			)
		)
	)
ORDER BY COALESCE(snapshot.effective_at, snapshot.bucket_start) DESC, snapshot.available_at DESC, snapshot.internal_revision DESC
LIMIT 1`
