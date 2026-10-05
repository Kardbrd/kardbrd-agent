package agent

import (
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestBuildCapabilitiesDoesNotInventDefaultsOrChoices(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.SetConnectedBot("bot-1", "socket-1")
	if _, ok := m.BuildExecutionCapabilities(); ok {
		t.Fatal("unverified capabilities were advertised")
	}
	m.CommentExecution = &rules.CommentExecutionConfig{Defaults: rules.CommentDefaults{Model: "verified-model", Effort: "high"}, Models: []rules.CommentModel{{ID: "verified-model", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe", VerifiedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), ExecutorVersion: "codex 0.156.0"}}
	payload, ok := m.BuildExecutionCapabilities()
	if !ok || payload.InstanceID != "socket-1" || payload.BoardID != "board1" || payload.Defaults.Model == nil || *payload.Defaults.Model != "verified-model" || payload.Provenance.ConfigFingerprint == "" {
		t.Fatalf("payload = %+v, ok=%v", payload, ok)
	}
}

func TestRejectedPairIsWithdrawn(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.SetConnectedBot("bot-1", "socket-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Defaults: rules.CommentDefaults{Model: "verified-model", Effort: "high"}, Models: []rules.CommentModel{{ID: "verified-model", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe", VerifiedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), ExecutorVersion: "codex 0.156.0"}}
	if !m.withdrawRejectedPair("verified-model", "high", "The model is not supported with this account") {
		t.Fatal("provider rejection not detected")
	}
	payload, ok := m.BuildExecutionCapabilities()
	if !ok || payload.Defaults.Model != nil || payload.Defaults.Effort != nil || len(payload.Models) != 0 {
		t.Fatalf("payload = %+v, ok=%v", payload, ok)
	}
}

func TestReverifiedCommentConfigRestoresWithdrawnPair(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.SetConnectedBot("bot-1", "socket-1")
	old := &rules.CommentExecutionConfig{Defaults: rules.CommentDefaults{Model: "verified-model", Effort: "high"}, Models: []rules.CommentModel{{ID: "verified-model", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe", VerifiedAt: "2026-10-05T16:00:00Z", ExecutorVersion: "codex 0.156.0"}}
	m.CommentExecution = old
	m.withdrawRejectedPair("verified-model", "high", "model not supported")
	next := *old
	next.Verification.VerifiedAt = "2026-10-05T17:00:00Z"
	if err := m.ApplyRulesConfig(rules.Config{CommentExecution: &next}); err != nil {
		t.Fatal(err)
	}
	payload, ok := m.BuildExecutionCapabilities()
	if !ok || payload.Defaults.Model == nil || len(payload.Models) != 1 {
		t.Fatalf("payload = %+v, ok=%v", payload, ok)
	}
}
