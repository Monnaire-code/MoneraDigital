package deposit

import (
	"strconv"
	"time"

	"monera-digital/internal/safeheron"
)

// KYT 风险等级（业务汇总，来自 SPEC §6.5.1 处置矩阵）。
// SummarizeRiskLevel 返回值 / deposits.aml_risk_level 列取值。
const (
	KytLow     = "LOW"
	KytMedium  = "MEDIUM"
	KytHigh    = "HIGH"
	KytSevere  = "SEVERE"
	KytUnknown = "UNKNOWN"
	KytFailed  = "FAILED"
	KytSkipped = "SKIPPED"
	KytPending = "PENDING"
	KytEmpty   = "EMPTY"
)

// KYT 失败原因码（deposits.failed_reason 列）。
// 初查路径不带后缀；超时兜底路径带 _AFTER_TIMEOUT，运营一眼可区分。
const (
	ReasonKytUntriggered    = "KYT_UNTRIGGERED"
	ReasonKytRiskPrefix     = "KYT_RISK_"
	ReasonKytProviderFailed = "KYT_PROVIDER_FAILED"
	ReasonKytSkipped        = "KYT_SKIPPED"

	ReasonKytUntriggeredAfterTimeout    = "KYT_UNTRIGGERED_AFTER_TIMEOUT"
	ReasonKytProviderFailedAfterTimeout = "KYT_PROVIDER_FAILED_AFTER_TIMEOUT"
	ReasonKytSkippedAfterTimeout        = "KYT_SKIPPED_AFTER_TIMEOUT"
	ReasonKytTimeoutStillPending        = "KYT_TIMEOUT_STILL_PENDING"

	ReasonKytOrphanAlert  = "KYT_ORPHAN_ALERT"
	ReasonKytApiFailed    = "KYT_API_FAILED"
	ReasonKytEmptyAmlList = "KYT_EMPTY_AML_LIST"
	ReasonKytUnknownState = "KYT_UNKNOWN_STATE"
)

func BuildKytRiskReason(riskLevel string) string {
	return ReasonKytRiskPrefix + riskLevel
}

func BuildKytTimeoutRiskReason(riskLevel string) string {
	return ReasonKytRiskPrefix + riskLevel + "_AFTER_TIMEOUT"
}

// riskSeverity 返回风险等级的数值严重度，数值越大越严重。
func riskSeverity(level string) int {
	switch level {
	case KytLow:
		return 1
	case KytMedium:
		return 2
	case KytUnknown:
		return 3
	case KytHigh:
		return 4
	case KytSevere:
		return 5
	default:
		return 3 // 未知 riskLevel 视同 UNKNOWN
	}
}

// SummarizeRiskLevel 取 amlList 中所有 provider 的最高严重度。
// 优先级：PENDING > FAILED > SKIPPED > riskLevel 比较。
func SummarizeRiskLevel(amlList []safeheron.AmlReport) string {
	if len(amlList) == 0 {
		return KytEmpty
	}
	hasPending := false
	hasFailed := false
	hasSkipped := false
	maxSev := 0
	maxLevel := KytLow

	for _, r := range amlList {
		switch r.Status {
		case "PENDING":
			hasPending = true
		case "FAILED":
			hasFailed = true
		case "SKIPPED":
			hasSkipped = true
		case "COMPLETED":
			sev := riskSeverity(r.RiskLevel)
			if sev > maxSev {
				maxSev = sev
				maxLevel = r.RiskLevel
			}
		}
	}

	if hasPending {
		return KytPending
	}
	if hasFailed {
		return KytFailed
	}
	if hasSkipped {
		return KytSkipped
	}
	return maxLevel
}

// AlertLevelForKyt 把 KYT 风险等级映射到告警级别（K-17）。
func AlertLevelForKyt(riskLevel string) string {
	switch riskLevel {
	case KytHigh, KytSevere:
		return "ERROR"
	default:
		return "WARN"
	}
}

type KytDecisionAction int

const (
	KytActionCredit       KytDecisionAction = iota // CREDITED
	KytActionKeepPending                           // 保持 KYT_PENDING
	KytActionManualReview                          // MANUAL_REVIEW
)

type KytDecision struct {
	Action     KytDecisionAction
	RiskLevel  string
	Reason     string
	AlertLevel string
}

// DecideKYT 根据 amlScreeningState 和 amlList 决定下一步（SPEC §6.5.1 处置矩阵）。
// isAfterTimeout=true 时 reason 带 _AFTER_TIMEOUT 后缀。
func DecideKYT(state string, amlList []safeheron.AmlReport, isAfterTimeout bool) KytDecision {
	switch state {
	case "IN_PROGRESS":
		if isAfterTimeout {
			return KytDecision{
				Action:     KytActionManualReview,
				RiskLevel:  KytPending,
				Reason:     ReasonKytTimeoutStillPending,
				AlertLevel: "ERROR",
			}
		}
		return KytDecision{Action: KytActionKeepPending, RiskLevel: KytPending}

	case "UNTRIGGERED":
		reason := ReasonKytUntriggered
		if isAfterTimeout {
			reason = ReasonKytUntriggeredAfterTimeout
		}
		return KytDecision{
			Action:     KytActionManualReview,
			RiskLevel:  KytUnknown,
			Reason:     reason,
			AlertLevel: "WARN",
		}

	case "TRIGGERED":
		// fall through to risk evaluation below

	default:
		return KytDecision{
			Action:     KytActionManualReview,
			RiskLevel:  KytUnknown,
			Reason:     ReasonKytUnknownState,
			AlertLevel: "ERROR",
		}
	}

	risk := SummarizeRiskLevel(amlList)
	switch risk {
	case KytEmpty:
		return KytDecision{
			Action:     KytActionManualReview,
			RiskLevel:  KytEmpty,
			Reason:     ReasonKytEmptyAmlList,
			AlertLevel: "WARN",
		}
	case KytPending:
		if isAfterTimeout {
			return KytDecision{
				Action:     KytActionManualReview,
				RiskLevel:  KytPending,
				Reason:     ReasonKytTimeoutStillPending,
				AlertLevel: "ERROR",
			}
		}
		return KytDecision{Action: KytActionKeepPending, RiskLevel: KytPending}
	case KytLow:
		return KytDecision{Action: KytActionCredit, RiskLevel: KytLow}
	case KytFailed:
		reason := ReasonKytProviderFailed
		if isAfterTimeout {
			reason = ReasonKytProviderFailedAfterTimeout
		}
		return KytDecision{
			Action:     KytActionManualReview,
			RiskLevel:  KytFailed,
			Reason:     reason,
			AlertLevel: "WARN",
		}
	case KytSkipped:
		reason := ReasonKytSkipped
		if isAfterTimeout {
			reason = ReasonKytSkippedAfterTimeout
		}
		return KytDecision{
			Action:     KytActionManualReview,
			RiskLevel:  KytSkipped,
			Reason:     reason,
			AlertLevel: "WARN",
		}
	default:
		// MEDIUM / HIGH / SEVERE / UNKNOWN
		reason := BuildKytRiskReason(risk)
		if isAfterTimeout {
			reason = BuildKytTimeoutRiskReason(risk)
		}
		return KytDecision{
			Action:     KytActionManualReview,
			RiskLevel:  risk,
			Reason:     reason,
			AlertLevel: AlertLevelForKyt(risk),
		}
	}
}

// maxLastUpdateTime 取 amlList 中 LastUpdateTime（UNIX 毫秒字符串）的最大值。
// 全部解析失败时返回零值 time.Time{} —— S-3：之前回退到 time.Now() 会让
// LockOneKYTPendingTimeout 永远捞不到这条 deposit（updated_at < NOW()-20m
// 永远不成立），导致永久卡 KYT_PENDING。返回零值后超时扫描会立即捡到。
func maxLastUpdateTime(amlList []safeheron.AmlReport) time.Time {
	var max int64
	for _, r := range amlList {
		if ts, err := strconv.ParseInt(r.LastUpdateTime, 10, 64); err == nil && ts > max {
			max = ts
		}
	}
	if max == 0 {
		return time.Time{}
	}
	return time.UnixMilli(max)
}
