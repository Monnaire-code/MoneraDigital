package safeheron

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/api"
)

type replayWebhookAPI struct {
	resendFn       func(api.ResendWebhookRequest, *api.ResultResponse) error
	resendFailedFn func(api.ResendFailedRequest, *api.MessagesCountResponse) error
}

func (m *replayWebhookAPI) ResendWebhook(req api.ResendWebhookRequest, resp *api.ResultResponse) error {
	return m.resendFn(req, resp)
}

func (m *replayWebhookAPI) ResendFailed(req api.ResendFailedRequest, resp *api.MessagesCountResponse) error {
	return m.resendFailedFn(req, resp)
}

func TestReplayTransactionWebhook_UsesTransactionCategory(t *testing.T) {
	client := &Client{webhookReplay: &replayWebhookAPI{
		resendFn: func(req api.ResendWebhookRequest, resp *api.ResultResponse) error {
			if req.Category != "TRANSACTION" || req.TxKey != "tx-001" {
				t.Fatalf("unexpected replay request: %#v", req)
			}
			resp.Result = true
			return nil
		},
	}}

	accepted, err := client.ReplayTransactionWebhook(context.Background(), "tx-001")
	if err != nil {
		t.Fatalf("ReplayTransactionWebhook: %v", err)
	}
	if !accepted {
		t.Fatal("replay result = false, want true")
	}
}

func TestReplayTransactionWebhook_RejectsBlankTxKeyBeforeSDKCall(t *testing.T) {
	called := false
	client := &Client{webhookReplay: &replayWebhookAPI{
		resendFn: func(api.ResendWebhookRequest, *api.ResultResponse) error {
			called = true
			return nil
		},
	}}
	_, err := client.ReplayTransactionWebhook(context.Background(), " \t ")
	if err == nil || !strings.Contains(err.Error(), "txKey") {
		t.Fatalf("ReplayTransactionWebhook error = %v, want txKey validation", err)
	}
	if called {
		t.Fatal("SDK called for blank txKey")
	}
}

func TestReplayTransactionWebhook_WrapsSDKError(t *testing.T) {
	client := &Client{webhookReplay: &replayWebhookAPI{
		resendFn: func(api.ResendWebhookRequest, *api.ResultResponse) error {
			return errors.New("sdk unavailable")
		},
	}}
	_, err := client.ReplayTransactionWebhook(context.Background(), "tx-001")
	if err == nil || !strings.Contains(err.Error(), "ResendWebhook") {
		t.Fatalf("ReplayTransactionWebhook error = %v, want wrapped SDK error", err)
	}
}

func TestValidateFailedWebhookReplayWindow(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC).UnixMilli()
	hour := time.Hour.Milliseconds()
	week := (7 * 24 * time.Hour).Milliseconds()

	cases := []struct {
		name  string
		input WebhookReplayWindow
		want  string
	}{
		{"valid one hour", WebhookReplayWindow{StartTime: now - hour, EndTime: now}, ""},
		{"valid seven day boundary", WebhookReplayWindow{StartTime: now - week, EndTime: now - week + hour}, ""},
		{"missing start", WebhookReplayWindow{EndTime: now}, "positive"},
		{"reversed", WebhookReplayWindow{StartTime: now, EndTime: now - 1}, "after"},
		{"too wide", WebhookReplayWindow{StartTime: now - hour - 1, EndTime: now}, "one hour"},
		{"too old", WebhookReplayWindow{StartTime: now - week - 1, EndTime: now - week - 1 + hour}, "past seven days"},
		{"future", WebhookReplayWindow{StartTime: now - hour + 1, EndTime: now + 1}, "future"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateFailedWebhookReplayWindow(testCase.input, now)
			if testCase.want == "" && err != nil {
				t.Fatalf("validateFailedWebhookReplayWindow(%#v) = %v", testCase.input, err)
			}
			if testCase.want != "" && (err == nil || !strings.Contains(err.Error(), testCase.want)) {
				t.Fatalf("validateFailedWebhookReplayWindow(%#v) = %v, want %q", testCase.input, err, testCase.want)
			}
		})
	}
}

func TestReplayFailedWebhooks_ForwardsValidatedWindow(t *testing.T) {
	now := time.Now().UnixMilli()
	input := WebhookReplayWindow{StartTime: now - time.Hour.Milliseconds(), EndTime: now}
	client := &Client{webhookReplay: &replayWebhookAPI{
		resendFailedFn: func(req api.ResendFailedRequest, resp *api.MessagesCountResponse) error {
			if req.StartTime != input.StartTime || req.EndTime != input.EndTime {
				t.Fatalf("unexpected replay window: %#v", req)
			}
			resp.MessagesCount = 7
			return nil
		},
	}}

	count, err := client.ReplayFailedWebhooks(context.Background(), input)
	if err != nil {
		t.Fatalf("ReplayFailedWebhooks: %v", err)
	}
	if count != 7 {
		t.Fatalf("ReplayFailedWebhooks count = %d, want 7", count)
	}
}

func TestReplayFailedWebhooks_WrapsSDKError(t *testing.T) {
	now := time.Now().UnixMilli()
	client := &Client{webhookReplay: &replayWebhookAPI{
		resendFailedFn: func(api.ResendFailedRequest, *api.MessagesCountResponse) error {
			return errors.New("sdk unavailable")
		},
	}}
	_, err := client.ReplayFailedWebhooks(context.Background(), WebhookReplayWindow{
		StartTime: now - time.Minute.Milliseconds(),
		EndTime:   now,
	})
	if err == nil || !strings.Contains(err.Error(), "ResendFailed") {
		t.Fatalf("ReplayFailedWebhooks error = %v, want wrapped SDK error", err)
	}
}
