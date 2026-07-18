package fundrouting

import (
	"fmt"
	"strings"
)

type Mode string

const (
	ModeCaptureOnly          Mode = "capture-only"
	ModeRoutingAuthoritative Mode = "routing-authoritative"
)

func ParseMode(raw string) (Mode, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(raw)))
	switch mode {
	case ModeCaptureOnly, ModeRoutingAuthoritative:
		return mode, nil
	default:
		return "", fmt.Errorf("SAFEHERON_TRANSACTION_ROUTING_MODE must be capture-only or routing-authoritative")
	}
}
