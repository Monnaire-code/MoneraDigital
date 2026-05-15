package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// AlertService dispatches Safeheron Phase 1 alerts (MANUAL_REVIEW / FAILED) to
// Feishu webhook + an optional email distribution list. Both sinks are
// best-effort: a failure on either is logged but does not propagate.
type AlertService struct {
	feishuURL  string
	recipients []string
	emailSvc   alertEmailer
	httpClient *http.Client
}

// alertEmailer is the narrow EmailService surface used here so the AlertService
// can be unit-tested without spinning up Resend.
//
// SendAlertEmail (NOT SendActivationEmail) carries an explicit subject so the
// alert title reaches the operator inbox. T7-I-6.
type alertEmailer interface {
	SendAlertEmail(ctx context.Context, toEmail, subject, body string) error
}

// NewAlertService configures a Feishu+email alert dispatcher.
func NewAlertService(feishuURL string, recipients []string, email alertEmailer) *AlertService {
	return &AlertService{
		feishuURL:  feishuURL,
		recipients: recipients,
		emailSvc:   email,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// Send fires an alert. Safe to pass an empty fields map. Level conventions:
// "INFO" / "WARN" / "ERROR". Errors from the underlying sinks are logged only.
func (a *AlertService) Send(level, title string, fields map[string]string) {
	if a == nil {
		return
	}
	msg := formatAlert(level, title, fields)
	// 5s deadline covers both the Feishu request and the email fan-out so an
	// unhealthy sink can't pin the caller.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.sendFeishu(ctx, msg)
	a.sendEmail(ctx, title, msg)
}

func (a *AlertService) sendFeishu(ctx context.Context, msg string) {
	if a.feishuURL == "" {
		return
	}
	body, err := json.Marshal(map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": msg},
	})
	if err != nil {
		log.Printf("alert: feishu marshal failed: %v", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.feishuURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("alert: feishu request build failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		log.Printf("alert: feishu send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("alert: feishu non-2xx status: %d", resp.StatusCode)
	}
}

func (a *AlertService) sendEmail(ctx context.Context, title, body string) {
	if a.emailSvc == nil || len(a.recipients) == 0 {
		return
	}
	subject := classifyAlertPrefix(title) + title
	for _, addr := range a.recipients {
		if err := a.emailSvc.SendAlertEmail(ctx, addr, subject, body); err != nil {
			log.Printf("alert: email send to %s failed: %v", addr, err)
		}
	}
}

// classifyAlertPrefix maps title keywords to a Chinese category prefix.
// KYT/AML takes priority over deposit/withdraw.
func classifyAlertPrefix(title string) string {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "kyt") || strings.Contains(t, "aml"):
		return "【AML告警】"
	case strings.Contains(t, "deposit"):
		return "【充值告警】"
	case strings.Contains(t, "withdraw"):
		return "【提现告警】"
	default:
		return "【系统告警】"
	}
}

func formatAlert(level, title string, fields map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%slevel=%s\n", classifyAlertPrefix(title), level)
	fmt.Fprintf(&b, "title=%s\n", title)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, fields[k])
	}
	return b.String()
}
