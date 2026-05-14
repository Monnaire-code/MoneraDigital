// Package alertrender renders the HTML for Safeheron Phase 1 alert emails.
// Lives in its own sub-package so the escape logic can be unit-tested without
// pulling in the wider services package (which carries pre-existing
// baseline test failures unrelated to this work).
package alertrender

import (
	"fmt"
	"html"
)

// BuildAlertHTML renders the alert email HTML with caller-supplied subject and
// body HTML-escaped. Webhook-derived fields (destinationAddress, coinKey, ...)
// reach this function via formatAlert(); without escaping, a crafted webhook
// payload could inject HTML into the operator's inbox. R2-I-2.
func BuildAlertHTML(subject, body string) string {
	return fmt.Sprintf(
		`<!DOCTYPE html><html><body style="font-family:Arial,sans-serif;line-height:1.5;color:#222">`+
			`<h2 style="color:#b00020">%s</h2>`+
			`<pre style="background:#f5f5f5;padding:12px;border-radius:6px;white-space:pre-wrap">%s</pre>`+
			`</body></html>`,
		html.EscapeString(subject), html.EscapeString(body),
	)
}
