package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Kardbrd/kardbrd-agent/internal/commentpolicy"
	"github.com/Kardbrd/kardbrd-agent/internal/executor"
)

func TestStructuredIntakePersistsConciseRejection(t *testing.T) {
	m := newTestManager(t)
	m.ClaimDir = t.TempDir()
	reason := errors.New(strings.Repeat("secret ", 300))
	ctx := context.WithValue(context.Background(), replayIntakeKey{}, true)
	if err := m.rejectStructuredIntake(ctx, map[string]any{"card_id": "card1", "comment_id": "trigger", "author_name": "Paul"}, nil, reason); err != nil {
		t.Fatal(err)
	}
	claim, err := m.readMentionClaim("card1", "trigger")
	if err != nil {
		t.Fatal(err)
	}
	if err := commentpolicy.ValidateBotComment(claim.OutcomeBody); err != nil || strings.Contains(claim.OutcomeBody, "secret") || !strings.Contains(claim.OutcomeBody, "request") {
		t.Fatalf("unsafe persisted rejection: %q, %v", claim.OutcomeBody, err)
	}
}

func TestStructuredHoldPersistsStableConciseBody(t *testing.T) {
	m := newTestManager(t)
	m.ClaimDir = t.TempDir()
	m.BotID, m.verifiedBotID = "bot1", "bot1"
	claim, _, err := m.createOrReadMentionClaim("card1", "trigger", "Paul", mentionDispatch{}, structuredSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.publishStructuredHold(context.Background(), claim, "checking authentication", errors.New(strings.Repeat("secret ", 300))); err != nil {
		t.Fatal(err)
	}
	saved, err := m.readMentionClaim("card1", "trigger")
	if err != nil {
		t.Fatal(err)
	}
	if err := commentpolicy.ValidateBotComment(saved.IntakeErrorBody); err != nil || strings.Contains(saved.IntakeErrorBody, "secret") {
		t.Fatalf("unsafe persisted hold: %q, %v", saved.IntakeErrorBody, err)
	}
	if err := m.publishStructuredHold(context.Background(), claim, "different phase", errors.New("different reason")); err != nil {
		t.Fatal(err)
	}
	if got := m.Client.(*fakeBoardClient).commentsSnapshot(); len(got) != 1 || got[0].content != saved.IntakeErrorBody {
		t.Fatalf("hold replay changed body: %#v", got)
	}
}

func TestErrorCommentKeepsDiagnosticOutOfCard(t *testing.T) {
	body := buildErrorComment(executor.Result{Error: "secret credential", Stderr: "secret stderr"}, "Error")
	if strings.Contains(body, "secret") || !strings.Contains(body, "run log") {
		t.Fatalf("error notice = %q", body)
	}
}
