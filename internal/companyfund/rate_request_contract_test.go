package companyfund

import (
	"strings"
	"testing"
	"time"
)

func TestRateRequestHistoricalBucketValidationAndNormalization(t *testing.T) {
	historical := newRateRequestReservationInput("coingecko:history")
	historical.RequestKind = RateRequestKindHistorical
	if err := historical.validate(); err == nil {
		t.Fatal("historical request without bucket should be rejected")
	}
	bucket := time.Date(2026, 7, 9, 23, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	historical.NormalizedBucketStart = &bucket
	if err := historical.validate(); err != nil {
		t.Fatalf("historical request with bucket validation error = %v", err)
	}
	if got := historical.canonical().NormalizedBucketStart; got == nil || !got.Equal(bucket.UTC()) || got.Location() != time.UTC {
		t.Fatalf("historical bucket canonicalization = %#v, want UTC %s", got, bucket.UTC())
	}
	current := newRateRequestReservationInput("coingecko:current")
	current.NormalizedBucketStart = &bucket
	if err := current.validate(); err == nil {
		t.Fatal("current request with historical bucket should be rejected")
	}
}

func TestRateRequestSQLDoesNotTouchOtherIngestionState(t *testing.T) {
	for name, statement := range map[string]string{
		"budget insert": insertRateBudgetPeriodSQL, "budget reserve": reserveRateBudgetPeriodSQL,
		"request insert": insertRateRequestAttemptSQL, "request claim": claimNextRateRequestSQL,
		"request dispatch": markRateRequestDispatchedSQL, "request completion": finalizeDispatchedRateRequestSQL,
	} {
		lower := strings.ToLower(statement)
		if strings.Contains(lower, "raw") || strings.Contains(lower, "process_status") {
			t.Fatalf("%s SQL must not couple rate requests to other ingestion state: %s", name, statement)
		}
	}
}

func TestRateBudgetAdvisoryLockKey_IsStableForCanonicalBillingDay(t *testing.T) {
	utc := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	localSameInstant := time.Date(2026, 7, 10, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	first := RateBudgetAdvisoryLockKey("coingecko", utc, "2026-07")
	second := RateBudgetAdvisoryLockKey("coingecko", localSameInstant, "2026-07")
	if first != second || first < 0 {
		t.Fatalf("advisory lock key must be stable and non-negative: %d != %d", first, second)
	}
	if first == RateBudgetAdvisoryLockKey("coingecko", utc, "2026-08") {
		t.Fatal("different period keys should not reuse the deterministic advisory key")
	}
	utcPlusEightMidnight := time.Date(2026, 7, 10, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	previousUTCDay := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	if got, want := RateBudgetAdvisoryLockKey("coingecko", utcPlusEightMidnight, "2026-07"), RateBudgetAdvisoryLockKey("coingecko", previousUTCDay, "2026-07"); got != want {
		t.Fatalf("billing anchor must use its UTC day regardless of caller location: %d != %d", got, want)
	}
}

func TestRateRequestLogicalLock_CoversSameKeyAcrossBillingPeriods(t *testing.T) {
	anchor := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	nextAnchor := anchor.AddDate(0, 1, 0)
	logicalKey := "current:bitcoin:usd"
	logicalLock := RateRequestAdvisoryLockKey("coingecko", logicalKey)
	if logicalLock != RateRequestAdvisoryLockKey("coingecko", logicalKey) {
		t.Fatal("same provider/logical key must keep one lock across billing rollover")
	}
	if logicalLock == RateRequestAdvisoryLockKey("cryptocompare", logicalKey) {
		t.Fatal("provider must scope a logical-request advisory lock")
	}
	if RateBudgetAdvisoryLockKey("coingecko", anchor, "2026-07") == RateBudgetAdvisoryLockKey("coingecko", nextAnchor, "2026-08") {
		t.Fatal("different billing periods should retain independent quota locks")
	}
	if logicalLock == RateBudgetAdvisoryLockKey("coingecko", anchor, "2026-07") {
		t.Fatal("logical and budget locks must use distinct domains")
	}
}

func historicalRateRequestInput(logicalRequestKey string) RateRequestReservationInput {
	input := newRateRequestReservationInput(logicalRequestKey)
	bucket := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	input.RequestKind = RateRequestKindHistorical
	input.NormalizedBucketStart = &bucket
	return input
}
