package worker

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHumanUpdatesRetainMachineEvidenceOnlyInMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result AdapterResult
		want   string
	}{
		{"completed", AdapterResult{Status: ResultCompleted, Summary: "Sent the introduction email."}, "Sent the introduction email."},
		{"decision", AdapterResult{Status: ResultWaitingUser, Summary: "The gardener quoted $120.", DecisionPrompt: "Would you like to accept the quote?"}, "Would you like to accept the quote?"},
		{"diagnostic", AdapterResult{Status: ResultNeedsReview, ReviewNote: "RPCError for request-0123456789abcdef"}, "It will not be retried automatically."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newMetadataServer(t)
			record := testRecord(StateReady)
			record.Sources = []SourceRef{{ID: "request-0123456789abcdef", Reference: "codex://threads/internal-thread/messages/internal-message"}}
			fake.add("card-1", record)
			notifier := &receiptNotifier{receipt: NoticeReceipt{ReceiptID: "verified-notice-receipt"}}
			service := testService(fake, &countedRunner{result: tc.result}, time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC))
			service.Config.Notifier = notifier
			if _, err := service.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			stored := fake.record("card-1")
			packets := notifier.Packets()
			if len(packets) != 1 || len(fake.commentBodies) != 1 {
				t.Fatalf("expected one human update, got %d comments and %d notifications", len(fake.commentBodies), len(packets))
			}
			message := fake.commentBodies[0]
			if message != packets[0].Message || !strings.Contains(message, tc.want) {
				t.Fatalf("human update lost the useful result: %q", message)
			}
			for _, internal := range []string{stored.Outcome.RunID, record.Sources[0].ID, record.Sources[0].Reference, "Run:", "RPCError"} {
				if internal != "" && strings.Contains(message, internal) {
					t.Errorf("human update contains execution detail %q: %q", internal, message)
				}
			}
			if stored.Outcome.RunID == "" || packets[0].RunID != stored.Outcome.RunID || stored.Sources[0] != record.Sources[0] || stored.Outcome.ReviewNote != tc.result.ReviewNote {
				t.Fatal("structured run identity, source provenance or diagnostic evidence was lost")
			}
			if stored.Journal.State != "posted" || stored.Notice.State != "delivered" {
				t.Fatal("human formatting changed journal or notification receipt handling")
			}
			if _, err := service.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(fake.commentBodies) != 1 || notifier.Calls() != 1 {
				t.Fatal("an unchanged poll repeated the human update")
			}
		})
	}
}
