package alertrender

import (
	"strings"
	"testing"
)

// TestBuildAlertHTML_EscapesUserContent verifies that alert email HTML escapes
// caller-supplied content. Webhook-derived fields land in the body via
// formatAlert(), and an attacker who can influence those fields must not be
// able to inject HTML into the operator's inbox. R2-I-2.
func TestBuildAlertHTML_EscapesUserContent(t *testing.T) {
	body := "destinationAddress=<img src=x onerror=alert(1)>\nreason=<script>x</script>"
	out := BuildAlertHTML("【Phase1告警】<script>1</script>", body)

	// Once the surrounding angle brackets are escaped (<img → &lt;img), the
	// content is no longer a tag and inert attribute strings like
	// `onerror=alert(1)` cannot execute. Assertion targets the structural
	// markers — angle brackets in tag positions — not the attribute text.
	for _, leak := range []string{"<script>", "<img src=x", "</script>"} {
		if strings.Contains(out, leak) {
			t.Errorf("alert HTML must escape user content; found raw %q in output", leak)
		}
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected escaped <script> visible to operator, html=%q", out)
	}
}

func TestBuildAlertHTML_PreservesSafePlainText(t *testing.T) {
	out := BuildAlertHTML("Deposit manual review",
		"reason=ADDRESS_UNASSIGNED\nuserId=42\namount=0.5")
	if !strings.Contains(out, "Deposit manual review") {
		t.Errorf("plain ASCII subject must round-trip, html=%q", out)
	}
	if !strings.Contains(out, "reason=ADDRESS_UNASSIGNED") {
		t.Errorf("plain ASCII body must round-trip, html=%q", out)
	}
}
