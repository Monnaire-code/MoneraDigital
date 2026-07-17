package companyfund

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const (
	maxProviderFactIdentityKeyBytes       = 256
	maxProviderFactAccountKeyBytes        = 128
	maxProviderFactReferenceBytes         = 256
	maxProviderFactCurrencyBytes          = 64
	maxProviderFactExtrasBytes            = 16 << 10
	providerFactNumericScale        int32 = 18
	providerFactIntegerDigits             = 47 // NUMERIC(65,18)
)

// ProviderTransactionFactInput is the durable, normalized provider parent or
// group fact. Complete provider messages remain owned by provider events; this
// input accepts only allowlisted audit fields that survive payload retention.
type ProviderTransactionFactInput struct {
	Channel                   Channel
	ProviderAccountKey        string
	ProviderTransactionID     string
	ProviderGroupID           string
	FactIdentityKey           string
	FactVersion               int
	SourceProviderEventID     int64
	SourcePayloadDigest       string
	ProviderOccurredAt        *time.Time
	ProviderAmount            *decimal.Decimal
	ProviderCurrency          string
	ProviderReportedUSD       *decimal.Decimal
	ConversionFromCurrency    string
	ConversionToCurrency      string
	ConversionRate            *decimal.Decimal
	ConversionBuyAmount       *decimal.Decimal
	ConversionSellAmount      *decimal.Decimal
	ValueScope                ProviderValueScope
	AllocationState           ProviderFactAllocationState
	DerivationContractVersion string
	ProviderExtrasJSON        []byte
}

// ProviderTransactionFactInsertResult distinguishes an inserted fact from an
// exact retry that converged on the existing immutable fact.
type ProviderTransactionFactInsertResult struct {
	Fact     ProviderTransactionFact
	Inserted bool
}

const selectProviderEventForFactSQL = `
SELECT channel, source_payload_digest
FROM company_fund_provider_events
WHERE id = $1
FOR KEY SHARE`

const providerTransactionFactReturnedColumns = `
id,
channel,
provider_account_key,
provider_transaction_id,
provider_group_id,
fact_identity_key,
fact_version,
source_provider_event_id,
source_payload_digest,
provider_occurred_at,
provider_amount::TEXT,
provider_currency,
provider_reported_usd_value::TEXT,
conversion_from_currency,
conversion_to_currency,
conversion_rate::TEXT,
conversion_buy_amount::TEXT,
conversion_sell_amount::TEXT,
value_scope,
allocation_state,
derivation_contract_version,
provider_extras::TEXT,
created_at,
updated_at`

const insertProviderTransactionFactSQL = `
INSERT INTO company_fund_provider_transaction_facts (
	channel,
	provider_account_key,
	provider_transaction_id,
	provider_group_id,
	fact_identity_key,
	fact_version,
	source_provider_event_id,
	source_payload_digest,
	provider_occurred_at,
	provider_amount,
	provider_currency,
	provider_reported_usd_value,
	conversion_from_currency,
	conversion_to_currency,
	conversion_rate,
	conversion_buy_amount,
	conversion_sell_amount,
	value_scope,
	allocation_state,
	derivation_contract_version,
	provider_extras
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
	$12, $13, $14, $15, $16, $17, $18, $19, $20, $21::jsonb
)
ON CONFLICT (channel, fact_identity_key, fact_version) DO NOTHING
RETURNING ` + providerTransactionFactReturnedColumns

const selectProviderTransactionFactByIdentitySQL = `
SELECT ` + providerTransactionFactReturnedColumns + `
FROM company_fund_provider_transaction_facts
WHERE channel = $1
  AND fact_identity_key = $2
  AND fact_version = $3`

// InsertProviderTransactionFact verifies source-event provenance and inserts
// the fact in one database transaction. It never treats a parent total as a
// child amount; movement linkage owns any separately-proven allocation.
func (r *DBRepository) InsertProviderTransactionFact(ctx context.Context, input ProviderTransactionFactInput) (ProviderTransactionFactInsertResult, error) {
	providerExtrasJSON, err := input.validate()
	if err != nil {
		return ProviderTransactionFactInsertResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return ProviderTransactionFactInsertResult{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ProviderTransactionFactInsertResult{}, fmt.Errorf("begin provider transaction fact insert: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := verifyProviderEventForFact(ctx, tx, input); err != nil {
		return ProviderTransactionFactInsertResult{}, err
	}

	fact, err := scanProviderTransactionFact(tx.QueryRowContext(ctx, insertProviderTransactionFactSQL,
		input.Channel,
		input.ProviderAccountKey,
		nullableString(input.ProviderTransactionID),
		nullableString(input.ProviderGroupID),
		input.FactIdentityKey,
		input.FactVersion,
		input.SourceProviderEventID,
		input.SourcePayloadDigest,
		nullableTime(input.ProviderOccurredAt),
		nullableProviderFactDecimal(input.ProviderAmount),
		nullableString(input.ProviderCurrency),
		nullableProviderFactDecimal(input.ProviderReportedUSD),
		nullableString(input.ConversionFromCurrency),
		nullableString(input.ConversionToCurrency),
		nullableProviderFactDecimal(input.ConversionRate),
		nullableProviderFactDecimal(input.ConversionBuyAmount),
		nullableProviderFactDecimal(input.ConversionSellAmount),
		input.ValueScope,
		input.AllocationState,
		nullableString(input.DerivationContractVersion),
		providerExtrasJSON,
	))
	if err == nil {
		if err := tx.Commit(); err != nil {
			return ProviderTransactionFactInsertResult{}, fmt.Errorf("commit provider transaction fact insert: %w", err)
		}
		committed = true
		return ProviderTransactionFactInsertResult{Fact: fact, Inserted: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ProviderTransactionFactInsertResult{}, fmt.Errorf("insert provider transaction fact: %w", err)
	}

	existing, err := scanProviderTransactionFact(tx.QueryRowContext(ctx, selectProviderTransactionFactByIdentitySQL, input.Channel, input.FactIdentityKey, input.FactVersion))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProviderTransactionFactInsertResult{}, fmt.Errorf("provider transaction fact identity conflict did not return an existing row")
		}
		return ProviderTransactionFactInsertResult{}, fmt.Errorf("read existing provider transaction fact: %w", err)
	}
	if conflictField, err := immutableProviderTransactionFactConflict(existing, input, providerExtrasJSON); err != nil {
		return ProviderTransactionFactInsertResult{}, err
	} else if conflictField != "" {
		return ProviderTransactionFactInsertResult{}, fmt.Errorf("provider transaction fact identity/version conflicts on immutable field %s", conflictField)
	}
	if err := tx.Commit(); err != nil {
		return ProviderTransactionFactInsertResult{}, fmt.Errorf("commit provider transaction fact readback: %w", err)
	}
	committed = true
	return ProviderTransactionFactInsertResult{Fact: existing}, nil
}

// GetProviderTransactionFact reads a durable normalized fact by its immutable
// identity/version. It does not load a provider event's retained bytes.
func (r *DBRepository) GetProviderTransactionFact(ctx context.Context, channel Channel, factIdentityKey string, factVersion int) (ProviderTransactionFact, error) {
	if !channel.Valid() {
		return ProviderTransactionFact{}, fmt.Errorf("unsupported provider transaction fact channel %q", channel)
	}
	if err := validateRequiredString("provider transaction fact identity key", factIdentityKey, maxProviderFactIdentityKeyBytes); err != nil {
		return ProviderTransactionFact{}, err
	}
	if factVersion <= 0 {
		return ProviderTransactionFact{}, fmt.Errorf("provider transaction fact version must be positive")
	}
	if err := r.requireDB(); err != nil {
		return ProviderTransactionFact{}, err
	}

	fact, err := scanProviderTransactionFact(r.db.QueryRowContext(ctx, selectProviderTransactionFactByIdentitySQL, channel, factIdentityKey, factVersion))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProviderTransactionFact{}, fmt.Errorf("provider transaction fact %q version %d does not exist", factIdentityKey, factVersion)
		}
		return ProviderTransactionFact{}, fmt.Errorf("read provider transaction fact: %w", err)
	}
	return fact, nil
}

func verifyProviderEventForFact(ctx context.Context, tx *sql.Tx, input ProviderTransactionFactInput) error {
	var eventChannel string
	var eventDigest string
	if err := tx.QueryRowContext(ctx, selectProviderEventForFactSQL, input.SourceProviderEventID).Scan(&eventChannel, &eventDigest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("source provider event %d does not exist", input.SourceProviderEventID)
		}
		return fmt.Errorf("read source provider event provenance: %w", err)
	}
	if Channel(eventChannel) != input.Channel {
		return fmt.Errorf("source provider event channel %q does not match fact channel %q", eventChannel, input.Channel)
	}
	if eventDigest != input.SourcePayloadDigest {
		return fmt.Errorf("source provider event payload digest does not match provider transaction fact")
	}
	return nil
}

func (input ProviderTransactionFactInput) validate() (string, error) {
	if !input.Channel.Valid() {
		return "", fmt.Errorf("unsupported provider transaction fact channel %q", input.Channel)
	}
	if err := validateRequiredString("provider account key", input.ProviderAccountKey, maxProviderFactAccountKeyBytes); err != nil {
		return "", err
	}
	if err := validateRequiredString("provider transaction fact identity key", input.FactIdentityKey, maxProviderFactIdentityKeyBytes); err != nil {
		return "", err
	}
	if input.FactVersion <= 0 {
		return "", fmt.Errorf("provider transaction fact version must be positive")
	}
	if input.SourceProviderEventID <= 0 {
		return "", fmt.Errorf("provider transaction fact source provider event ID must be positive")
	}
	if !isLowerSHA256Hex(input.SourcePayloadDigest) {
		return "", fmt.Errorf("provider transaction fact source payload digest must be lowercase SHA-256 hex")
	}
	if input.ProviderTransactionID == "" && input.ProviderGroupID == "" {
		return "", fmt.Errorf("provider transaction fact requires a provider transaction ID or group ID")
	}
	if err := validateOptionalProviderFactString("provider transaction ID", input.ProviderTransactionID, maxProviderFactReferenceBytes); err != nil {
		return "", err
	}
	if err := validateOptionalProviderFactString("provider group ID", input.ProviderGroupID, maxProviderFactReferenceBytes); err != nil {
		return "", err
	}
	if !validProviderFactValueScope(input.ValueScope) {
		return "", fmt.Errorf("unsupported provider transaction fact value scope %q", input.ValueScope)
	}
	if !validProviderFactAllocationState(input.AllocationState) {
		return "", fmt.Errorf("unsupported provider transaction fact allocation state %q", input.AllocationState)
	}
	if input.AllocationState == ProviderFactAllocationStateProvenDerivable {
		if err := validateRequiredString("provider fact derivation contract version", input.DerivationContractVersion, 64); err != nil {
			return "", err
		}
	} else if strings.TrimSpace(input.DerivationContractVersion) != "" {
		return "", fmt.Errorf("provider transaction fact derivation contract version is only valid for PROVEN_DERIVABLE facts")
	}
	if err := validateProviderFactDecimal("provider amount", input.ProviderAmount, false); err != nil {
		return "", err
	}
	if input.ProviderAmount != nil {
		if err := validateRequiredString("provider transaction fact currency", input.ProviderCurrency, maxProviderFactCurrencyBytes); err != nil {
			return "", err
		}
	} else if strings.TrimSpace(input.ProviderCurrency) != "" {
		return "", fmt.Errorf("provider transaction fact currency requires a provider amount")
	}
	if err := validateProviderFactDecimal("provider reported USD", input.ProviderReportedUSD, false); err != nil {
		return "", err
	}
	if err := validateConversionFact(input); err != nil {
		return "", err
	}
	return normalizedProviderFactExtras(input.ProviderExtrasJSON)
}

func validProviderFactValueScope(scope ProviderValueScope) bool {
	switch scope {
	case ProviderValueScopeTransactionTotal, ProviderValueScopeDirectItem, ProviderValueScopeConversionGroup:
		return true
	default:
		return false
	}
}

func validProviderFactAllocationState(state ProviderFactAllocationState) bool {
	switch state {
	case ProviderFactAllocationStateNotApplicable, ProviderFactAllocationStateUnproven, ProviderFactAllocationStateProvenDerivable:
		return true
	default:
		return false
	}
}

func validateConversionFact(input ProviderTransactionFactInput) error {
	if err := validateOptionalProviderFactString("conversion from currency", input.ConversionFromCurrency, maxProviderFactCurrencyBytes); err != nil {
		return err
	}
	if err := validateOptionalProviderFactString("conversion to currency", input.ConversionToCurrency, maxProviderFactCurrencyBytes); err != nil {
		return err
	}
	if err := validateProviderFactDecimal("conversion rate", input.ConversionRate, true); err != nil {
		return err
	}
	if err := validateProviderFactDecimal("conversion buy amount", input.ConversionBuyAmount, false); err != nil {
		return err
	}
	if err := validateProviderFactDecimal("conversion sell amount", input.ConversionSellAmount, false); err != nil {
		return err
	}
	if input.ConversionRate != nil || input.ConversionBuyAmount != nil || input.ConversionSellAmount != nil {
		if err := validateRequiredString("conversion from currency", input.ConversionFromCurrency, maxProviderFactCurrencyBytes); err != nil {
			return err
		}
		if err := validateRequiredString("conversion to currency", input.ConversionToCurrency, maxProviderFactCurrencyBytes); err != nil {
			return err
		}
	}
	return nil
}

func validateProviderFactDecimal(label string, value *decimal.Decimal, mustBePositive bool) error {
	if value == nil {
		return nil
	}
	if value.IsNegative() || (mustBePositive && !value.IsPositive()) {
		if mustBePositive {
			return fmt.Errorf("provider transaction fact %s must be positive", label)
		}
		return fmt.Errorf("provider transaction fact %s must be non-negative", label)
	}
	if value.Exponent() < -providerFactNumericScale {
		return fmt.Errorf("provider transaction fact %s exceeds NUMERIC(65,18) fractional precision", label)
	}
	integerDigits := len(strings.TrimLeft(value.Truncate(0).Abs().String(), "0"))
	if integerDigits > providerFactIntegerDigits {
		return fmt.Errorf("provider transaction fact %s exceeds NUMERIC(65,18) integer precision", label)
	}
	return nil
}

func validateOptionalProviderFactString(label, value string, maxBytes int) error {
	if value == "" {
		return nil
	}
	if !utf8.ValidString(value) || len(value) > maxBytes || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must be valid non-blank UTF-8 within %d bytes", label, maxBytes)
	}
	return nil
}

func normalizedProviderFactExtras(value []byte) (string, error) {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" {
		return "{}", nil
	}
	if len(trimmed) > maxProviderFactExtrasBytes || !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("provider transaction fact extras must be valid bounded JSON")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(trimmed))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return "", fmt.Errorf("provider transaction fact extras must be a JSON object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("canonicalize provider transaction fact extras: %w", err)
	}
	return string(canonical), nil
}

// immutableProviderTransactionFactConflict compares every persisted business
// field that participates in an idempotent fact. The database-generated ID and
// timestamps are deliberately excluded: they describe storage, not provider
// fact identity. It returns the first conflicting field for an actionable
// quarantine/error message.
func immutableProviderTransactionFactConflict(existing ProviderTransactionFact, input ProviderTransactionFactInput, canonicalInputExtras string) (string, error) {
	checks := []struct {
		field string
		equal bool
	}{
		{"channel", existing.Channel == input.Channel},
		{"provider_account_key", existing.ProviderAccountKey == input.ProviderAccountKey},
		{"provider_transaction_id", existing.ProviderTransactionID == input.ProviderTransactionID},
		{"provider_group_id", existing.ProviderGroupID == input.ProviderGroupID},
		{"fact_identity_key", existing.FactIdentityKey == input.FactIdentityKey},
		{"fact_version", existing.FactVersion == input.FactVersion},
		{"source_provider_event_id", existing.SourceEventID == input.SourceProviderEventID},
		{"source_payload_digest", existing.SourcePayloadDigest == input.SourcePayloadDigest},
		{"provider_occurred_at", equalProviderFactTime(existing.ProviderOccurredAt, input.ProviderOccurredAt)},
		{"provider_amount", equalProviderFactDecimal(existing.ProviderAmount, input.ProviderAmount)},
		{"provider_currency", existing.ProviderCurrency == input.ProviderCurrency},
		{"provider_reported_usd_value", equalProviderFactDecimal(existing.ProviderReportedUSD, input.ProviderReportedUSD)},
		{"conversion_from_currency", existing.ConversionFromCurrency == input.ConversionFromCurrency},
		{"conversion_to_currency", existing.ConversionToCurrency == input.ConversionToCurrency},
		{"conversion_rate", equalProviderFactDecimal(existing.ConversionRate, input.ConversionRate)},
		{"conversion_buy_amount", equalProviderFactDecimal(existing.ConversionBuyAmount, input.ConversionBuyAmount)},
		{"conversion_sell_amount", equalProviderFactDecimal(existing.ConversionSellAmount, input.ConversionSellAmount)},
		{"value_scope", existing.ValueScope == input.ValueScope},
		{"allocation_state", existing.AllocationState == input.AllocationState},
		{"derivation_contract_version", existing.DerivationContractVersion == input.DerivationContractVersion},
	}
	for _, check := range checks {
		if !check.equal {
			return check.field, nil
		}
	}
	canonicalExistingExtras, err := normalizedProviderFactExtras(existing.ProviderExtrasJSON)
	if err != nil {
		return "", fmt.Errorf("canonicalize stored provider transaction fact extras: %w", err)
	}
	if canonicalExistingExtras != canonicalInputExtras {
		return "provider_extras", nil
	}
	return "", nil
}

func equalProviderFactTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func equalProviderFactDecimal(left, right *decimal.Decimal) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func nullableProviderFactDecimal(value *decimal.Decimal) any {
	if value == nil {
		return nil
	}
	return value.String()
}

type providerTransactionFactScanner interface {
	Scan(dest ...any) error
}

func scanProviderTransactionFact(row providerTransactionFactScanner) (ProviderTransactionFact, error) {
	var fact ProviderTransactionFact
	var channel string
	var providerAccountKey sql.NullString
	var providerTransactionID sql.NullString
	var providerGroupID sql.NullString
	var providerOccurredAt sql.NullTime
	var providerAmountText sql.NullString
	var providerCurrency sql.NullString
	var providerReportedUSDText sql.NullString
	var conversionFromCurrency sql.NullString
	var conversionToCurrency sql.NullString
	var conversionRateText sql.NullString
	var conversionBuyAmountText sql.NullString
	var conversionSellAmountText sql.NullString
	var derivationContractVersion sql.NullString
	var providerExtras string
	if err := row.Scan(
		&fact.ID,
		&channel,
		&providerAccountKey,
		&providerTransactionID,
		&providerGroupID,
		&fact.FactIdentityKey,
		&fact.FactVersion,
		&fact.SourceEventID,
		&fact.SourcePayloadDigest,
		&providerOccurredAt,
		&providerAmountText,
		&providerCurrency,
		&providerReportedUSDText,
		&conversionFromCurrency,
		&conversionToCurrency,
		&conversionRateText,
		&conversionBuyAmountText,
		&conversionSellAmountText,
		&fact.ValueScope,
		&fact.AllocationState,
		&derivationContractVersion,
		&providerExtras,
		&fact.CreatedAt,
		&fact.UpdatedAt,
	); err != nil {
		return ProviderTransactionFact{}, err
	}
	if !Channel(channel).Valid() {
		return ProviderTransactionFact{}, fmt.Errorf("stored provider transaction fact has unsupported channel %q", channel)
	}
	fact.Channel = Channel(channel)
	fact.ProviderAccountKey = providerAccountKey.String
	fact.ProviderTransactionID = providerTransactionID.String
	fact.ProviderGroupID = providerGroupID.String
	fact.ProviderOccurredAt = nullTimePointer(providerOccurredAt)
	fact.ProviderCurrency = providerCurrency.String
	fact.ConversionFromCurrency = conversionFromCurrency.String
	fact.ConversionToCurrency = conversionToCurrency.String
	fact.DerivationContractVersion = derivationContractVersion.String
	fact.ProviderExtrasJSON = []byte(providerExtras)

	var err error
	if fact.ProviderAmount, err = providerFactDecimalPointer("provider amount", providerAmountText); err != nil {
		return ProviderTransactionFact{}, err
	}
	if fact.ProviderReportedUSD, err = providerFactDecimalPointer("provider reported USD", providerReportedUSDText); err != nil {
		return ProviderTransactionFact{}, err
	}
	if fact.ConversionRate, err = providerFactDecimalPointer("conversion rate", conversionRateText); err != nil {
		return ProviderTransactionFact{}, err
	}
	if fact.ConversionBuyAmount, err = providerFactDecimalPointer("conversion buy amount", conversionBuyAmountText); err != nil {
		return ProviderTransactionFact{}, err
	}
	if fact.ConversionSellAmount, err = providerFactDecimalPointer("conversion sell amount", conversionSellAmountText); err != nil {
		return ProviderTransactionFact{}, err
	}
	return fact, nil
}

func providerFactDecimalPointer(label string, text sql.NullString) (*decimal.Decimal, error) {
	if !text.Valid || text.String == "" {
		return nil, nil
	}
	value, err := decimal.NewFromString(text.String)
	if err != nil {
		return nil, fmt.Errorf("parse stored provider transaction fact %s: %w", label, err)
	}
	return &value, nil
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	stored := value.Time
	return &stored
}
