package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AlertService dispatches Safeheron Phase 1 alerts (MANUAL_REVIEW / FAILED) to
// Feishu webhook + an optional email distribution list. Both sinks are
// best-effort: a failure on either is logged but does not propagate.
type AlertService struct {
	feishuURL    string
	feishuSecret string // signing secret; empty = no signature
	recipients   []string
	emailSvc     alertEmailer
	httpClient   *http.Client
}

// alertEmailer is the narrow EmailService surface used here so the AlertService
// can be unit-tested without spinning up Resend.
//
// SendAlertEmail (NOT SendActivationEmail) carries an explicit subject so the
// alert title reaches the operator inbox. T7-I-6.
type alertEmailer interface {
	SendAlertEmail(ctx context.Context, toEmail, subject, body string) error
}

type RoutingDeliveryOutcome string

const (
	RoutingDeliverySent              RoutingDeliveryOutcome = "SENT"
	RoutingDeliveryDefinitelyNotSent RoutingDeliveryOutcome = "DEFINITELY_NOT_SENT"
	RoutingDeliveryUnknown           RoutingDeliveryOutcome = "DELIVERY_UNKNOWN"
)

type RoutingSink struct {
	Kind        string
	Fingerprint string
}

// NewAlertService configures a Feishu+email alert dispatcher.
// feishuSecret is the Signing Secret from the Lark bot security settings;
// leave empty to send unsigned (works only when bot signing is disabled).
func NewAlertService(feishuURL, feishuSecret string, recipients []string, email alertEmailer) *AlertService {
	return &AlertService{
		feishuURL:    feishuURL,
		feishuSecret: feishuSecret,
		recipients:   recipients,
		emailSvc:     email,
		httpClient:   &http.Client{Timeout: 5 * time.Second},
	}
}

// Send fires an alert. Safe to pass an empty fields map. Level conventions:
// "INFO" / "WARN" / "ERROR". Errors from the underlying sinks are logged only.
func (a *AlertService) Send(level, title string, fields map[string]string) {
	if a == nil {
		return
	}
	prefix := classifyAlertPrefix(title)
	msg := formatAlert(prefix, level, title, fields)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.sendFeishu(ctx, msg)
	a.sendEmail(ctx, prefix, title, msg)
}

func (a *AlertService) RoutingSinks() []RoutingSink {
	if a == nil {
		return nil
	}
	result := make([]RoutingSink, 0, len(a.recipients)+1)
	if a.feishuURL != "" {
		result = append(result, RoutingSink{Kind: "LARK", Fingerprint: routingSinkFingerprint("LARK", a.feishuURL)})
	}
	for _, recipient := range a.recipients {
		trimmed := strings.ToLower(strings.TrimSpace(recipient))
		if trimmed != "" {
			result = append(result, RoutingSink{Kind: "EMAIL", Fingerprint: routingSinkFingerprint("EMAIL", trimmed)})
		}
	}
	return result
}

func (a *AlertService) SendRouting(ctx context.Context, kind, fingerprint, level, title string, fields map[string]string) RoutingDeliveryOutcome {
	if a == nil {
		return RoutingDeliveryDefinitelyNotSent
	}
	prefix := classifyAlertPrefix(title)
	message := formatAlert(prefix, level, title, fields)
	switch kind {
	case "LARK":
		if fingerprint != routingSinkFingerprint("LARK", a.feishuURL) || a.feishuURL == "" {
			return RoutingDeliveryDefinitelyNotSent
		}
		return a.sendFeishuResult(ctx, message)
	case "EMAIL":
		for _, recipient := range a.recipients {
			trimmed := strings.ToLower(strings.TrimSpace(recipient))
			if fingerprint == routingSinkFingerprint("EMAIL", trimmed) {
				if a.emailSvc == nil {
					return RoutingDeliveryDefinitelyNotSent
				}
				if err := a.emailSvc.SendAlertEmail(ctx, recipient, prefix+title, message); err != nil {
					return RoutingDeliveryUnknown
				}
				return RoutingDeliverySent
			}
		}
		return RoutingDeliveryDefinitelyNotSent
	default:
		return RoutingDeliveryDefinitelyNotSent
	}
}

func routingSinkFingerprint(kind, target string) string {
	sum := sha256.Sum256([]byte(kind + "\x1f" + strings.TrimSpace(target)))
	return fmt.Sprintf("%x", sum[:])
}

func (a *AlertService) sendFeishu(ctx context.Context, msg string) {
	if outcome := a.sendFeishuResult(ctx, msg); outcome != RoutingDeliverySent && a.feishuURL != "" {
		log.Printf("alert: feishu send outcome=%s", outcome)
	}
}

func (a *AlertService) sendFeishuResult(ctx context.Context, msg string) RoutingDeliveryOutcome {
	if a.feishuURL == "" {
		return RoutingDeliveryDefinitelyNotSent
	}

	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": msg},
	}
	if a.feishuSecret != "" {
		// Lark signing: HMAC-SHA256(key=timestamp+"\n"+secret, message="")
		// https://open.larksuite.com/document/server-docs/bot-v3/add-custom-bot
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(timestamp+"\n"+a.feishuSecret))
		payload["timestamp"] = timestamp
		payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("alert: feishu marshal failed: %v", err)
		return RoutingDeliveryDefinitelyNotSent
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.feishuURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("alert: feishu request build failed: %v", err)
		return RoutingDeliveryDefinitelyNotSent
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		log.Printf("alert: feishu send failed: %v", err)
		return RoutingDeliveryUnknown
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("alert: feishu http error status=%d", resp.StatusCode)
		if resp.StatusCode < 500 {
			return RoutingDeliveryDefinitelyNotSent
		}
		return RoutingDeliveryUnknown
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil {
		log.Printf("alert: feishu response parse failed: %v", err)
		return RoutingDeliveryUnknown
	}
	if result.Code != 0 {
		log.Printf("alert: feishu error code=%d msg=%s", result.Code, result.Msg)
		return RoutingDeliveryDefinitelyNotSent
	}
	return RoutingDeliverySent
}

func (a *AlertService) sendEmail(ctx context.Context, prefix, title, body string) {
	if a.emailSvc == nil || len(a.recipients) == 0 {
		return
	}
	subject := prefix + title
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

func formatAlert(prefix, level, title string, fields map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%slevel=%s\n", prefix, level)
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
