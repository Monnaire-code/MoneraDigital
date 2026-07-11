package companyfund

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"
)

func (input RateRequestReservationInput) validate() error {
	if err := validateRequiredString("rate request provider", input.Provider, maxRateProviderBytes); err != nil {
		return err
	}
	if err := input.Budget.validate(); err != nil {
		return err
	}
	if input.Provider != input.Budget.Provider {
		return fmt.Errorf("%w: request provider %q, budget provider %q", ErrRateRequestProviderBudgetMismatch, input.Provider, input.Budget.Provider)
	}
	if err := validateRequiredString("rate logical request key", input.LogicalRequestKey, maxRateLogicalRequestKeyBytes); err != nil {
		return err
	}
	if !input.RequestKind.Valid() {
		return fmt.Errorf("unsupported rate request kind %q", input.RequestKind)
	}
	switch input.RequestKind {
	case RateRequestKindHistorical:
		if input.NormalizedBucketStart == nil || input.NormalizedBucketStart.IsZero() {
			return fmt.Errorf("historical rate request requires a normalized bucket start")
		}
	case RateRequestKindCurrent, RateRequestKindContractCheck:
		if input.NormalizedBucketStart != nil {
			return fmt.Errorf("%s rate request cannot carry a historical normalized bucket", input.RequestKind)
		}
	}
	if input.RequestKind != RateRequestKindRetry && input.NotBefore != nil {
		return fmt.Errorf("only retry rate requests may have a not-before time")
	}
	if input.NotBefore != nil && input.NotBefore.IsZero() {
		return fmt.Errorf("rate request not-before time must not be zero")
	}
	return nil
}

func (config RateBudgetConfig) validate() error {
	if err := validateRequiredString("rate budget provider", config.Provider, maxRateProviderBytes); err != nil {
		return err
	}
	if config.BillingAnchor.IsZero() {
		return fmt.Errorf("rate budget billing anchor is required")
	}
	if err := validateRequiredString("rate budget period key", config.PeriodKey, maxRatePeriodKeyBytes); err != nil {
		return err
	}
	if config.PeriodStart.IsZero() || config.PeriodEnd.IsZero() || !config.PeriodEnd.After(config.PeriodStart) {
		return fmt.Errorf("rate budget period end must be after its start")
	}
	if config.CallLimit < 0 {
		return fmt.Errorf("rate budget call limit must be non-negative")
	}
	if config.PlanName != "" && len(config.PlanName) > maxRatePlanNameBytes {
		return fmt.Errorf("rate budget plan name must be at most %d bytes", maxRatePlanNameBytes)
	}
	if config.LicenseReference != "" && len(config.LicenseReference) > maxRateLicenseReferenceBytes {
		return fmt.Errorf("rate budget license reference must be at most %d bytes", maxRateLicenseReferenceBytes)
	}
	return validateRequiredString("rate budget config version", config.ConfigVersion, maxRateConfigVersionBytes)
}

func (config RateBudgetConfig) canonical() RateBudgetConfig {
	config.BillingAnchor = canonicalRateBillingAnchor(config.BillingAnchor)
	config.PeriodStart = config.PeriodStart.UTC()
	config.PeriodEnd = config.PeriodEnd.UTC()
	return config
}

func (input RateRequestReservationInput) canonical() RateRequestReservationInput {
	input.Budget = input.Budget.canonical()
	if input.NormalizedBucketStart != nil {
		bucketStart := input.NormalizedBucketStart.UTC()
		input.NormalizedBucketStart = &bucketStart
	}
	if input.NotBefore != nil {
		notBefore := input.NotBefore.UTC()
		input.NotBefore = &notBefore
	}
	return input
}

func (period rateBudgetPeriodRecord) matchesConfig(config RateBudgetConfig) error {
	if period.Provider != config.Provider ||
		!sameRateBillingAnchor(period.BillingAnchor, config.BillingAnchor) ||
		period.PeriodKey != config.PeriodKey ||
		!period.PeriodStart.Equal(config.PeriodStart) ||
		!period.PeriodEnd.Equal(config.PeriodEnd) ||
		period.CallLimit != config.CallLimit ||
		period.PlanName != config.PlanName ||
		period.LicenseReference != config.LicenseReference ||
		period.ConfigVersion != config.ConfigVersion {
		return fmt.Errorf("%w for provider %q billing anchor %s period %q", ErrRateBudgetConfigurationMismatch, config.Provider, config.BillingAnchor.Format("2006-01-02"), config.PeriodKey)
	}
	return nil
}

func (completion RateRequestCompletion) validate() error {
	if !completion.State.terminal() {
		return fmt.Errorf("rate request completion state must be terminal")
	}
	if completion.ResponseSnapshotGroup != "" && len(completion.ResponseSnapshotGroup) > maxRateResponseSnapshotGroupBytes {
		return fmt.Errorf("rate response snapshot group must be at most %d bytes", maxRateResponseSnapshotGroupBytes)
	}
	if completion.ErrorCode != "" && len(completion.ErrorCode) > maxRateErrorCodeBytes {
		return fmt.Errorf("rate request error code must be at most %d bytes", maxRateErrorCodeBytes)
	}
	if len(completion.ErrorDetail) > maxRateErrorDetailBytes {
		return fmt.Errorf("rate request error detail must be at most %d bytes", maxRateErrorDetailBytes)
	}
	return nil
}

func validateRateRequestLeaseOwner(owner string) error {
	return validateRequiredString("rate request lease owner", owner, maxRateLeaseOwnerBytes)
}

func rateRequestLeaseDurationMicroseconds(leaseDuration time.Duration) (int64, error) {
	if leaseDuration <= 0 || leaseDuration.Microseconds() <= 0 {
		return 0, fmt.Errorf("rate request lease duration must be at least one microsecond")
	}
	return leaseDuration.Microseconds(), nil
}

func canonicalRateBillingAnchor(anchor time.Time) time.Time {
	utc := anchor.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func sameRateBillingAnchor(left, right time.Time) bool {
	return canonicalRateBillingAnchor(left).Equal(canonicalRateBillingAnchor(right))
}

// RateRequestAdvisoryLockKey serializes reservations for one provider/logical
// key even when the billing period rolls over. It is acquired before the
// budget-period lock, so all reservation paths share one global lock order.
func RateRequestAdvisoryLockKey(provider, logicalRequestKey string) int64 {
	return rateAdvisoryLockKey("company-fund-rate-request", provider, logicalRequestKey)
}

// RateBudgetAdvisoryLockKey serializes all quota reservations for one
// provider billing period. It is acquired after RateRequestAdvisoryLockKey in
// the same transaction whenever a new request attempt needs quota.
func RateBudgetAdvisoryLockKey(provider string, billingAnchor time.Time, periodKey string) int64 {
	anchor := canonicalRateBillingAnchor(billingAnchor)
	return rateAdvisoryLockKey("company-fund-rate-budget", provider, anchor.Format("2006-01-02"), periodKey)
}

func rateAdvisoryLockKey(parts ...string) int64 {
	hash := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	sum := hash.Sum(nil)
	return int64(binary.BigEndian.Uint64(sum[:8]) & (uint64(1)<<63 - 1))
}
