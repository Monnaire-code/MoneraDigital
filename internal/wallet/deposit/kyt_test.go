package deposit

import (
	"testing"
	"time"

	"monera-digital/internal/safeheron"
)

func TestSummarizeRiskLevel(t *testing.T) {
	tests := []struct {
		name     string
		amlList  []safeheron.AmlReport
		expected string
	}{
		{"COMPLETED LOW", []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "LOW"}}, KytLow},
		{"COMPLETED MEDIUM", []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "MEDIUM"}}, KytMedium},
		{"COMPLETED HIGH", []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "HIGH"}}, KytHigh},
		{"COMPLETED SEVERE", []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "SEVERE"}}, KytSevere},
		{"COMPLETED UNKNOWN", []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "UNKNOWN"}}, KytUnknown},
		{"PENDING status", []safeheron.AmlReport{{Status: "PENDING", RiskLevel: ""}}, KytPending},
		{"FAILED status", []safeheron.AmlReport{{Status: "FAILED", RiskLevel: ""}}, KytFailed},
		{"SKIPPED status", []safeheron.AmlReport{{Status: "SKIPPED", RiskLevel: ""}}, KytSkipped},
		{"PENDING beats FAILED", []safeheron.AmlReport{
			{Status: "FAILED", RiskLevel: ""},
			{Status: "PENDING", RiskLevel: ""},
		}, KytPending},
		{"multi COMPLETED takes highest", []safeheron.AmlReport{
			{Status: "COMPLETED", RiskLevel: "LOW"},
			{Status: "COMPLETED", RiskLevel: "HIGH"},
			{Status: "COMPLETED", RiskLevel: "MEDIUM"},
		}, KytHigh},
		{"empty list returns EMPTY sentinel", []safeheron.AmlReport{}, KytEmpty},
		{"nil list returns EMPTY sentinel", nil, KytEmpty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SummarizeRiskLevel(tt.amlList)
			if got != tt.expected {
				t.Errorf("SummarizeRiskLevel() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDecideKYT(t *testing.T) {
	low := []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "LOW"}}
	high := []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "HIGH"}}
	severe := []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "SEVERE"}}
	medium := []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "MEDIUM"}}
	unknown := []safeheron.AmlReport{{Status: "COMPLETED", RiskLevel: "UNKNOWN"}}
	pending := []safeheron.AmlReport{{Status: "PENDING"}}
	failed := []safeheron.AmlReport{{Status: "FAILED"}}
	skipped := []safeheron.AmlReport{{Status: "SKIPPED"}}

	tests := []struct {
		name           string
		state          string
		amlList        []safeheron.AmlReport
		isAfterTimeout bool
		wantAction     KytDecisionAction
		wantRisk       string
		wantReason     string
		wantAlert      string
	}{
		// 初查路径
		{"IN_PROGRESS", "IN_PROGRESS", nil, false, KytActionKeepPending, KytPending, "", ""},
		{"UNTRIGGERED", "UNTRIGGERED", nil, false, KytActionManualReview, KytUnknown, ReasonKytUntriggered, "WARN"},
		{"TRIGGERED LOW", "TRIGGERED", low, false, KytActionCredit, KytLow, "", ""},
		{"TRIGGERED HIGH", "TRIGGERED", high, false, KytActionManualReview, KytHigh, "KYT_RISK_HIGH", "ERROR"},
		{"TRIGGERED SEVERE", "TRIGGERED", severe, false, KytActionManualReview, KytSevere, "KYT_RISK_SEVERE", "ERROR"},
		{"TRIGGERED MEDIUM", "TRIGGERED", medium, false, KytActionManualReview, KytMedium, "KYT_RISK_MEDIUM", "WARN"},
		{"TRIGGERED UNKNOWN", "TRIGGERED", unknown, false, KytActionManualReview, KytUnknown, "KYT_RISK_UNKNOWN", "WARN"},
		{"TRIGGERED PENDING", "TRIGGERED", pending, false, KytActionKeepPending, KytPending, "", ""},
		{"TRIGGERED FAILED", "TRIGGERED", failed, false, KytActionManualReview, KytFailed, ReasonKytProviderFailed, "WARN"},
		{"TRIGGERED SKIPPED", "TRIGGERED", skipped, false, KytActionManualReview, KytSkipped, ReasonKytSkipped, "WARN"},

		// I-2: TRIGGERED + empty amlList → ManualReview (not silent credit)
		{"TRIGGERED empty list", "TRIGGERED", nil, false, KytActionManualReview, KytEmpty, ReasonKytEmptyAmlList, "WARN"},
		{"TRIGGERED empty list timeout", "TRIGGERED", []safeheron.AmlReport{}, true, KytActionManualReview, KytEmpty, ReasonKytEmptyAmlList, "WARN"},

		// S-3: unknown state → ManualReview
		{"unknown state", "BANANA", nil, false, KytActionManualReview, KytUnknown, ReasonKytUnknownState, "ERROR"},
		{"empty state", "", nil, false, KytActionManualReview, KytUnknown, ReasonKytUnknownState, "ERROR"},

		// 超时兜底路径
		{"IN_PROGRESS timeout", "IN_PROGRESS", nil, true, KytActionManualReview, KytPending, ReasonKytTimeoutStillPending, "ERROR"},
		{"UNTRIGGERED timeout", "UNTRIGGERED", nil, true, KytActionManualReview, KytUnknown, ReasonKytUntriggeredAfterTimeout, "WARN"},
		{"TRIGGERED LOW timeout", "TRIGGERED", low, true, KytActionCredit, KytLow, "", ""},
		{"TRIGGERED HIGH timeout", "TRIGGERED", high, true, KytActionManualReview, KytHigh, "KYT_RISK_HIGH_AFTER_TIMEOUT", "ERROR"},
		{"TRIGGERED SEVERE timeout", "TRIGGERED", severe, true, KytActionManualReview, KytSevere, "KYT_RISK_SEVERE_AFTER_TIMEOUT", "ERROR"},
		{"TRIGGERED MEDIUM timeout", "TRIGGERED", medium, true, KytActionManualReview, KytMedium, "KYT_RISK_MEDIUM_AFTER_TIMEOUT", "WARN"},
		{"TRIGGERED PENDING timeout", "TRIGGERED", pending, true, KytActionManualReview, KytPending, ReasonKytTimeoutStillPending, "ERROR"},
		{"TRIGGERED FAILED timeout", "TRIGGERED", failed, true, KytActionManualReview, KytFailed, ReasonKytProviderFailedAfterTimeout, "WARN"},
		{"TRIGGERED SKIPPED timeout", "TRIGGERED", skipped, true, KytActionManualReview, KytSkipped, ReasonKytSkippedAfterTimeout, "WARN"},
		{"TRIGGERED UNKNOWN timeout", "TRIGGERED", unknown, true, KytActionManualReview, KytUnknown, "KYT_RISK_UNKNOWN_AFTER_TIMEOUT", "WARN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideKYT(tt.state, tt.amlList, tt.isAfterTimeout)
			if got.Action != tt.wantAction {
				t.Errorf("Action = %d, want %d", got.Action, tt.wantAction)
			}
			if got.RiskLevel != tt.wantRisk {
				t.Errorf("RiskLevel = %q, want %q", got.RiskLevel, tt.wantRisk)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if tt.wantAlert != "" && got.AlertLevel != tt.wantAlert {
				t.Errorf("AlertLevel = %q, want %q", got.AlertLevel, tt.wantAlert)
			}
		})
	}
}

func TestAlertLevelForKyt(t *testing.T) {
	tests := []struct {
		risk string
		want string
	}{
		{KytHigh, "ERROR"},
		{KytSevere, "ERROR"},
		{KytLow, "WARN"},
		{KytMedium, "WARN"},
		{KytUnknown, "WARN"},
		{KytFailed, "WARN"},
	}
	for _, tt := range tests {
		if got := AlertLevelForKyt(tt.risk); got != tt.want {
			t.Errorf("AlertLevelForKyt(%q) = %q, want %q", tt.risk, got, tt.want)
		}
	}
}

func TestMaxLastUpdateTime(t *testing.T) {
	t.Run("single valid timestamp", func(t *testing.T) {
		aml := []safeheron.AmlReport{{LastUpdateTime: "1715500001000"}}
		got := maxLastUpdateTime(aml)
		want := time.UnixMilli(1715500001000)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("empty LastUpdateTime fallback", func(t *testing.T) {
		aml := []safeheron.AmlReport{{LastUpdateTime: ""}}
		before := time.Now()
		got := maxLastUpdateTime(aml)
		after := time.Now()
		if got.Before(before) || got.After(after) {
			t.Errorf("expected time.Now() fallback, got %v", got)
		}
	})

	t.Run("multiple picks max", func(t *testing.T) {
		aml := []safeheron.AmlReport{
			{LastUpdateTime: "1715500001000"},
			{LastUpdateTime: "1715500003000"},
			{LastUpdateTime: "1715500002000"},
		}
		got := maxLastUpdateTime(aml)
		want := time.UnixMilli(1715500003000)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("all parse failures fallback", func(t *testing.T) {
		aml := []safeheron.AmlReport{
			{LastUpdateTime: "not-a-number"},
			{LastUpdateTime: ""},
		}
		before := time.Now()
		got := maxLastUpdateTime(aml)
		after := time.Now()
		if got.Before(before) || got.After(after) {
			t.Errorf("expected time.Now() fallback, got %v", got)
		}
	})
}

func TestBuildKytRiskReason(t *testing.T) {
	if got := BuildKytRiskReason("HIGH"); got != "KYT_RISK_HIGH" {
		t.Errorf("got %q", got)
	}
}

func TestBuildKytTimeoutRiskReason(t *testing.T) {
	if got := BuildKytTimeoutRiskReason("SEVERE"); got != "KYT_RISK_SEVERE_AFTER_TIMEOUT" {
		t.Errorf("got %q", got)
	}
}
