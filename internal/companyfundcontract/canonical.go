package companyfundcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type CanonicalAccountPolicyRecord struct {
	AccountID          int64  `json:"account_id"`
	Channel            string `json:"channel"`
	ProviderAccountKey string `json:"provider_account_key"`
	Address            string `json:"address"`
	NetworkFamily      string `json:"network_family"`
	AccountEnabled     bool   `json:"account_enabled"`
	AssetKey           string `json:"asset_key"`
	PolicyEnabled      bool   `json:"policy_enabled"`
}

type CanonicalAccountPolicyExport struct {
	JSON   []byte `json:"json"`
	SHA256 string `json:"sha256"`
}

func BuildCanonicalAccountPolicyExport(records []CanonicalAccountPolicyRecord) (CanonicalAccountPolicyExport, error) {
	canonical := append([]CanonicalAccountPolicyRecord(nil), records...)
	for index := range canonical {
		canonical[index].Channel = strings.ToUpper(strings.TrimSpace(canonical[index].Channel))
		canonical[index].ProviderAccountKey = strings.TrimSpace(canonical[index].ProviderAccountKey)
		canonical[index].Address = strings.TrimSpace(canonical[index].Address)
		canonical[index].NetworkFamily = strings.ToUpper(strings.TrimSpace(canonical[index].NetworkFamily))
		canonical[index].AssetKey = strings.TrimSpace(canonical[index].AssetKey)
		if canonical[index].AccountID <= 0 || canonical[index].Channel == "" || canonical[index].ProviderAccountKey == "" {
			return CanonicalAccountPolicyExport{}, fmt.Errorf("canonical account/policy record %d is incomplete", index)
		}
	}
	sort.Slice(canonical, func(left, right int) bool {
		leftJSON, _ := json.Marshal(canonical[left])
		rightJSON, _ := json.Marshal(canonical[right])
		return string(leftJSON) < string(rightJSON)
	})
	data, _ := json.Marshal(canonical)
	digest := sha256.Sum256(data)
	return CanonicalAccountPolicyExport{JSON: data, SHA256: hex.EncodeToString(digest[:])}, nil
}
