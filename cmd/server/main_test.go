package main

import (
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"monera-digital/internal/logger"
)

func TestEmailServiceStatusLogExcludesConfiguredAPIKey(t *testing.T) {
	const apiKey = "resend-test-key-must-not-appear-in-logs"
	t.Setenv("RESEND_API_KEY", apiKey)

	previousLogger := logger.Logger
	core, observed := observer.New(zap.InfoLevel)
	logger.Logger = zap.New(core).Sugar()
	t.Cleanup(func() {
		logger.Logger = previousLogger
	})

	logEmailServiceStatus(true, "ops@example.test")

	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("status entries = %d, want 1", len(entries))
	}

	entry := entries[0]
	if entry.Message != "[EmailService] Status check" {
		t.Fatalf("message = %q", entry.Message)
	}

	fields := entry.ContextMap()
	if fields["enabled"] != true {
		t.Fatalf("enabled = %#v, want true", fields["enabled"])
	}
	if fields["SENDER_EMAIL"] != "ops@example.test" {
		t.Fatalf("SENDER_EMAIL = %#v", fields["SENDER_EMAIL"])
	}
	if _, exists := fields["RESEND_API_KEY"]; exists {
		t.Fatal("status log contains RESEND_API_KEY")
	}
	if strings.Contains(fmt.Sprint(fields), apiKey) {
		t.Fatal("status log contains configured API key")
	}
}
