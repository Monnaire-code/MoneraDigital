package companyfund

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const (
	maxRateSnapshotProviderBytes               = 64
	maxRateSnapshotAssetIdentityKeyBytes       = 512
	maxRateSnapshotProviderAssetIDBytes        = 256
	maxRateSnapshotCurrencyBytes               = 64
	maxRateSnapshotMethodBytes                 = 64
	maxRateSnapshotGranularityBytes            = 16
	maxRateSnapshotPolicyVersionBytes          = 64
	maxRateSnapshotGroupIDBytes                = 256
	maxRateSnapshotRevisionBytes               = 128
	rateSnapshotNumericScale             int32 = 18
	rateSnapshotIntegerDigits                  = 47 // NUMERIC(65,18)

	rateSnapshotCoinGeckoProvider  = "COINGECKO"
	rateSnapshotBTCProviderAssetID = "bitcoin"
)

// RateSnapshotInput contains only immutable provider/audit facts. A corrected
// observation is never represented as an UPDATE: AppendRateSnapshot retires
// the previous eligible leaf and appends a new internal revision.
//
// PriceKind, DerivedTargetAt and AvailableAtCutoffAt are validation inputs;
// they are deliberately not persisted because the durable request attempt and
// immutable observation times are the source of truth for later valuation.
type RateSnapshotInput struct {
	Provider                 string
	AssetIdentityKey         string
	ProviderAssetID          string
	ProviderPlatformID       string
	AssetContract            string
	BaseCurrency             string
	QuoteCurrency            string
	Rate                     decimal.Decimal
	Method                   string
	Granularity              string
	BucketStart              time.Time
	EffectiveAt              *time.Time
	AvailableAt              time.Time
	FetchedAt                time.Time
	CutoffAt                 *time.Time
	SnapshotGroupID          string
	PolicyVersion            string
	ProviderRevision         string
	SourceProviderFactID     *int64
	SourcePayloadDigest      string
	OriginatingRateRequestID *int64
	IsFinal                  bool

	NumeratorSnapshotID   *int64
	DenominatorSnapshotID *int64
	PriceKind             MarketPriceKind
	DerivedTargetAt       *time.Time
	AvailableAtCutoffAt   *time.Time
	HistoricalMaxGap      time.Duration
}

// RateSnapshotRecord is the immutable rate-snapshot row returned by this
// repository. Optional database columns use pointers so a missing observation
// is never silently represented as a zero timestamp or ID.
type RateSnapshotRecord struct {
	ID                       int64
	Provider                 string
	AssetIdentityKey         string
	ProviderAssetID          string
	ProviderPlatformID       string
	AssetContract            string
	BaseCurrency             string
	QuoteCurrency            string
	Rate                     decimal.Decimal
	Method                   string
	Granularity              string
	BucketStart              time.Time
	EffectiveAt              *time.Time
	AvailableAt              time.Time
	FetchedAt                time.Time
	CutoffAt                 *time.Time
	SnapshotGroupID          string
	PolicyVersion            string
	ProviderRevision         string
	InternalRevision         int
	SupersedesSnapshotID     *int64
	NumeratorSnapshotID      *int64
	DenominatorSnapshotID    *int64
	SourceProviderFactID     *int64
	SourcePayloadDigest      string
	IsEligibleLeaf           bool
	IsFinal                  bool
	OriginatingRateRequestID *int64
	OriginatingRequestKind   string
}

// RateSnapshotAppendResult distinguishes a newly appended immutable revision
// from an exact source-digest retry that read back the existing row.
type RateSnapshotAppendResult struct {
	Snapshot RateSnapshotRecord
	Inserted bool
}

// RateSnapshotLookup is the exact valuation contract for a latest-usable
// lookup. Asset/provider/platform/contract and policy are all included so a
// similarly named asset cannot supply a rate from another mapping.
type RateSnapshotLookup struct {
	Provider            string
	AssetIdentityKey    string
	ProviderAssetID     string
	ProviderPlatformID  string
	AssetContract       string
	BaseCurrency        string
	QuoteCurrency       string
	Method              string
	Granularity         string
	PolicyVersion       string
	AsOf                time.Time
	AvailableAtCutoffAt *time.Time
	MaxGap              time.Duration
}

func (input RateSnapshotInput) validate() error {
	for _, field := range []struct {
		label string
		value string
		max   int
	}{
		{"rate snapshot provider", input.Provider, maxRateSnapshotProviderBytes},
		{"rate snapshot asset identity key", input.AssetIdentityKey, maxRateSnapshotAssetIdentityKeyBytes},
		{"rate snapshot base currency", input.BaseCurrency, maxRateSnapshotCurrencyBytes},
		{"rate snapshot quote currency", input.QuoteCurrency, maxRateSnapshotCurrencyBytes},
		{"rate snapshot method", input.Method, maxRateSnapshotMethodBytes},
		{"rate snapshot granularity", input.Granularity, maxRateSnapshotGranularityBytes},
		{"rate snapshot policy version", input.PolicyVersion, maxRateSnapshotPolicyVersionBytes},
	} {
		if err := validateRateSnapshotString(field.label, field.value, field.max, true); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		label string
		value string
		max   int
	}{
		{"rate snapshot provider asset ID", input.ProviderAssetID, maxRateSnapshotProviderAssetIDBytes},
		{"rate snapshot provider platform ID", input.ProviderPlatformID, maxRateSnapshotProviderAssetIDBytes},
		{"rate snapshot asset contract", input.AssetContract, maxRateSnapshotProviderAssetIDBytes},
		{"rate snapshot group ID", input.SnapshotGroupID, maxRateSnapshotGroupIDBytes},
		{"rate snapshot provider revision", input.ProviderRevision, maxRateSnapshotRevisionBytes},
	} {
		if err := validateRateSnapshotString(field.label, field.value, field.max, false); err != nil {
			return err
		}
	}
	if !input.PriceKind.Valid() {
		return fmt.Errorf("unsupported rate snapshot price kind %q", input.PriceKind)
	}
	if err := validateRateSnapshotDecimal(input.Rate); err != nil {
		return err
	}
	if !isLowerSHA256Hex(input.SourcePayloadDigest) {
		return fmt.Errorf("rate snapshot source payload digest must be lowercase SHA-256 hex")
	}
	if input.BucketStart.IsZero() || input.BucketStart.Location() != time.UTC {
		return fmt.Errorf("rate snapshot bucket start must be a non-zero UTC time")
	}
	if input.AvailableAt.IsZero() || input.FetchedAt.IsZero() {
		return fmt.Errorf("rate snapshot available and fetched times are required")
	}
	if input.EffectiveAt != nil && input.EffectiveAt.IsZero() {
		return fmt.Errorf("rate snapshot effective time cannot be zero")
	}
	if input.CutoffAt != nil && input.CutoffAt.IsZero() {
		return fmt.Errorf("rate snapshot cutoff time cannot be zero")
	}
	if input.OriginatingRateRequestID != nil && *input.OriginatingRateRequestID <= 0 {
		return fmt.Errorf("originating rate request ID must be positive")
	}
	if input.SourceProviderFactID != nil && *input.SourceProviderFactID <= 0 {
		return fmt.Errorf("source provider fact ID must be positive")
	}
	if input.Method == string(USDValuationMethodCoinGeckoBTCCross) {
		if input.NumeratorSnapshotID == nil || input.DenominatorSnapshotID == nil {
			return fmt.Errorf("%s rate snapshot requires numerator and denominator snapshots", USDValuationMethodCoinGeckoBTCCross)
		}
		if *input.NumeratorSnapshotID <= 0 || *input.DenominatorSnapshotID <= 0 {
			return fmt.Errorf("%s rate snapshot input IDs must be positive", USDValuationMethodCoinGeckoBTCCross)
		}
	} else if input.NumeratorSnapshotID != nil || input.DenominatorSnapshotID != nil {
		return fmt.Errorf("rate snapshot numerator and denominator are only valid for %s", USDValuationMethodCoinGeckoBTCCross)
	}
	return nil
}

func validateRateSnapshotDecimal(value decimal.Decimal) error {
	if !value.IsPositive() {
		return fmt.Errorf("rate snapshot rate must be positive")
	}
	if value.Exponent() < -rateSnapshotNumericScale {
		return fmt.Errorf("rate snapshot rate exceeds NUMERIC(65,18) fractional precision")
	}
	integerDigits := len(strings.TrimLeft(value.Truncate(0).Abs().String(), "0"))
	if integerDigits > rateSnapshotIntegerDigits {
		return fmt.Errorf("rate snapshot rate exceeds NUMERIC(65,18) integer precision")
	}
	return nil
}

func validateRateSnapshotString(label, value string, maxBytes int, required bool) error {
	if value == "" && !required {
		return nil
	}
	if !utf8.ValidString(value) || len(value) > maxBytes || strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be a non-blank, trimmed UTF-8 string within %d bytes", label, maxBytes)
	}
	return nil
}

func (input RateSnapshotInput) seriesKey() string {
	return lengthDelimitedTuple([]string{
		"company-fund-rate-snapshot",
		input.Provider,
		input.AssetIdentityKey,
		input.QuoteCurrency,
		input.Method,
		input.Granularity,
		input.BucketStart.UTC().Format(time.RFC3339Nano),
		input.PolicyVersion,
	})
}

func (input RateSnapshotInput) seriesArgs(extra ...any) []any {
	args := []any{
		input.Provider,
		input.AssetIdentityKey,
		input.QuoteCurrency,
		input.Method,
		input.Granularity,
		input.BucketStart,
		input.PolicyVersion,
	}
	return append(args, extra...)
}
