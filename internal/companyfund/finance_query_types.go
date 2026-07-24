package companyfund

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultFinanceDetailLimit       = 100
	maxFinanceDetailLimit           = 1000
	maxFinanceApplicantBytes        = 256
	maxFinanceCounterpartyNameBytes = 256
	maxFinanceDescriptionBytes      = 16 << 10
	maxFinanceUpdatedByBytes        = 256
)

// FinanceTransactionFilter is the single shared inclusion contract for both
// dashboard aggregates and drilldown rows. By default it excludes a movement
// when the finance-owned override is false or, absent an override, the durable
// automatic exclusion snapshot is true.
type FinanceTransactionFilter struct {
	DateFrom                 *time.Time          `json:"dateFrom,omitempty"`
	DateTo                   *time.Time          `json:"dateTo,omitempty"`
	Channels                 []TransactionSource `json:"channels,omitempty"`
	AccountIDs               []int64             `json:"accountIds,omitempty"`
	Directions               []Direction         `json:"directions,omitempty"`
	Currencies               []string            `json:"currencies,omitempty"`
	FinanceCategoryLevel1IDs []int64             `json:"financeCategoryLevel1Ids,omitempty"`
	FinanceCategoryLevel2IDs []int64             `json:"financeCategoryLevel2Ids,omitempty"`
	OperatingIncomeExpense   *bool               `json:"operatingIncomeExpense,omitempty"`
	IncludeSummaryExcluded   bool                `json:"includeSummaryExcluded"`
}

// CanonicalizeFinanceTransactionFilter validates and normalizes the shared
// dashboard/detail filter at an HTTP or service boundary. Repository methods
// invoke the same private canonical form defensively before issuing SQL.
func CanonicalizeFinanceTransactionFilter(filter FinanceTransactionFilter) (FinanceTransactionFilter, error) {
	return filter.canonical()
}

func (filter FinanceTransactionFilter) canonical() (FinanceTransactionFilter, error) {
	if filter.DateFrom != nil {
		value := filter.DateFrom.UTC()
		filter.DateFrom = &value
	}
	if filter.DateTo != nil {
		value := filter.DateTo.UTC()
		filter.DateTo = &value
	}
	if filter.DateFrom != nil && filter.DateTo != nil && !filter.DateTo.After(*filter.DateFrom) {
		return FinanceTransactionFilter{}, fmt.Errorf("finance filter date end must be after date start")
	}

	channels := make([]TransactionSource, 0, len(filter.Channels))
	seenChannels := make(map[TransactionSource]struct{}, len(filter.Channels))
	for _, channel := range filter.Channels {
		if !channel.Valid() {
			return FinanceTransactionFilter{}, fmt.Errorf("unsupported finance filter channel %q", channel)
		}
		if _, exists := seenChannels[channel]; !exists {
			seenChannels[channel] = struct{}{}
			channels = append(channels, channel)
		}
	}
	sort.Slice(channels, func(left, right int) bool { return channels[left] < channels[right] })
	filter.Channels = channels

	directions := make([]Direction, 0, len(filter.Directions))
	seenDirections := make(map[Direction]struct{}, len(filter.Directions))
	for _, direction := range filter.Directions {
		if !direction.Valid() {
			return FinanceTransactionFilter{}, fmt.Errorf("unsupported finance filter direction %q", direction)
		}
		if _, exists := seenDirections[direction]; !exists {
			seenDirections[direction] = struct{}{}
			directions = append(directions, direction)
		}
	}
	sort.Slice(directions, func(left, right int) bool { return directions[left] < directions[right] })
	filter.Directions = directions

	currencies := make([]string, 0, len(filter.Currencies))
	seenCurrencies := make(map[string]struct{}, len(filter.Currencies))
	for _, currency := range filter.Currencies {
		// Validate the provider/user supplied bytes before case conversion:
		// strings.ToUpper can replace malformed UTF-8, which would otherwise
		// turn invalid input into a different valid-looking filter value.
		if !utf8.ValidString(currency) || len(currency) > 64 {
			return FinanceTransactionFilter{}, fmt.Errorf("finance filter currency must be valid UTF-8 within 64 bytes")
		}
		normalized := strings.ToUpper(strings.TrimSpace(currency))
		if normalized == "" || !utf8.ValidString(normalized) || len(normalized) > 64 {
			return FinanceTransactionFilter{}, fmt.Errorf("finance filter currency must be valid UTF-8 within 64 bytes")
		}
		if _, exists := seenCurrencies[normalized]; !exists {
			seenCurrencies[normalized] = struct{}{}
			currencies = append(currencies, normalized)
		}
	}
	sort.Strings(currencies)
	filter.Currencies = currencies

	var err error
	if filter.AccountIDs, err = canonicalPositiveInt64Set("finance filter account ID", filter.AccountIDs); err != nil {
		return FinanceTransactionFilter{}, err
	}
	if filter.FinanceCategoryLevel1IDs, err = canonicalPositiveInt64Set("finance filter level 1 category ID", filter.FinanceCategoryLevel1IDs); err != nil {
		return FinanceTransactionFilter{}, err
	}
	if filter.FinanceCategoryLevel2IDs, err = canonicalPositiveInt64Set("finance filter level 2 category ID", filter.FinanceCategoryLevel2IDs); err != nil {
		return FinanceTransactionFilter{}, err
	}
	return filter, nil
}

func canonicalPositiveInt64Set(label string, values []int64) ([]int64, error) {
	set := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			return nil, fmt.Errorf("%s must be positive", label)
		}
		set[value] = struct{}{}
	}
	canonical := make([]int64, 0, len(set))
	for value := range set {
		canonical = append(canonical, value)
	}
	sort.Slice(canonical, func(left, right int) bool { return canonical[left] < canonical[right] })
	return canonical, nil
}

type FinanceDashboardSummary struct {
	Filter     FinanceTransactionFilter    `json:"filter"`
	Aggregates []FinanceDashboardAggregate `json:"aggregates"`
}

// FinanceDashboardAggregate is one exact currency/direction total. Drilldown
// contains the same canonical filter with only this aggregate's dimensions
// added, so a dashboard number and its detail rows share one inclusion rule.
type FinanceDashboardAggregate struct {
	Direction        Direction                `json:"direction"`
	Currency         string                   `json:"currency"`
	TransactionCount int64                    `json:"transactionCount"`
	Amount           string                   `json:"amount"`
	USDValue         *string                  `json:"usdValue,omitempty"`
	UnpricedCount    int64                    `json:"unpricedCount"`
	Drilldown        FinanceTransactionFilter `json:"drilldown"`
}

type FinanceTransactionDetailRequest struct {
	Filter FinanceTransactionFilter `json:"filter"`
	Limit  int                      `json:"limit"`
	Offset int                      `json:"offset"`
}

// CanonicalizeFinanceTransactionDetailRequest validates pagination together
// with the same shared filter used by dashboard aggregates.
func CanonicalizeFinanceTransactionDetailRequest(request FinanceTransactionDetailRequest) (FinanceTransactionDetailRequest, error) {
	return request.canonical()
}

func (request FinanceTransactionDetailRequest) canonical() (FinanceTransactionDetailRequest, error) {
	filter, err := request.Filter.canonical()
	if err != nil {
		return FinanceTransactionDetailRequest{}, err
	}
	if request.Limit == 0 {
		request.Limit = defaultFinanceDetailLimit
	}
	if request.Limit < 1 || request.Limit > maxFinanceDetailLimit {
		return FinanceTransactionDetailRequest{}, fmt.Errorf("finance detail limit must be between 1 and %d", maxFinanceDetailLimit)
	}
	if request.Offset < 0 {
		return FinanceTransactionDetailRequest{}, fmt.Errorf("finance detail offset must be non-negative")
	}
	request.Filter = filter
	return request, nil
}

// FinanceTransactionDetail returns only reportable financial fields. Decimal
// values are strings so no float conversion can alter the stored amount, USD
// value, or fee value.
type FinanceTransactionDetail struct {
	ID              int64             `json:"id"`
	Date            time.Time         `json:"date"`
	Channel         TransactionSource `json:"channel"`
	CompanyEntity   string            `json:"companyEntity"`
	FundAccountName string            `json:"fundAccountName"`
	SubAccountName  string            `json:"subAccountName"`
	AccountType     string            `json:"accountType"`
	Direction       Direction         `json:"direction"`
	// TransferMode distinguishes provider-normal single transfers from batches.
	// DirectionInternalTransfer remains the authoritative internal-transfer
	// marker, so the finance API does not invent a redundant stored type.
	TransferMode              TransferMode `json:"transferMode"`
	MovementKind              MovementKind `json:"movementKind"`
	IsOperatingIncomeExpense  *bool        `json:"isOperatingIncomeExpense,omitempty"`
	FinanceCategoryLevel1ID   *int64       `json:"financeCategoryLevel1Id,omitempty"`
	FinanceCategoryLevel1Code string       `json:"financeCategoryLevel1Code"`
	FinanceCategoryLevel1Name string       `json:"financeCategoryLevel1Name"`
	FinanceCategoryLevel2ID   *int64       `json:"financeCategoryLevel2Id,omitempty"`
	FinanceCategoryLevel2Code string       `json:"financeCategoryLevel2Code"`
	FinanceCategoryLevel2Name string       `json:"financeCategoryLevel2Name"`
	Currency                  string       `json:"currency"`
	Amount                    string       `json:"amount"`
	USDValue                  *string      `json:"usdValue,omitempty"`
	FeeAmount                 *string      `json:"feeAmount,omitempty"`
	FeeCurrency               string       `json:"feeCurrency"`
	Payer                     string       `json:"payer"`
	Payee                     string       `json:"payee"`
	CounterpartyNameOverride  *string      `json:"counterpartyNameOverride,omitempty"`
	EffectiveCounterpartyName string       `json:"effectiveCounterpartyName"`
	FromAddressOrAccount      string       `json:"fromAddressOrAccount"`
	ToAddressOrAccount        string       `json:"toAddressOrAccount"`
	Applicant                 string       `json:"applicant"`
	BusinessDescription       string       `json:"businessDescription"`
	TxHash                    string       `json:"txHash"`
	ProviderTransactionID     string       `json:"providerTransactionId"`
	SummaryIncluded           bool         `json:"summaryIncluded"`
	IsDust                    bool         `json:"isDust"`
	AutoExcludedFromSummary   bool         `json:"autoExcludedFromSummary"`
	SummaryInclusionOverride  *bool        `json:"summaryInclusionOverride,omitempty"`
}

// FinanceClassificationUpdate replaces finance-owned fields for one
// transaction. CounterpartyNameOverrideSet distinguishes omission (preserve)
// from an explicit null or blank value (clear), so older callers cannot erase
// a newer manual override. Provider ingestion has no path to these columns.
type FinanceClassificationUpdate struct {
	TransactionID               int64   `json:"transactionId"`
	FinanceCategoryLevel1ID     *int64  `json:"financeCategoryLevel1Id,omitempty"`
	FinanceCategoryLevel2ID     *int64  `json:"financeCategoryLevel2Id,omitempty"`
	IsOperatingIncomeExpense    *bool   `json:"isOperatingIncomeExpense,omitempty"`
	Applicant                   *string `json:"applicant,omitempty"`
	BusinessDescription         *string `json:"businessDescription,omitempty"`
	SummaryInclusionOverride    *bool   `json:"summaryInclusionOverride,omitempty"`
	CounterpartyNameOverride    *string `json:"counterpartyNameOverride,omitempty"`
	CounterpartyNameOverrideSet bool    `json:"-"`
	UpdatedBy                   string  `json:"updatedBy"`
}

// CanonicalizeFinanceClassificationUpdate validates finance-owned fields
// before a management transport invokes the transactional repository update.
func CanonicalizeFinanceClassificationUpdate(input FinanceClassificationUpdate) (FinanceClassificationUpdate, error) {
	return input.canonical()
}

type FinanceClassificationResult struct {
	TransactionID            int64     `json:"transactionId"`
	FinanceCategoryLevel1ID  *int64    `json:"financeCategoryLevel1Id,omitempty"`
	FinanceCategoryLevel2ID  *int64    `json:"financeCategoryLevel2Id,omitempty"`
	IsOperatingIncomeExpense *bool     `json:"isOperatingIncomeExpense,omitempty"`
	Applicant                string    `json:"applicant"`
	BusinessDescription      string    `json:"businessDescription"`
	SummaryInclusionOverride *bool     `json:"summaryInclusionOverride,omitempty"`
	CounterpartyNameOverride *string   `json:"counterpartyNameOverride,omitempty"`
	ClassificationStatus     string    `json:"classificationStatus"`
	UpdatedBy                string    `json:"updatedBy"`
	UpdatedAt                time.Time `json:"updatedAt"`
}

// CompanyFundFinanceStore is deliberately an internal service boundary. A
// future privileged management route may authorize it; ordinary JWT routes
// must not expose it directly.
type CompanyFundFinanceStore interface {
	GetFinanceDashboard(ctx context.Context, filter FinanceTransactionFilter) (FinanceDashboardSummary, error)
	ListFinanceTransactionDetails(ctx context.Context, request FinanceTransactionDetailRequest) ([]FinanceTransactionDetail, error)
	UpdateFinanceTransactionClassification(ctx context.Context, input FinanceClassificationUpdate) (FinanceClassificationResult, error)
}

func (input FinanceClassificationUpdate) canonical() (FinanceClassificationUpdate, error) {
	if input.TransactionID <= 0 {
		return FinanceClassificationUpdate{}, fmt.Errorf("finance classification transaction ID must be positive")
	}
	for label, value := range map[string]*int64{
		"finance classification level 1 category ID": input.FinanceCategoryLevel1ID,
		"finance classification level 2 category ID": input.FinanceCategoryLevel2ID,
	} {
		if value != nil && *value <= 0 {
			return FinanceClassificationUpdate{}, fmt.Errorf("%s must be positive", label)
		}
	}
	updatedBy := strings.TrimSpace(input.UpdatedBy)
	if err := validateRequiredString("finance classification updated by", updatedBy, maxFinanceUpdatedByBytes); err != nil {
		return FinanceClassificationUpdate{}, err
	}
	applicant, err := canonicalFinanceManualText("finance applicant", input.Applicant, maxFinanceApplicantBytes)
	if err != nil {
		return FinanceClassificationUpdate{}, err
	}
	description, err := canonicalFinanceManualText("finance business description", input.BusinessDescription, maxFinanceDescriptionBytes)
	if err != nil {
		return FinanceClassificationUpdate{}, err
	}
	counterpartyNameOverrideSet := input.CounterpartyNameOverrideSet || input.CounterpartyNameOverride != nil
	counterpartyName, err := canonicalFinanceManualText("finance counterparty name override", input.CounterpartyNameOverride, maxFinanceCounterpartyNameBytes)
	if err != nil {
		return FinanceClassificationUpdate{}, err
	}
	input.Applicant = applicant
	input.BusinessDescription = description
	input.CounterpartyNameOverride = counterpartyName
	input.CounterpartyNameOverrideSet = counterpartyNameOverrideSet
	input.UpdatedBy = updatedBy
	return input, nil
}

func canonicalFinanceManualText(label string, value *string, maxBytes int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil, nil
	}
	if !utf8.ValidString(normalized) || len(normalized) > maxBytes {
		return nil, fmt.Errorf("%s must be valid UTF-8 within %d bytes", label, maxBytes)
	}
	return &normalized, nil
}
