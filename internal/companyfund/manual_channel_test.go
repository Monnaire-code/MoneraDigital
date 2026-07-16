package companyfund

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestManualChannelIsAValidValuationCandidate(t *testing.T) {
	candidate := CompanyFundTransactionValuationCandidate{
		ID:           101,
		Channel:      Channel("MANUAL"),
		MovementKind: MovementKindAdjustment,
		Direction:    DirectionInflow,
		Currency:     "USDT",
		Amount:       decimal.RequireFromString("12.5"),
		FirstSeenAt:  time.Date(2026, time.July, 16, 1, 2, 3, 0, time.UTC),
	}

	if !candidate.Channel.Valid() {
		t.Fatal("MANUAL channel must be accepted by the provider-neutral domain")
	}
	if err := candidate.validate(); err != nil {
		t.Fatalf("manual valuation candidate must be valid: %v", err)
	}
}
