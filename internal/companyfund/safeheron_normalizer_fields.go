package companyfund

import (
	"fmt"
	"strings"
	"time"

	"monera-digital/internal/safeheron"

	"github.com/shopspring/decimal"
)

func normalizeSafeheronAssetMapping(snapshotCoinKey string, mapping SafeheronAssetMapping, label string) (AssetIdentity, error) {
	coinKey := strings.TrimSpace(snapshotCoinKey)
	if coinKey == "" {
		return AssetIdentity{}, fmt.Errorf("Safeheron %s coin key is required", label)
	}
	if strings.TrimSpace(mapping.CoinKey) != coinKey {
		return AssetIdentity{}, fmt.Errorf("Safeheron %s asset mapping coin key %q does not match snapshot coin key %q", label, mapping.CoinKey, coinKey)
	}
	asset := normalizeAssetIdentity(mapping.Asset)
	asset.ContractAddress = normalizeAssetContract(asset.ContractAddress)
	if asset.Currency == "" || asset.ChainCode == "" || (asset.ProviderAssetKey == "" && asset.ContractAddress == "") {
		return AssetIdentity{}, fmt.Errorf("Safeheron %s asset mapping requires explicit currency, chain, and provider asset key or contract", label)
	}
	return asset, nil
}

func parseSafeheronAmount(label, text string, required bool) (decimal.Decimal, error) {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		if required {
			return decimal.Zero, fmt.Errorf("%s is required", label)
		}
		return decimal.Zero, nil
	}
	amount, err := decimal.NewFromString(normalized)
	if err != nil || amount.IsNegative() {
		return decimal.Zero, fmt.Errorf("%s must be a non-negative exact decimal", label)
	}
	if err := validateTransactionSupplementDecimal(label, &amount); err != nil {
		return decimal.Zero, err
	}
	return amount, nil
}

func safeheronUnixMilliseconds(value int64, label string) (*time.Time, error) {
	if value == 0 {
		return nil, nil
	}
	if value < 0 {
		return nil, fmt.Errorf("Safeheron %s cannot be negative", label)
	}
	converted := time.UnixMilli(value).UTC()
	return &converted, nil
}

func safeheronTruePointer(value bool) *bool {
	if !value {
		return nil
	}
	result := true
	return &result
}

func safeheronOptionalString(value string) *string {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func safeheronCopyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func safeheronAccountIDs(from, to *CompanyFundAccount) (*int64, *int64) {
	var fromID, toID *int64
	if from != nil {
		value := from.ID
		fromID = &value
	}
	if to != nil {
		value := to.ID
		toID = &value
	}
	return fromID, toID
}

func safeheronRiskPolicy(registry *AccountRegistrySnapshot, direction Direction, fromID, toID *int64, asset AssetIdentity) (AccountAssetPolicy, bool) {
	subjectID, err := PolicySubjectAccountID(direction, fromID, toID)
	if err != nil || registry == nil {
		return AccountAssetPolicy{}, false
	}
	return registry.LookupAssetPolicy(subjectID, asset)
}

func safeheronAccountSnapshot(account *CompanyFundAccount) *AccountSnapshot {
	if account == nil {
		return nil
	}
	return &AccountSnapshot{
		AccountID: account.ID, CompanyEntity: account.CompanyEntity, FundAccountName: account.FundAccountName,
		SubAccountName: account.SubAccountName, AccountType: account.AccountType,
	}
}

func safeheronProviderAccountKey(explicit string, snapshot safeheron.TransactionSnapshot, from, to *CompanyFundAccount) string {
	if normalized := strings.TrimSpace(explicit); normalized != "" {
		return normalized
	}
	for _, value := range []string{
		snapshot.SourceAccountKey, snapshot.DestinationAccountKey,
		safeheronAccountProviderKey(from), safeheronAccountProviderKey(to),
	} {
		if normalized := strings.TrimSpace(value); normalized != "" {
			return normalized
		}
	}
	return ""
}

func safeheronAccountProviderKey(account *CompanyFundAccount) string {
	if account == nil {
		return ""
	}
	return account.ProviderAccountKey
}

func safeheronProviderFields(metadata ProviderFactMetadata, base safeheronNormalizationBase, amount decimal.Decimal, asset AssetIdentity) ProviderOwnedFields {
	amountCopy := amount
	currencyCopy := asset.Currency
	assetCopy := asset
	statusCopy := base.status
	return ProviderOwnedFields{
		Metadata: metadata, Amount: &amountCopy, Currency: &currencyCopy, Asset: &assetCopy,
		TxHash: safeheronCopyString(base.txHash), Status: &statusCopy,
		OccurredAt: copyTime(base.occurredAt), CompletedAt: copyTime(base.completedAt),
	}
}

func safeheronCopyString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func safeheronProviderDisplay(snapshot safeheron.TransactionSnapshot, draft safeheronPrincipalDraft, fee *safeheronFeeDisplay) ProviderTransactionDisplayInput {
	display := ProviderTransactionDisplayInput{
		From:        safeheronPartyDisplay(snapshot.SourceAddress, draft.fromAccount),
		To:          safeheronPartyDisplay(draft.line.DestinationAddress, draft.toAccount),
		PayerName:   safeheronAccountName(draft.fromAccount),
		PayeeName:   safeheronAccountName(draft.toAccount),
		BlockHeight: safeheronBlockHeight(snapshot.BlockHeight),
	}
	if fee != nil {
		display.Fee = safeheronProviderFeeInput(*fee)
	}
	return display
}

func safeheronPartyDisplay(address string, account *CompanyFundAccount) ProviderTransactionPartyDisplayInput {
	result := ProviderTransactionPartyDisplayInput{AddressOrAccount: safeheronOptionalString(address)}
	if account == nil {
		return result
	}
	result.CompanyEntity = safeheronOptionalString(account.CompanyEntity)
	result.FundAccountName = safeheronOptionalString(account.FundAccountName)
	result.SubAccountName = safeheronOptionalString(account.SubAccountName)
	result.AccountType = safeheronOptionalString(account.AccountType)
	return result
}

func safeheronAccountName(account *CompanyFundAccount) *string {
	if account == nil {
		return nil
	}
	return safeheronOptionalString(account.AccountName)
}

func safeheronBlockHeight(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	copy := value
	return &copy
}
