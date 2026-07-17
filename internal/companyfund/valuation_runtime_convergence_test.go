package companyfund

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCompanyFundCurrentValuator_PermanentUnpricedSweepConvergesThroughRepositoryIdempotency(t *testing.T) {
	now := time.Date(2026, time.July, 16, 6, 0, 0, 0, time.UTC)
	candidate := permanentUnpricedValuationCandidate(31)
	candidate.CurrentValuationDependencyFingerprint = strings.Repeat("a", 64)
	store := newRepositoryIdempotentValuationStore(candidate)
	initialHistoryCount := store.historyCount()
	valuator := newTestCompanyFundCurrentValuatorWithConfig(
		t,
		now,
		store,
		newCurrentRateRefresherRegistryWithAccount(t, 1, nil),
		newTestCurrentRateCache(t, &now, time.Minute),
		CompanyFundCurrentValuatorConfig{DefaultMappings: CoinGeckoDefaultRateMappings{}},
	)

	first := valuator.Sweep(t.Context(), 10)
	second := valuator.Sweep(t.Context(), 10)

	if first.Err != nil || first.Applied != 1 || first.Converged != 1 {
		t.Fatalf("first Sweep() = %#v; want one changed dependency appended", first)
	}
	if second.Err != nil || second.Applied != 0 || second.Converged != 1 {
		t.Fatalf("second Sweep() = %#v; want repository-idempotent convergence", second)
	}
	if store.applyAttempts != 2 || store.historyCount() != initialHistoryCount+1 {
		t.Fatalf("repository attempts=%d histories=%d; want two applies but only one new history", store.applyAttempts, store.historyCount())
	}
	if store.candidate.CurrentValuationStatus != USDValuationStatusUnpriced || store.candidate.CurrentValuationSource != "" {
		t.Fatalf("permanent unpriced projection = %#v", store.candidate)
	}
}

func TestCompanyFundCurrentValuator_UnmappedFingerprintRemainsLegacyCompatible(t *testing.T) {
	now := time.Date(2026, time.July, 16, 6, 5, 0, 0, time.UTC)
	candidate := permanentUnpricedValuationCandidate(32)
	result := permanentUnpricedValuationResult(t, candidate)
	legacyFingerprint := legacyCurrentValuationFingerprint(candidate, result, nil, "current-usd-v1")
	candidate.CurrentValuationDependencyFingerprint = legacyFingerprint
	store := newRepositoryIdempotentValuationStore(candidate)
	initialHistoryCount := store.historyCount()
	valuator := newTestCompanyFundCurrentValuatorWithConfig(
		t,
		now,
		store,
		newCurrentRateRefresherRegistryWithAccount(t, 1, nil),
		newTestCurrentRateCache(t, &now, time.Minute),
		CompanyFundCurrentValuatorConfig{DefaultMappings: CoinGeckoDefaultRateMappings{}},
	)

	sweep := valuator.Sweep(t.Context(), 10)
	if sweep.Err != nil || sweep.Applied != 0 || sweep.Converged != 1 || store.historyCount() != initialHistoryCount {
		t.Fatalf("legacy-compatible Sweep() = %#v histories=%d; code-only upgrade must not append history", sweep, store.historyCount())
	}

	unmapped := companyFundCurrentValuationFingerprint(candidate, result, nil, nil, nil, "current-usd-v1")
	if unmapped != legacyFingerprint {
		t.Fatalf("unmapped fingerprint = %s, want legacy %s", unmapped, legacyFingerprint)
	}
	firstMapping := defaultRateKeyForFingerprintTest(t, candidate.Asset, "asset-one")
	secondMapping := defaultRateKeyForFingerprintTest(t, candidate.Asset, "asset-two")
	firstMapped := companyFundCurrentValuationFingerprint(candidate, result, nil, &firstMapping, nil, "current-usd-v1")
	secondMapped := companyFundCurrentValuationFingerprint(candidate, result, nil, &secondMapping, nil, "current-usd-v1")
	if firstMapped == unmapped || secondMapped == unmapped || firstMapped == secondMapped {
		t.Fatalf("mapping identity must change fingerprint: unmapped=%s first=%s second=%s", unmapped, firstMapped, secondMapped)
	}
	blankPolicy := &AccountAssetPolicy{ID: 41, AccountID: 1, Asset: candidate.Asset, Enabled: true}
	blankUnmapped := companyFundCurrentValuationFingerprint(candidate, result, blankPolicy, nil, nil, "current-usd-v1")
	blankFirstMapped := companyFundCurrentValuationFingerprint(candidate, result, blankPolicy, &firstMapping, nil, "current-usd-v1")
	blankSecondMapped := companyFundCurrentValuationFingerprint(candidate, result, blankPolicy, &secondMapping, nil, "current-usd-v1")
	if blankFirstMapped == blankUnmapped || blankSecondMapped == blankUnmapped || blankFirstMapped == blankSecondMapped {
		t.Fatalf("blank-policy fallback mapping must change fingerprint: unmapped=%s first=%s second=%s", blankUnmapped, blankFirstMapped, blankSecondMapped)
	}
}

func TestCompanyFundCurrentValuator_ExplicitPolicyFingerprintRemainsLegacyCompatible(t *testing.T) {
	candidate := newValuationRuntimeCandidate(33, "ETH", decimal.NewFromInt(1))
	policy := &AccountAssetPolicy{
		ID: 42, AccountID: 1, Asset: candidate.Asset, CoinGeckoID: "ethereum", Enabled: true,
	}
	result, err := EvaluateUSDValue(candidate.usdValuationInput())
	if err != nil {
		t.Fatalf("EvaluateUSDValue() error = %v", err)
	}
	rateKey, ok := CoinGeckoQuoteCacheKeyForPolicy(*policy)
	if !ok {
		t.Fatal("explicit policy must provide a rate key")
	}
	legacyFingerprint := legacyCurrentValuationFingerprint(candidate, result, policy, "current-usd-v1")
	currentFingerprint := companyFundCurrentValuationFingerprint(candidate, result, policy, &rateKey, nil, "current-usd-v1")
	if currentFingerprint != legacyFingerprint {
		t.Fatalf("explicit-policy fingerprint = %s, want legacy %s", currentFingerprint, legacyFingerprint)
	}
}

func permanentUnpricedValuationCandidate(id int64) CompanyFundTransactionValuationCandidate {
	candidate := newValuationRuntimeCandidate(id, "ZZZ", decimal.NewFromInt(1))
	candidate.ToCompanyFundAccountID = int64Pointer(1)
	candidate.CurrentValuationHistoryID = int64Pointer(700 + id)
	candidate.CurrentValuationStatus = USDValuationStatusUnpriced
	candidate.CurrentValuationSource = ""
	return candidate
}

func permanentUnpricedValuationResult(t *testing.T, candidate CompanyFundTransactionValuationCandidate) USDValuationResult {
	t.Helper()
	direct, err := EvaluateUSDValue(candidate.usdValuationInput())
	if err != nil {
		t.Fatalf("EvaluateUSDValue() error = %v", err)
	}
	result := companyFundUnpricedMappingResult(direct)
	if result.Status != USDValuationStatusUnpriced || result.Reason != USDValuationReasonMappingMissing || result.Source != "" {
		t.Fatalf("permanent unpriced result = %#v", result)
	}
	return result
}

func defaultRateKeyForFingerprintTest(t *testing.T, asset AssetIdentity, coinID string) CoinGeckoQuoteCacheKey {
	t.Helper()
	key, ok := CoinGeckoQuoteCacheKeyForDefault(asset, CoinGeckoDefaultRateMappings{
		Crypto: map[string]string{asset.Currency: coinID},
	})
	if !ok {
		t.Fatalf("default rate key for %s was not configured", coinID)
	}
	return key
}

// This is the deployed v1 fingerprint tuple before mapping identity was added.
// Keeping the fixture local makes an intentional compatibility break visible.
func legacyCurrentValuationFingerprint(
	candidate CompanyFundTransactionValuationCandidate,
	result USDValuationResult,
	policy *AccountAssetPolicy,
	policyVersion string,
) string {
	values := []string{
		"company-fund-current-valuation-v1",
		fmt.Sprintf("%d", candidate.ID),
		string(candidate.Channel),
		string(candidate.MovementKind),
		string(candidate.Direction),
		strings.ToUpper(strings.TrimSpace(candidate.Currency)),
		candidate.Amount.String(),
		normalizeAssetIdentity(candidate.Asset).canonicalKey(),
		fmt.Sprintf("unrecognized=%t", candidate.IsUnrecognizedAsset),
		companyFundValuationFingerprintTime(candidate.OccurredAt),
		companyFundValuationFingerprintTime(candidate.CompletedAt),
		candidate.FirstSeenAt.UTC().Format(time.RFC3339Nano),
		companyFundValuationFingerprintID(candidate.ProviderTransactionFactID),
		companyFundValuationFingerprintDecimal(candidate.ProviderReportedUSD),
		string(candidate.ProviderValueScope),
		string(candidate.ProviderAllocationState),
		candidate.AirwallexConversionFrom,
		candidate.AirwallexConversionTo,
		policyVersion,
		string(result.Status),
		string(result.Reason),
		string(result.Source),
		string(result.Method),
		string(result.Basis),
		companyFundValuationFingerprintDecimal(result.Value),
		result.UnitPrice.String(),
		companyFundValuationFingerprintTime(result.ValuationTargetAt),
		companyFundValuationFingerprintTime(result.PriceAt),
		companyFundValuationFingerprintTime(result.AvailableAt),
		result.Granularity,
	}
	if policy != nil {
		values = append(values,
			fmt.Sprintf("%d", policy.ID),
			normalizeAssetIdentity(policy.Asset).canonicalKey(),
			strings.TrimSpace(policy.CoinGeckoID),
			strings.TrimSpace(policy.CoinGeckoPlatformID),
			strings.TrimSpace(policy.CoinGeckoContractAddress),
		)
	} else {
		values = append(values, "no-policy")
	}
	values = append(values, "no-quote")
	digest := sha256.Sum256([]byte(lengthDelimitedTuple(values)))
	return hex.EncodeToString(digest[:])
}

type repositoryIdempotentValuationStore struct {
	candidate     CompanyFundTransactionValuationCandidate
	histories     map[string]struct{}
	nextHistoryID int64
	applyAttempts int
}

func newRepositoryIdempotentValuationStore(candidate CompanyFundTransactionValuationCandidate) *repositoryIdempotentValuationStore {
	store := &repositoryIdempotentValuationStore{
		candidate:     candidate,
		histories:     make(map[string]struct{}),
		nextHistoryID: 900,
	}
	if candidate.CurrentValuationHistoryID != nil {
		store.histories[valuationApplyIdentity(candidate.CurrentValuationDependencyFingerprint, "current-usd-v1", companyFundCurrentValuationTrigger)] = struct{}{}
	}
	return store
}

func (store *repositoryIdempotentValuationStore) GetCompanyFundTransactionValuationCandidate(_ context.Context, transactionID int64) (*CompanyFundTransactionValuationCandidate, error) {
	if transactionID != store.candidate.ID {
		return nil, nil
	}
	candidate := store.candidate
	return &candidate, nil
}

func (store *repositoryIdempotentValuationStore) ListCompanyFundValuationRepairCandidates(_ context.Context, limit int) ([]CompanyFundTransactionValuationCandidate, error) {
	return store.ListCompanyFundValuationRepairCandidatesAfter(context.Background(), 0, limit)
}

func (store *repositoryIdempotentValuationStore) ListCompanyFundValuationRepairCandidatesAfter(_ context.Context, afterID int64, limit int) ([]CompanyFundTransactionValuationCandidate, error) {
	if limit <= 0 || store.candidate.ID <= afterID {
		return nil, nil
	}
	return []CompanyFundTransactionValuationCandidate{store.candidate}, nil
}

func (store *repositoryIdempotentValuationStore) ApplyCompanyFundValuation(_ context.Context, input CompanyFundValuationApplyInput) (CompanyFundValuationApplyResult, error) {
	store.applyAttempts++
	if input.ExpectedCurrentHistoryID == nil || store.candidate.CurrentValuationHistoryID == nil ||
		*input.ExpectedCurrentHistoryID != *store.candidate.CurrentValuationHistoryID ||
		input.ExpectedCurrentDependencyFingerprint != store.candidate.CurrentValuationDependencyFingerprint {
		return CompanyFundValuationApplyResult{Superseded: true}, nil
	}
	identity := valuationApplyIdentity(input.DependencyFingerprint, input.PolicyVersion, input.TransitionTrigger)
	if _, exists := store.histories[identity]; exists {
		return CompanyFundValuationApplyResult{}, nil
	}
	store.histories[identity] = struct{}{}
	store.nextHistoryID++
	historyID := store.nextHistoryID
	store.candidate.CurrentValuationHistoryID = &historyID
	store.candidate.CurrentValuationDependencyFingerprint = input.DependencyFingerprint
	store.candidate.CurrentValuationStatus = input.Result.Status
	store.candidate.CurrentValuationSource = input.Result.Source
	return CompanyFundValuationApplyResult{Inserted: true}, nil
}

func (store *repositoryIdempotentValuationStore) historyCount() int {
	return len(store.histories)
}

func valuationApplyIdentity(fingerprint, policyVersion, trigger string) string {
	return lengthDelimitedTuple([]string{fingerprint, policyVersion, trigger})
}
