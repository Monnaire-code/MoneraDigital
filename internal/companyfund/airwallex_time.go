package companyfund

import (
	"fmt"
	"strings"
	"time"
)

var airwallexTokenExpiryLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05Z0700",
}

// parseAirwallexTokenExpiry accepts ISO8601 timestamps with either RFC3339's
// colonized offset or the documented +0000-style offset. Go accepts optional
// fractional seconds while parsing the second layout as well.
func parseAirwallexTokenExpiry(value string) (time.Time, error) {
	for _, layout := range airwallexTokenExpiryLayouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid airwallex token expiry")
}

// parseAirwallexAPIVersion accepts only the date form Airwallex documents for
// x-api-version. Whitespace, partial dates, and timestamps are rejected so a
// deployment cannot accidentally drift to the account-default API contract.
func parseAirwallexAPIVersion(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) {
		return "", fmt.Errorf("airwallex API version must be an exact YYYY-MM-DD date")
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return "", fmt.Errorf("airwallex API version must be an exact YYYY-MM-DD date")
	}
	return value, nil
}
