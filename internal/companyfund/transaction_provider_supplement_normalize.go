package companyfund

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

func normalizeTransactionProviderSupplement(
	display ProviderTransactionDisplayInput,
	risk ProviderAutomaticRiskInput,
) (normalizedTransactionProviderSupplement, error) {
	normalizedDisplay, err := normalizeProviderTransactionDisplay(display)
	if err != nil {
		return normalizedTransactionProviderSupplement{}, err
	}
	normalizedRisk, err := normalizeProviderAutomaticRisk(risk)
	if err != nil {
		return normalizedTransactionProviderSupplement{}, err
	}
	return normalizedTransactionProviderSupplement{Display: normalizedDisplay, Risk: normalizedRisk}, nil
}

func normalizeProviderTransactionDisplay(input ProviderTransactionDisplayInput) (normalizedProviderTransactionDisplay, error) {
	from, err := normalizeProviderTransactionPartyDisplay(input.From)
	if err != nil {
		return normalizedProviderTransactionDisplay{}, fmt.Errorf("normalize from transaction display: %w", err)
	}
	to, err := normalizeProviderTransactionPartyDisplay(input.To)
	if err != nil {
		return normalizedProviderTransactionDisplay{}, fmt.Errorf("normalize to transaction display: %w", err)
	}
	payerName, err := normalizeTransactionSupplementString("payer name", input.PayerName, maxTransactionDisplayNameBytes)
	if err != nil {
		return normalizedProviderTransactionDisplay{}, err
	}
	payeeName, err := normalizeTransactionSupplementString("payee name", input.PayeeName, maxTransactionDisplayNameBytes)
	if err != nil {
		return normalizedProviderTransactionDisplay{}, err
	}
	if err := validateTransactionSupplementDecimal("provider fee amount", input.Fee.Amount); err != nil {
		return normalizedProviderTransactionDisplay{}, err
	}
	feeCurrency, err := normalizeTransactionSupplementString("provider fee currency", input.Fee.Currency, 64)
	if err != nil {
		return normalizedProviderTransactionDisplay{}, err
	}
	feeDetailsJSON, err := normalizeTransactionSupplementJSONObject("provider fee details", input.Fee.DetailsJSON)
	if err != nil {
		return normalizedProviderTransactionDisplay{}, err
	}
	if input.BlockHeight != nil && *input.BlockHeight < 0 {
		return normalizedProviderTransactionDisplay{}, fmt.Errorf("provider block height must be non-negative")
	}
	blockHash, err := normalizeTransactionSupplementString("provider block hash", input.BlockHash, 256)
	if err != nil {
		return normalizedProviderTransactionDisplay{}, err
	}
	return normalizedProviderTransactionDisplay{
		From:           from,
		To:             to,
		PayerName:      payerName,
		PayeeName:      payeeName,
		FeeAmount:      copyTransactionSupplementDecimal(input.Fee.Amount),
		FeeCurrency:    feeCurrency,
		FeeDetailsJSON: feeDetailsJSON,
		BlockHeight:    copyTransactionSupplementInt64(input.BlockHeight),
		BlockHash:      blockHash,
	}, nil
}

func normalizeProviderTransactionPartyDisplay(input ProviderTransactionPartyDisplayInput) (normalizedProviderTransactionPartyDisplay, error) {
	address, err := normalizeTransactionSupplementString("transaction address or account", input.AddressOrAccount, maxTransactionAddressOrAccountBytes)
	if err != nil {
		return normalizedProviderTransactionPartyDisplay{}, err
	}
	entity, err := normalizeTransactionSupplementString("company entity snapshot", input.CompanyEntity, maxTransactionDisplayNameBytes)
	if err != nil {
		return normalizedProviderTransactionPartyDisplay{}, err
	}
	fundAccount, err := normalizeTransactionSupplementString("fund account snapshot", input.FundAccountName, maxTransactionDisplayNameBytes)
	if err != nil {
		return normalizedProviderTransactionPartyDisplay{}, err
	}
	subAccount, err := normalizeTransactionSupplementString("sub-account snapshot", input.SubAccountName, maxTransactionDisplayNameBytes)
	if err != nil {
		return normalizedProviderTransactionPartyDisplay{}, err
	}
	accountType, err := normalizeTransactionSupplementString("account type snapshot", input.AccountType, maxTransactionAccountTypeBytes)
	if err != nil {
		return normalizedProviderTransactionPartyDisplay{}, err
	}
	return normalizedProviderTransactionPartyDisplay{
		AddressOrAccount: address,
		CompanyEntity:    entity,
		FundAccountName:  fundAccount,
		SubAccountName:   subAccount,
		AccountType:      accountType,
	}, nil
}

func normalizeProviderAutomaticRisk(input ProviderAutomaticRiskInput) (normalizedProviderAutomaticRisk, error) {
	if (input.DustPolicyID == nil) != (input.DustThreshold == nil) {
		return normalizedProviderAutomaticRisk{}, fmt.Errorf("automatic dust policy ID and threshold must be supplied together")
	}
	if (input.DustPolicyID != nil || input.DustThreshold != nil) && input.IsDust == nil {
		return normalizedProviderAutomaticRisk{}, fmt.Errorf("automatic dust evidence requires an explicit dust result")
	}
	if input.DustPolicyID != nil && *input.DustPolicyID <= 0 {
		return normalizedProviderAutomaticRisk{}, fmt.Errorf("automatic dust policy ID must be positive")
	}
	if err := validateTransactionSupplementDecimal("automatic dust threshold", input.DustThreshold); err != nil {
		return normalizedProviderAutomaticRisk{}, err
	}
	if boolPointerValue(input.IsDust) && (input.DustPolicyID == nil || input.DustThreshold == nil) {
		return normalizedProviderAutomaticRisk{}, fmt.Errorf("automatic dust result requires a policy ID and threshold")
	}
	if input.AMLScreeningState != nil && !input.AMLScreeningState.valid() {
		return normalizedProviderAutomaticRisk{}, fmt.Errorf("unsupported automatic AML screening state %q", *input.AMLScreeningState)
	}
	if input.AMLRiskLevel != nil && !input.AMLRiskLevel.valid() {
		return normalizedProviderAutomaticRisk{}, fmt.Errorf("unsupported automatic AML risk level %q", *input.AMLRiskLevel)
	}
	riskFlagsJSON, err := normalizeAutomaticRiskFlags(input.RiskFlags)
	if err != nil {
		return normalizedProviderAutomaticRisk{}, err
	}
	return normalizedProviderAutomaticRisk{
		IsDust:                  copyTransactionSupplementBool(input.IsDust),
		AutoExcludedFromSummary: copyTransactionSupplementBool(input.AutoExcludedFromSummary),
		DustPolicyID:            copyTransactionSupplementInt64(input.DustPolicyID),
		DustThreshold:           copyTransactionSupplementDecimal(input.DustThreshold),
		IsSourcePhishing:        copyTransactionSupplementBool(input.IsSourcePhishing),
		IsDestinationPhishing:   copyTransactionSupplementBool(input.IsDestinationPhishing),
		IsUnrecognizedAsset:     copyTransactionSupplementBool(input.IsUnrecognizedAsset),
		AMLLock:                 copyTransactionSupplementBool(input.AMLLock),
		AMLScreeningState:       copyAMLScreeningState(input.AMLScreeningState),
		AMLRiskLevel:            copyAMLRiskLevel(input.AMLRiskLevel),
		RiskFlagsJSON:           riskFlagsJSON,
	}, nil
}

func normalizeTransactionSupplementString(label string, value *string, maxBytes int) (*string, error) {
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

func normalizeTransactionSupplementJSONObject(label string, value json.RawMessage) (*string, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if len(trimmed) > maxTransactionFeeDetailsBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maxTransactionFeeDetailsBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("%s must contain exactly one JSON object", label)
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("canonicalize %s: %w", label, err)
	}
	result := string(canonical)
	return &result, nil
}

func normalizeAutomaticRiskFlags(value *[]RiskFlag) (*string, error) {
	if value == nil {
		return nil, nil
	}
	seen := make(map[RiskFlag]struct{}, len(*value))
	flags := make([]string, 0, len(*value))
	for _, flag := range *value {
		if !flag.valid() {
			return nil, fmt.Errorf("unsupported automatic risk flag %q", flag)
		}
		if _, exists := seen[flag]; exists {
			continue
		}
		seen[flag] = struct{}{}
		flags = append(flags, string(flag))
	}
	sort.Strings(flags)
	canonical, err := json.Marshal(flags)
	if err != nil {
		return nil, fmt.Errorf("canonicalize automatic risk flags: %w", err)
	}
	result := string(canonical)
	return &result, nil
}
