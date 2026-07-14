package companyfund

import (
	"encoding/json"

	"github.com/shopspring/decimal"
)

const (
	maxTransactionAddressOrAccountBytes = 512
	maxTransactionDisplayNameBytes      = 256
	maxTransactionAccountTypeBytes      = 64
	maxTransactionFeeDetailsBytes       = 16 << 10
)

// AMLScreeningState is the provider/system screening workflow state. It is
// distinct from manual risk review and can be updated by webhook or
// reconciliation facts only through TransactionUpsertInput.AutomaticRisk.
type AMLScreeningState string

const (
	AMLScreeningStateNotScreened    AMLScreeningState = "NOT_SCREENED"
	AMLScreeningStatePending        AMLScreeningState = "PENDING"
	AMLScreeningStateScreened       AMLScreeningState = "SCREENED"
	AMLScreeningStateReviewRequired AMLScreeningState = "REVIEW_REQUIRED"
	AMLScreeningStateCleared        AMLScreeningState = "CLEARED"
	AMLScreeningStateBlocked        AMLScreeningState = "BLOCKED"
	AMLScreeningStateError          AMLScreeningState = "ERROR"
)

// ProviderTransactionPartyDisplayInput contains a provider-observed endpoint
// plus the resolved company-account display snapshot for one side. All fields
// are pointers so a partial follow-up cannot erase a prior fact.
type ProviderTransactionPartyDisplayInput struct {
	AddressOrAccount *string
	CompanyEntity    *string
	FundAccountName  *string
	SubAccountName   *string
	AccountType      *string
}

type ProviderTransactionFeeInput struct {
	Amount      *decimal.Decimal
	Currency    *string
	DetailsJSON json.RawMessage
}

// ProviderTransactionDisplayInput is the explicit normalizer-to-repository
// contract for financial display data. It excludes manual finance fields.
type ProviderTransactionDisplayInput struct {
	From        ProviderTransactionPartyDisplayInput
	To          ProviderTransactionPartyDisplayInput
	PayerName   *string
	PayeeName   *string
	Fee         ProviderTransactionFeeInput
	BlockHeight *int64
	BlockHash   *string
}

// ProviderAutomaticRiskInput holds automatic provider/system results. It does
// not contain a manual review status, override, or financial classification.
type ProviderAutomaticRiskInput struct {
	IsDust *bool
	// AutoExcludedFromSummary snapshots the automatic dust/risk decision at
	// ingestion time. It is provider/system-owned and is deliberately separate
	// from the finance-owned manual summary override.
	AutoExcludedFromSummary *bool
	DustPolicyID            *int64
	DustThreshold           *decimal.Decimal
	IsSourcePhishing        *bool
	IsDestinationPhishing   *bool
	IsUnrecognizedAsset     *bool
	AMLLock                 *bool
	AMLScreeningState       *AMLScreeningState
	AMLRiskLevel            *AMLRiskLevel
	// RiskFlags is a pointer so nil means absent, while a pointer to an empty
	// slice is an explicit provider/system clear under newer metadata.
	RiskFlags *[]RiskFlag
}

type normalizedTransactionProviderSupplement struct {
	Display normalizedProviderTransactionDisplay
	Risk    normalizedProviderAutomaticRisk
}

type normalizedProviderTransactionDisplay struct {
	From           normalizedProviderTransactionPartyDisplay
	To             normalizedProviderTransactionPartyDisplay
	PayerName      *string
	PayeeName      *string
	FeeAmount      *decimal.Decimal
	FeeCurrency    *string
	FeeDetailsJSON *string
	BlockHeight    *int64
	BlockHash      *string
}

type normalizedProviderTransactionPartyDisplay struct {
	AddressOrAccount *string
	CompanyEntity    *string
	FundAccountName  *string
	SubAccountName   *string
	AccountType      *string
}

type normalizedProviderAutomaticRisk struct {
	IsDust                  *bool
	AutoExcludedFromSummary *bool
	DustPolicyID            *int64
	DustThreshold           *decimal.Decimal
	IsSourcePhishing        *bool
	IsDestinationPhishing   *bool
	IsUnrecognizedAsset     *bool
	AMLLock                 *bool
	AMLScreeningState       *AMLScreeningState
	AMLRiskLevel            *AMLRiskLevel
	RiskFlagsJSON           *string
}

func (value normalizedTransactionProviderSupplement) hasValues() bool {
	return value.Display.hasValues() || value.Risk.hasValues()
}

func (value normalizedProviderTransactionDisplay) hasValues() bool {
	return value.From.hasValues() || value.To.hasValues() || value.PayerName != nil || value.PayeeName != nil ||
		value.FeeAmount != nil || value.FeeCurrency != nil || value.FeeDetailsJSON != nil ||
		value.BlockHeight != nil || value.BlockHash != nil
}

func (value normalizedProviderTransactionPartyDisplay) hasValues() bool {
	return value.AddressOrAccount != nil || value.CompanyEntity != nil || value.FundAccountName != nil ||
		value.SubAccountName != nil || value.AccountType != nil
}

func (value normalizedProviderAutomaticRisk) hasValues() bool {
	return value.IsDust != nil || value.AutoExcludedFromSummary != nil || value.DustPolicyID != nil || value.DustThreshold != nil ||
		value.IsSourcePhishing != nil || value.IsDestinationPhishing != nil || value.IsUnrecognizedAsset != nil ||
		value.AMLLock != nil || value.AMLScreeningState != nil || value.AMLRiskLevel != nil || value.RiskFlagsJSON != nil
}
