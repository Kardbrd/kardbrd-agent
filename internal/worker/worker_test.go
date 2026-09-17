package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

type fixedClock struct{ value time.Time }

func (c fixedClock) Now() time.Time { return c.value }

type countedRunner struct {
	mu       sync.Mutex
	calls    int
	result   AdapterResult
	before   func(Packet)
	released chan struct{}
}

type blockingRunner struct {
	started chan struct{}
}

func (r blockingRunner) Run(ctx context.Context, _ Packet) (AdapterResult, error) {
	close(r.started)
	<-ctx.Done()
	return AdapterResult{}, ctx.Err()
}

type manualLeaseTicker struct{ ch chan time.Time }

func (t manualLeaseTicker) C() <-chan time.Time { return t.ch }
func (t manualLeaseTicker) Stop()               {}

type blockingNotifier struct{ started chan struct{} }

func (n blockingNotifier) Deliver(ctx context.Context, _ NoticePacket) (string, error) {
	close(n.started)
	<-ctx.Done()
	return "", ctx.Err()
}

type countedNotifier struct {
	mu      sync.Mutex
	calls   int
	receipt string
}

func (n *countedNotifier) Deliver(_ context.Context, packet NoticePacket) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	if packet.NoticeID == "" || packet.RunID == "" {
		return "", fmt.Errorf("missing stable notice identity")
	}
	return n.receipt, nil
}

func (n *countedNotifier) Calls() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

func (r *countedRunner) Run(_ context.Context, packet Packet) (AdapterResult, error) {
	r.mu.Lock()
	r.calls++
	before := r.before
	released := r.released
	result := r.result
	r.mu.Unlock()
	if before != nil {
		before(packet)
	}
	if released != nil {
		<-released
	}
	return result, nil
}

func (r *countedRunner) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type metadataServer struct {
	t                    *testing.T
	mu                   sync.Mutex
	boardID              string
	cards                map[string]cardView
	metadata             map[string]map[string]json.RawMessage
	revisions            map[string]int64
	comments             int
	commentFail          bool
	created              int
	metadataConflicts    int
	conflictAfterComment int
	markdownMutation     func()
	createAmbiguous      bool
	server               *httptest.Server
}

func newMetadataServer(t *testing.T) *metadataServer {
	t.Helper()
	fake := &metadataServer{
		t:         t,
		boardID:   "board-1",
		cards:     map[string]cardView{},
		metadata:  map[string]map[string]json.RawMessage{},
		revisions: map[string]int64{},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *metadataServer) client() *api.Client {
	client := api.NewClient(f.server.URL, "test-token")
	client.SetNoRetry(true)
	return client
}

func (f *metadataServer) add(cardID string, record Record) {
	f.t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	card := cardView{ID: cardID, Title: cardID}
	card.Board.ID = f.boardID
	f.cards[cardID] = card
	f.metadata[cardID] = map[string]json.RawMessage{MetadataKey: encoded, "keep": json.RawMessage(`9007199254740993`)}
}

func (f *metadataServer) record(cardID string) Record {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	record, err := ParseRecord(f.metadata[cardID][MetadataKey])
	if err != nil {
		f.t.Fatal(err)
	}
	return record
}

func (f *metadataServer) setRecord(cardID string, record Record) {
	f.t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metadata[cardID][MetadataKey] = encoded
	f.revisions[cardID]++
}

func (f *metadataServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	write := func(status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
	if r.URL.Path == "/api/boards/board-1/" && r.Method == http.MethodGet {
		cards := make([]cardView, 0, len(f.cards))
		for _, card := range f.cards {
			cards = append(cards, card)
		}
		write(http.StatusOK, map[string]any{"data": map[string]any{"id": f.boardID, "lists": []any{map[string]any{"cards": cards}}}})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "cards" {
		cardID := parts[2]
		card, exists := f.cards[cardID]
		if !exists {
			write(http.StatusNotFound, map[string]string{"error": "missing"})
			return
		}
		if len(parts) == 3 && r.Method == http.MethodGet {
			if r.Header.Get("Accept") == "text/markdown" {
				if f.markdownMutation != nil {
					f.markdownMutation()
					f.markdownMutation = nil
				}
				w.Header().Set("Content-Type", "text/markdown")
				_, _ = w.Write([]byte("# " + cardID + "\n"))
				return
			}
			write(http.StatusOK, map[string]any{"data": card})
			return
		}
		if len(parts) == 4 && parts[3] == "metadata" {
			if r.Method == http.MethodGet {
				write(http.StatusOK, map[string]any{"data": map[string]any{"id": cardID, "metadata": f.metadata[cardID], "metadata_revision": f.revisions[cardID]}})
				return
			}
			if r.Method == http.MethodPost {
				var patch struct {
					Set      map[string]json.RawMessage `json:"set"`
					Remove   []string                   `json:"remove"`
					Expected int64                      `json:"expected_revision"`
				}
				if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
					f.t.Error(err)
					write(http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				if f.metadataConflicts > 0 || (f.conflictAfterComment > 0 && f.comments > 0) {
					if f.metadataConflicts > 0 {
						f.metadataConflicts--
					}
					if f.conflictAfterComment > 0 && f.comments > 0 {
						f.conflictAfterComment--
					}
					f.revisions[cardID]++
					write(http.StatusConflict, map[string]string{"error": "metadata changed", "code": "METADATA_CONFLICT"})
					return
				}
				if patch.Expected != f.revisions[cardID] {
					write(http.StatusConflict, map[string]string{"error": "metadata changed", "code": "METADATA_CONFLICT"})
					return
				}
				for key, value := range patch.Set {
					f.metadata[cardID][key] = append(json.RawMessage(nil), value...)
				}
				for _, key := range patch.Remove {
					delete(f.metadata[cardID], key)
				}
				f.revisions[cardID]++
				write(http.StatusOK, map[string]any{"data": map[string]any{"id": cardID, "metadata": f.metadata[cardID], "metadata_revision": f.revisions[cardID]}})
				return
			}
		}
		if len(parts) == 4 && parts[3] == "comments" && r.Method == http.MethodPost {
			f.comments++
			if f.commentFail {
				write(http.StatusInternalServerError, map[string]string{"error": "ambiguous comment"})
				return
			}
			write(http.StatusOK, map[string]any{"data": map[string]string{"id": fmt.Sprintf("comment-%d", f.comments)}})
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/boards/board-1/lists/") && strings.HasSuffix(r.URL.Path, "/cards/") && r.Method == http.MethodPost {
		var body struct{ Title, Description string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created++
		id := fmt.Sprintf("suggestion-%d", f.created)
		card := cardView{ID: id, Title: body.Title}
		card.Board.ID = f.boardID
		f.cards[id] = card
		f.metadata[id] = map[string]json.RawMessage{}
		if f.createAmbiguous {
			write(http.StatusInternalServerError, map[string]string{"error": "ambiguous create"})
			return
		}
		write(http.StatusOK, map[string]any{"data": map[string]string{"id": id}})
		return
	}
	write(http.StatusNotFound, map[string]string{"error": "unhandled " + path.Clean(r.URL.Path)})
}

func testRecord(state State) Record {
	record, err := newDelegatedRecord("synthetic goal", "synthetic criteria", json.RawMessage(`{"scope":"fixture"}`), nil, nil)
	if err != nil {
		panic(err)
	}
	record.State = state
	if state == StateScheduled {
		at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		record.WakeAt = &at
	}
	if state == StateWaitingUser {
		record.Decision = &Decision{Prompt: "approve?"}
	}
	return record
}

func testService(fake *metadataServer, runner Runner, now time.Time) Service {
	return Service{
		Store:  Store{Client: fake.client()},
		Config: Config{BoardID: fake.boardID, WorkerID: "test-worker", PollInterval: time.Second, LeaseDuration: time.Minute, RunTimeout: time.Minute, NoticeTimeout: time.Second, MaxConcurrent: 2, ArtifactDir: "/tmp/worker-test-artifacts", OutputLimit: 4096, PacketLimit: 4096, Runner: runner},
		Clock:  fixedClock{value: now},
	}
}

func TestParseRecordRejectsUnknownVersionAndSuggestionPromotion(t *testing.T) {
	_, err := ParseRecord(json.RawMessage(`{"version":2,"goal":"g","completion_criteria":"c","state":"ready","delegation":{"delegated":true,"authorization":{}}}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %v", err)
	}
	suggested := Record{Version: SchemaVersion, Goal: "g", CompletionCriteria: "c", State: StateSuggested, Delegation: Delegation{Delegated: true}}
	if err := suggested.Validate(); err == nil {
		t.Fatal("delegated suggestion must be rejected")
	}
	_, err = ParseRecord(json.RawMessage(`{"version":1,"goal":"g","completion_criteria":"c","state":"ready","delegation":{"delegated":true,"authorization":{}},"action":{"id":"action-1","intent":{},"receipt":{"action_id":"wrong-action","receipt_id":"receipt-1"}}}`))
	if err == nil || !strings.Contains(err.Error(), "action receipt") {
		t.Fatalf("malformed stored action receipt error = %v", err)
	}
}

func TestStorePutPreservesUnrelatedExactJSON(t *testing.T) {
	fake := newMetadataServer(t)
	fake.add("card-1", testRecord(StateReady))
	store := Store{Client: fake.client()}
	snapshot, err := store.Load(context.Background(), "card-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Record.State = StatePaused
	if _, err := store.Put(context.Background(), snapshot, snapshot.Record); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := string(fake.metadata["card-1"]["keep"]); got != "9007199254740993" {
		t.Fatalf("unrelated exact JSON = %s", got)
	}
}

func TestTwoWorkersClaimOnlyOnceOverHTTP(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("card-1", testRecord(StateReady))
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted, Summary: "done", ReceiptID: "receipt-1"}}
	first, second := testService(fake, runner, now), testService(fake, runner, now)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, service := range []Service{first, second} {
		wg.Add(1)
		go func(service Service) {
			defer wg.Done()
			<-start
			if _, err := service.RunOnce(context.Background()); err != nil {
				t.Error(err)
			}
		}(service)
	}
	close(start)
	wg.Wait()
	if got := runner.Calls(); got != 1 {
		t.Fatalf("runner calls = %d, want 1", got)
	}
	if got := fake.record("card-1").State; got != StateCompleted {
		t.Fatalf("state = %s", got)
	}
}

func TestStaleCompletionCannotCommitAfterRevocation(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("card-1", testRecord(StateReady))
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted, Summary: "should not apply"}}
	runner.before = func(Packet) {
		record := fake.record("card-1")
		record.State = StateCancelled
		record.Delegation.Delegated = false
		record.Run = nil
		fake.setRecord("card-1", record)
	}
	if _, err := testService(fake, runner, now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.record("card-1").State; got != StateCancelled {
		t.Fatalf("stale completion changed state to %s", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.comments != 0 {
		t.Fatalf("stale completion posted %d comments", fake.comments)
	}
}

func TestRevocationAfterClaimBeforeRunnerPreventsAdapterLaunch(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("card-1", testRecord(StateReady))
	fake.mu.Lock()
	fake.markdownMutation = func() {
		record, err := ParseRecord(fake.metadata["card-1"][MetadataKey])
		if err != nil {
			fake.t.Fatal(err)
		}
		record.State = StateCancelled
		record.Delegation.Delegated = false
		record.Run = nil
		encoded, _ := json.Marshal(record)
		fake.metadata["card-1"][MetadataKey] = encoded
		fake.revisions["card-1"]++
	}
	fake.mu.Unlock()
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted}}
	if _, err := testService(fake, runner, now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.Calls() != 0 {
		t.Fatal("revoked task launched an adapter after context fetch")
	}
	if got := fake.record("card-1").State; got != StateCancelled {
		t.Fatalf("state = %s", got)
	}
}

func TestExpiredRunNeedsReviewWithoutRerun(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	record := testRecord(StateRunning)
	fence, err := claimFence(record)
	if err != nil {
		t.Fatal(err)
	}
	record.Run = &Run{ID: "old-run", OwnerToken: "old-owner", Fence: fence, StartedAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Hour), LeaseExpires: now.Add(-time.Minute)}
	fake.add("card-1", record)
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted}}
	if _, err := testService(fake, runner, now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.Calls() != 0 {
		t.Fatal("expired run must not rerun")
	}
	if got := fake.record("card-1").State; got != StateNeedsReview {
		t.Fatalf("state = %s", got)
	}
}

func TestExpiredRecoveryRechecksCancellationBeforeWriting(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	record := testRecord(StateRunning)
	fence, err := claimFence(record)
	if err != nil {
		t.Fatal(err)
	}
	record.Run = &Run{ID: "old-run", OwnerToken: "old-owner", Fence: fence, StartedAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Hour), LeaseExpires: now.Add(-time.Minute)}
	fake.add("card-1", record)
	service := testService(fake, &countedRunner{}, now)
	stale, err := service.Store.Load(context.Background(), "card-1")
	if err != nil {
		t.Fatal(err)
	}
	cancelled := fake.record("card-1")
	cancelled.State = StateCancelled
	cancelled.Delegation.Delegated = false
	cancelled.Run = nil
	fake.setRecord("card-1", cancelled)
	if err := service.reconcileExpired(context.Background(), stale); !errors.Is(err, ErrLostOwnership) {
		t.Fatalf("reconcile error = %v", err)
	}
	if got := fake.record("card-1").State; got != StateCancelled {
		t.Fatalf("stale recovery overwrote cancellation as %s", got)
	}
}

func TestLeaseOwnershipLossCancelsRunnerAndPreventsStaleResult(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("card-1", testRecord(StateReady))
	runner := blockingRunner{started: make(chan struct{})}
	service := testService(fake, runner, now)
	ticks := make(chan time.Time, 1)
	service.NewLeaseTicker = func(time.Duration) LeaseTicker { return manualLeaseTicker{ch: ticks} }
	done := make(chan error, 1)
	go func() {
		_, err := service.RunOnce(context.Background())
		done <- err
	}()
	<-runner.started
	stolen := fake.record("card-1")
	stolen.Run.OwnerToken = "new-owner"
	fake.setRecord("card-1", stolen)
	ticks <- now
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel after losing its lease ownership")
	}
	if got := fake.record("card-1").Run.OwnerToken; got != "new-owner" {
		t.Fatalf("stale worker overwrote owner %q", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.comments != 0 {
		t.Fatal("lost worker posted a stale result")
	}
}

func TestAuthorizationFenceChangeCancelsRunner(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("card-1", testRecord(StateReady))
	runner := blockingRunner{started: make(chan struct{})}
	service := testService(fake, runner, now)
	ticks := make(chan time.Time, 1)
	service.NewLeaseTicker = func(time.Duration) LeaseTicker { return manualLeaseTicker{ch: ticks} }
	done := make(chan error, 1)
	go func() { _, err := service.RunOnce(context.Background()); done <- err }()
	<-runner.started
	changed := fake.record("card-1")
	changed.Delegation.Authorization = json.RawMessage(`{"scope":"narrowed"}`)
	fake.setRecord("card-1", changed)
	ticks <- now
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := string(fake.record("card-1").Delegation.Authorization); got != `{"scope":"narrowed"}` {
		t.Fatalf("stale runner overwrote current authorization: %s", got)
	}
}

func TestExpiredCompletionCannotCommit(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("card-1", testRecord(StateReady))
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted}}
	runner.before = func(Packet) {
		record := fake.record("card-1")
		record.Run.LeaseExpires = now.Add(-time.Second)
		fake.setRecord("card-1", record)
	}
	if _, err := testService(fake, runner, now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.record("card-1").State; got != StateRunning {
		t.Fatalf("late completion changed expired claim to %s", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.comments != 0 {
		t.Fatalf("late completion posted %d comments", fake.comments)
	}
}

func TestInvalidScheduledAndActionReceiptResultsNeedReview(t *testing.T) {
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	t.Run("past scheduled wake", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.add("card-1", testRecord(StateReady))
		past := now.Add(-time.Second)
		if _, err := testService(fake, &countedRunner{result: AdapterResult{Status: ResultScheduled, WakeAt: &past}}, now).RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := fake.record("card-1").State; got != StateNeedsReview {
			t.Fatalf("state = %s", got)
		}
	})
	t.Run("action without receipt", func(t *testing.T) {
		fake := newMetadataServer(t)
		record := testRecord(StateReady)
		record.Action = &ActionIntent{ID: "action-1", Intent: json.RawMessage(`{"kind":"fixture"}`)}
		fake.add("card-1", record)
		if _, err := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted}}, now).RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := fake.record("card-1").State; got != StateNeedsReview {
			t.Fatalf("state = %s", got)
		}
	})
}

func TestActionIntentIsDurableBeforeAdapterAndReceiptMustMatch(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	record := testRecord(StateReady)
	record.Action = &ActionIntent{ID: "action-1", Intent: json.RawMessage(`{"kind":"fixture"}`)}
	fake.add("card-1", record)
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted, ActionStatus: "performed", ActionReceipt: &ActionReceipt{ActionID: "action-1", ReceiptID: "effect-1"}}}
	runner.before = func(packet Packet) {
		live := fake.record("card-1")
		if live.State != StateRunning || live.Run == nil || live.Action == nil || live.Action.ID != "action-1" || packet.Action == nil || packet.Action.ID != "action-1" {
			t.Errorf("action intent was not durable before adapter: %#v / %#v", live, packet)
		}
	}
	if _, err := testService(fake, runner, now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.record("card-1").Action.Receipt.ReceiptID; got != "effect-1" {
		t.Fatalf("stored action receipt = %s", got)
	}
}

func TestNoticeReceiptAndAmbiguousCommentAreNeverRetried(t *testing.T) {
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	t.Run("receipt", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.add("card-1", testRecord(StateReady))
		notifier := &countedNotifier{receipt: "notice-receipt-1"}
		service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted, Summary: "done"}}, now)
		service.Config.Notifier = notifier
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		record := fake.record("card-1")
		if record.Journal == nil || record.Journal.State != "posted" || record.Notice == nil || record.Notice.State != "delivered" || record.Notice.ReceiptID != "notice-receipt-1" {
			t.Fatalf("receipt record = %#v", record)
		}
		if notifier.Calls() != 1 {
			t.Fatalf("notifier calls = %d", notifier.Calls())
		}
	})
	t.Run("ambiguous comment", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.commentFail = true
		fake.add("card-1", testRecord(StateReady))
		service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted, Summary: "done"}}, now)
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := fake.record("card-1").Journal.State; got != "unknown" {
			t.Fatalf("journal state = %s", got)
		}
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if fake.comments != 1 {
			t.Fatalf("ambiguous comment retried %d times", fake.comments)
		}
	})
	t.Run("receipt metadata conflict is safely persisted without resend", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.add("card-1", testRecord(StateReady))
		fake.conflictAfterComment = 1
		notifier := &countedNotifier{receipt: "notice-receipt-1"}
		service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted, Summary: "done"}}, now)
		service.Config.Notifier = notifier
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		record := fake.record("card-1")
		if record.Journal.State != "posted" || record.Notice.State != "delivered" || notifier.Calls() != 1 {
			t.Fatalf("receipt update after conflict = %#v, notifier=%d", record, notifier.Calls())
		}
	})
	t.Run("notifier is bounded and uncertain receipt is not retried", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.add("card-1", testRecord(StateReady))
		notifier := blockingNotifier{started: make(chan struct{})}
		service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted, Summary: "done"}}, now)
		service.Config.Notifier = notifier
		service.Config.NoticeTimeout = 10 * time.Millisecond
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-notifier.started
		if got := fake.record("card-1").Notice.State; got != "unknown" {
			t.Fatalf("notice after timeout = %s", got)
		}
	})
}

func TestWakeDecideAndSuggestionDeduplicateWithoutRunner(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	event := testRecord(StateWaitingEvent)
	event.EventID = "event-1"
	user := testRecord(StateWaitingUser)
	fake.add("event-card", event)
	fake.add("user-card", user)
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted}}
	service := testService(fake, runner, now)
	if changed, err := service.Wake(context.Background(), "event-card", "event-1"); err != nil || !changed {
		t.Fatalf("wake = %v, %v", changed, err)
	}
	if changed, err := service.Wake(context.Background(), "event-card", "event-1"); err != nil || changed {
		t.Fatalf("duplicate wake = %v, %v", changed, err)
	}
	if changed, err := service.Decide(context.Background(), "user-card", "decision-1", json.RawMessage(`true`)); err != nil || !changed {
		t.Fatalf("decide = %v, %v", changed, err)
	}
	if changed, err := service.Decide(context.Background(), "user-card", "decision-1", json.RawMessage(`true`)); err != nil || changed {
		t.Fatalf("duplicate decision = %v, %v", changed, err)
	}
	fake.add("suggestion-registry", newSuggestionRegistryRecord())
	report, err := service.IngestSuggestions(context.Background(), "suggestion-registry", "list-1", []Suggestion{{SourceID: "source-1", Title: "proposal", Proposal: "fixture proposal"}, {SourceID: "source-1", Title: "proposal", Proposal: "fixture proposal"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Created) != 1 || report.Duplicates != 1 {
		t.Fatalf("ingest report = %#v", report)
	}
	if runner.Calls() != 0 {
		t.Fatal("suggestion ingestion invoked a runner")
	}
	if got := fake.record(report.Created[0]); got.State != StateSuggested || got.Delegation.Delegated {
		t.Fatalf("suggestion was promoted: %#v", got)
	}
}

func TestQuietNoopAndWaitingUserIsolation(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("waiting", testRecord(StateWaitingUser))
	fake.add("ready", testRecord(StateReady))
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted}}
	if _, err := testService(fake, runner, now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.Calls() != 1 || fake.record("waiting").State != StateWaitingUser || fake.record("ready").State != StateCompleted {
		t.Fatalf("waiting task blocked independent task")
	}
	fake.mu.Lock()
	comments := fake.comments
	fake.mu.Unlock()
	if _, err := testService(fake, &countedRunner{}, now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.comments != comments {
		t.Fatal("unchanged pass posted a repetitive comment")
	}
}

func TestDecisionReceiptRemainsDeduplicatedAcrossAnotherWait(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	user := testRecord(StateWaitingUser)
	fake.add("user-card", user)
	service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultWaitingUser, DecisionPrompt: "confirm again?"}}, now)
	if changed, err := service.Decide(context.Background(), "user-card", "decision-1", json.RawMessage(`true`)); err != nil || !changed {
		t.Fatalf("initial decision = %v, %v", changed, err)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.record("user-card").State; got != StateWaitingUser {
		t.Fatalf("second wait state = %s", got)
	}
	if changed, err := service.Decide(context.Background(), "user-card", "decision-1", json.RawMessage(`true`)); err != nil || changed {
		t.Fatalf("replayed decision unblocked a new wait: %v, %v", changed, err)
	}
}

func TestSuggestionRegistrySerializesConcurrentAndAmbiguousIngestion(t *testing.T) {
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	t.Run("concurrent", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.add("registry", newSuggestionRegistryRecord())
		first, second := testService(fake, &countedRunner{}, now), testService(fake, &countedRunner{}, now)
		start := make(chan struct{})
		type result struct {
			report IngestReport
			err    error
		}
		results := make(chan result, 2)
		for _, service := range []Service{first, second} {
			go func(service Service) {
				<-start
				report, err := service.IngestSuggestions(context.Background(), "registry", "list-1", []Suggestion{{SourceID: "source-1", Title: "proposal", Proposal: "fixture"}})
				results <- result{report: report, err: err}
			}(service)
		}
		close(start)
		for range 2 {
			if result := <-results; result.err != nil {
				t.Fatal(result.err)
			}
		}
		fake.mu.Lock()
		created := fake.created
		fake.mu.Unlock()
		if created != 1 {
			t.Fatalf("concurrent source created %d cards", created)
		}
	})
	t.Run("ambiguous create holds source", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.add("registry", newSuggestionRegistryRecord())
		fake.createAmbiguous = true
		service := testService(fake, &countedRunner{}, now)
		if _, err := service.IngestSuggestions(context.Background(), "registry", "list-1", []Suggestion{{SourceID: "source-1", Title: "proposal", Proposal: "fixture"}}); err == nil {
			t.Fatal("ambiguous creation must require review")
		}
		if claim := fake.record("registry").SuggestionClaims["source-1"]; claim.State != "needs_review" {
			t.Fatalf("ambiguous source claim = %#v", claim)
		}
		if report, err := service.IngestSuggestions(context.Background(), "registry", "list-1", []Suggestion{{SourceID: "source-1", Title: "proposal", Proposal: "fixture"}}); err != nil || report.Duplicates != 1 {
			t.Fatalf("duplicate after ambiguity = %#v, %v", report, err)
		}
		fake.mu.Lock()
		created := fake.created
		fake.mu.Unlock()
		if created != 1 {
			t.Fatalf("ambiguous source was re-created %d times", created)
		}
	})
}

func TestActionReceiptCannotBeOverwrittenOnContinuation(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	record := testRecord(StateReady)
	record.Action = &ActionIntent{ID: "action-1", Intent: json.RawMessage(`{"kind":"fixture"}`)}
	fake.add("card-1", record)
	first := &countedRunner{result: AdapterResult{Status: ResultWaitingEvent, EventID: "event-1", ActionStatus: "performed", ActionReceipt: &ActionReceipt{ActionID: "action-1", ReceiptID: "effect-1"}}}
	if _, err := testService(fake, first, now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted, ActionStatus: "performed", ActionReceipt: &ActionReceipt{ActionID: "action-1", ReceiptID: "effect-2"}}}, now)
	if changed, err := service.Wake(context.Background(), "card-1", "event-1"); err != nil || !changed {
		t.Fatalf("wake = %v, %v", changed, err)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated := fake.record("card-1")
	if updated.State != StateNeedsReview || updated.Action.Receipt == nil || updated.Action.Receipt.ReceiptID != "effect-1" {
		t.Fatalf("second performed action overwrote durable receipt: %#v", updated)
	}
}
