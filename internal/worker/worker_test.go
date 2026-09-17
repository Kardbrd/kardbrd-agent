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

type countedObserver struct {
	mu     sync.Mutex
	calls  int
	events []Suggestion
}

func (o *countedObserver) Observe(context.Context) ([]Suggestion, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	return append([]Suggestion(nil), o.events...), nil
}

func (o *countedObserver) Calls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
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

func (n blockingNotifier) Deliver(ctx context.Context, _ NoticePacket) (NoticeReceipt, error) {
	close(n.started)
	<-ctx.Done()
	return NoticeReceipt{}, ctx.Err()
}

// gatedNoticeNotifier lets a test persist external delivery evidence while a
// notifier call is in flight. This is the real host-relay ordering: queue
// acceptance may return after the relay has already confirmed UI display.
type gatedNoticeNotifier struct {
	started chan struct{}
	release chan struct{}
	receipt NoticeReceipt
	err     error
}

func (n gatedNoticeNotifier) Deliver(_ context.Context, packet NoticePacket) (NoticeReceipt, error) {
	close(n.started)
	<-n.release
	receipt := n.receipt
	if receipt.NoticeID == "" {
		receipt.NoticeID = packet.NoticeID
	}
	return receipt, n.err
}

func awaitTestSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func awaitTestError(t *testing.T, result <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}

type countedNotifier struct {
	mu      sync.Mutex
	calls   int
	receipt string
}

func (n *countedNotifier) Deliver(_ context.Context, packet NoticePacket) (NoticeReceipt, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	if packet.NoticeID == "" || packet.RunID == "" {
		return NoticeReceipt{}, fmt.Errorf("missing stable notice identity")
	}
	return NoticeReceipt{NoticeID: packet.NoticeID, ReceiptID: n.receipt}, nil
}

type wrongNoticeNotifier struct{}

func (wrongNoticeNotifier) Deliver(_ context.Context, _ NoticePacket) (NoticeReceipt, error) {
	return NoticeReceipt{NoticeID: "another-notice", ReceiptID: "receipt-1"}, nil
}

type receiptNotifier struct {
	mu      sync.Mutex
	calls   int
	receipt NoticeReceipt
	packets []NoticePacket
}

func (n *receiptNotifier) Deliver(_ context.Context, packet NoticePacket) (NoticeReceipt, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	n.packets = append(n.packets, packet)
	receipt := n.receipt
	if receipt.NoticeID == "" {
		receipt.NoticeID = packet.NoticeID
	}
	return receipt, nil
}

func (n *receiptNotifier) Calls() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

func (n *receiptNotifier) Packets() []NoticePacket {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]NoticePacket(nil), n.packets...)
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
	// Counted runners model a conforming adapter. Individual tests that need
	// malformed output use SubprocessRunner and its synthetic executable.
	if result.RunID == "" {
		result.RunID = packet.RunID
	}
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
	t                     *testing.T
	mu                    sync.Mutex
	boardID               string
	cards                 map[string]cardView
	metadata              map[string]map[string]json.RawMessage
	revisions             map[string]int64
	comments              int
	commentFail           bool
	commentNoID           bool
	created               int
	metadataConflicts     int
	metadataPostStarted   chan struct{}
	metadataPostRelease   <-chan struct{}
	conflictAfterComment  int
	dropCompletedResponse bool
	markdownMutation      func()
	markdownStarted       chan struct{}
	markdownRelease       <-chan struct{}
	renewalStarted        chan struct{}
	renewalRelease        <-chan struct{}
	renewalPostStarted    chan struct{}
	renewalPostRelease    <-chan struct{}
	createAmbiguous       bool
	server                *httptest.Server
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
				if f.markdownStarted != nil {
					select {
					case f.markdownStarted <- struct{}{}:
					default:
					}
				}
				if f.markdownRelease != nil {
					release := f.markdownRelease
					f.mu.Unlock()
					select {
					case <-release:
					case <-r.Context().Done():
					}
					f.mu.Lock()
				}
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
				if f.renewalStarted != nil && f.renewalRelease != nil {
					select {
					case f.renewalStarted <- struct{}{}:
					default:
					}
					release := f.renewalRelease
					f.mu.Unlock()
					select {
					case <-release:
					case <-r.Context().Done():
					}
					f.mu.Lock()
				}
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
				// Pause exactly one metadata POST after its client has read a
				// revision. Tests use this to put an operator acknowledgement
				// between an outbox update's read and CAS write.
				if f.metadataPostStarted != nil && f.metadataPostRelease != nil {
					started, release := f.metadataPostStarted, f.metadataPostRelease
					f.metadataPostStarted = nil
					f.metadataPostRelease = nil
					select {
					case started <- struct{}{}:
					default:
					}
					f.mu.Unlock()
					select {
					case <-release:
					case <-r.Context().Done():
					}
					f.mu.Lock()
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
				// A response may be delayed after the server has already
				// durably accepted a renewal. Tests use this boundary to prove
				// the client keeps the persisted expiry rather than extending it
				// from response arrival.
				if f.renewalPostStarted != nil && f.renewalPostRelease != nil {
					select {
					case f.renewalPostStarted <- struct{}{}:
					default:
					}
					release := f.renewalPostRelease
					f.mu.Unlock()
					select {
					case <-release:
					case <-r.Context().Done():
					}
					f.mu.Lock()
				}
				if f.dropCompletedResponse {
					record, err := ParseRecord(f.metadata[cardID][MetadataKey])
					if err != nil {
						f.t.Error(err)
						write(http.StatusInternalServerError, map[string]string{"error": "invalid committed record"})
						return
					}
					if record.State == StateCompleted {
						f.dropCompletedResponse = false
						write(http.StatusServiceUnavailable, map[string]string{"error": "response lost after commit"})
						return
					}
				}
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
			if f.commentNoID {
				write(http.StatusOK, map[string]any{"data": map[string]string{}})
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
		record.Decision = &Decision{ID: "decision-1", Prompt: "approve?"}
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

func TestLateRunStopsAtCommittedLeaseWhenRenewalBlocks(t *testing.T) {
	fake := newMetadataServer(t)
	fake.add("card-1", testRecord(StateReady))
	markdownStarted := make(chan struct{}, 1)
	markdownRelease := make(chan struct{})
	fake.markdownStarted = markdownStarted
	fake.markdownRelease = markdownRelease
	runner := blockingRunner{started: make(chan struct{})}
	service := testService(fake, runner, time.Now().UTC())
	service.Clock = realClock{}
	service.Config.LeaseDuration = 900 * time.Millisecond
	service.Config.RunTimeout = 3 * time.Second
	done := make(chan error, 1)
	go func() {
		_, err := service.RunOnce(context.Background())
		done <- err
	}()
	select {
	case <-markdownStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not request card context")
	}
	expires := fake.record("card-1").Run.LeaseExpires
	if delay := time.Until(expires.Add(-200 * time.Millisecond)); delay > 0 {
		timer := time.NewTimer(delay)
		<-timer.C
	}
	close(markdownRelease)
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("late runner did not start")
	}
	renewalStarted := make(chan struct{}, 1)
	renewalRelease := make(chan struct{})
	fake.mu.Lock()
	fake.renewalStarted = renewalStarted
	fake.renewalRelease = renewalRelease
	fake.mu.Unlock()
	select {
	case <-renewalStarted:
	case <-time.After(time.Second):
		close(renewalRelease)
		t.Fatal("worker did not start a renewal before lease expiry")
	}
	deadline := time.NewTimer(maxDuration(time.Until(expires.Add(150*time.Millisecond)), time.Millisecond))
	defer deadline.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-deadline.C:
		close(renewalRelease)
		t.Fatal("runner continued beyond its committed lease while renewal was blocked")
	}
	close(renewalRelease)
}

func TestSlowSuccessfulRenewalKeepsPersistedLeaseDeadline(t *testing.T) {
	fake := newMetadataServer(t)
	fake.add("card-1", testRecord(StateReady))
	runner := blockingRunner{started: make(chan struct{})}
	service := testService(fake, runner, time.Now().UTC())
	service.Clock = realClock{}
	service.Config.LeaseDuration = 1200 * time.Millisecond
	service.Config.RunTimeout = 4 * time.Second
	done := make(chan error, 1)
	go func() {
		_, err := service.RunOnce(context.Background())
		done <- err
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start the runner")
	}
	postStarted := make(chan struct{}, 1)
	postRelease := make(chan struct{})
	fake.mu.Lock()
	fake.renewalPostStarted = postStarted
	fake.renewalPostRelease = postRelease
	fake.mu.Unlock()
	select {
	case <-postStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not durably write its first renewal")
	}
	persistedExpiry := fake.record("card-1").Run.LeaseExpires
	getStarted := make(chan struct{}, 1)
	getRelease := make(chan struct{})
	fake.mu.Lock()
	fake.renewalStarted = getStarted
	fake.renewalRelease = getRelease
	fake.mu.Unlock()
	// The server has committed the expiry above but delays its successful
	// response. A response-time based deadline wrongly grants this runner the
	// delay as additional lease time.
	time.Sleep(400 * time.Millisecond)
	close(postRelease)
	deadline := time.NewTimer(maxDuration(time.Until(persistedExpiry.Add(150*time.Millisecond)), time.Millisecond))
	defer deadline.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-deadline.C:
		close(getRelease)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatal("runner outlived the expiry durably written by a slow renewal")
	}
	close(getRelease)
}

func TestRenewReturnsPersistedExpiryAfterConflictRevalidation(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	record := testRecord(StateRunning)
	fence, err := claimFence(record)
	if err != nil {
		t.Fatal(err)
	}
	claim := &Run{ID: "run-1", OwnerToken: "owner-1", Fence: fence, StartedAt: now, HeartbeatAt: now, LeaseExpires: now.Add(time.Minute)}
	record.Run = claim
	fake.add("card-1", record)
	fake.metadataConflicts = 1
	service := testService(fake, &countedRunner{}, now)
	persistedExpiry, err := service.renew(context.Background(), "card-1", claim)
	if err != nil {
		t.Fatal(err)
	}
	if got := fake.record("card-1").Run.LeaseExpires; !persistedExpiry.Equal(got) {
		t.Fatalf("renewal returned %s, durable expiry is %s", persistedExpiry, got)
	}
}

func maxDuration(left, floor time.Duration) time.Duration {
	if left < floor {
		return floor
	}
	return left
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

func TestAnsweredDecisionFenceChangeCancelsRunner(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	record := testRecord(StateReady)
	record.Decision = &Decision{ID: "decision-1", Prompt: "when?", Value: json.RawMessage(`"morning"`)}
	fake.add("card-1", record)
	runner := blockingRunner{started: make(chan struct{})}
	service := testService(fake, runner, now)
	ticks := make(chan time.Time, 1)
	service.NewLeaseTicker = func(time.Duration) LeaseTicker { return manualLeaseTicker{ch: ticks} }
	done := make(chan error, 1)
	go func() { _, err := service.RunOnce(context.Background()); done <- err }()
	<-runner.started
	changed := fake.record("card-1")
	changed.Decision.Value = json.RawMessage(`"afternoon"`)
	fake.setRecord("card-1", changed)
	ticks <- now
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := fake.record("card-1")
	if got.State != StateRunning || got.Decision == nil || string(got.Decision.Value) != `"afternoon"` {
		t.Fatalf("stale runner applied a result after decision changed: %#v", got)
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
		if record.Journal == nil || record.Journal.State != "posted" || record.Journal.CommentID != "comment-1" || record.Notice == nil || record.Notice.State != "delivered" || record.Notice.ReceiptID != "notice-receipt-1" {
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
	t.Run("comment without receipt ID remains uncertain", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.commentNoID = true
		fake.add("card-1", testRecord(StateReady))
		if _, err := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted, Summary: "done"}}, now).RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := fake.record("card-1").Journal; got.State == "posted" || got.CommentID != "" {
			t.Fatalf("fabricated comment receipt: %#v", got)
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
	t.Run("notifier receipt must identify the same notice", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.add("card-1", testRecord(StateReady))
		service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted, Summary: "done"}}, now)
		service.Config.Notifier = wrongNoticeNotifier{}
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := fake.record("card-1").Notice; got.State == "delivered" {
			t.Fatalf("accepted receipt for another notice: %#v", got)
		}
	})
	t.Run("unresolved notice survives a later step", func(t *testing.T) {
		fake := newMetadataServer(t)
		fake.add("card-1", testRecord(StateReady))
		blocking := blockingNotifier{started: make(chan struct{})}
		first := testService(fake, &countedRunner{result: AdapterResult{Status: ResultWaitingEvent, EventID: "event-1"}}, now)
		first.Config.Notifier = blocking
		first.Config.NoticeTimeout = 10 * time.Millisecond
		if _, err := first.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-blocking.started
		original := fake.record("card-1").Notice.ID
		if got := fake.record("card-1").Notice.State; got != "unknown" {
			t.Fatalf("first notice state = %s", got)
		}
		if changed, err := first.Wake(context.Background(), "card-1", "event-1"); err != nil || !changed {
			t.Fatalf("wake = %v, %v", changed, err)
		}
		if _, err := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted}}, now).RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, notice := range fake.record("card-1").UnresolvedNotices {
			found = found || (notice.ID == original && notice.State == "unknown")
		}
		if !found {
			t.Fatalf("later step erased unresolved notice %q: %#v", original, fake.record("card-1"))
		}
	})
}

func TestQueuedNoticeReceiptIsNotDeliveryAndIsRetained(t *testing.T) {
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake := newMetadataServer(t)
	fake.add("card-1", testRecord(StateReady))
	notifier := &receiptNotifier{receipt: NoticeReceipt{ReceiptID: "queue-1", DeliveryState: "queued"}}
	service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultWaitingUser, DecisionPrompt: "approve?"}}, now)
	service.Config.Notifier = notifier
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := fake.record("card-1")
	if record.Notice == nil || record.Notice.State != "queued" || record.Notice.QueueReceiptID != "queue-1" || record.Notice.ReceiptID != "" {
		t.Fatalf("queued notice fabricated delivery receipt: %#v", record.Notice)
	}
	packets := notifier.Packets()
	if len(packets) != 1 || packets[0].DecisionID != record.Decision.ID {
		t.Fatalf("waiting-user notice packet lost exact decision identity: %#v / %#v", packets, record.Decision)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notifier.Calls() != 1 {
		t.Fatalf("queued notice was resent on later poll: %d calls", notifier.Calls())
	}
	if changed, err := service.Decide(context.Background(), "card-1", record.Decision.ID, json.RawMessage(`true`)); err != nil || !changed {
		t.Fatalf("decision = %v, %v", changed, err)
	}
	if _, err := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted}}, now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, notice := range fake.record("card-1").UnresolvedNotices {
		found = found || (notice.ID == record.Notice.ID && notice.State == "queued" && notice.QueueReceiptID == "queue-1" && notice.ReceiptID == "")
	}
	if !found {
		t.Fatalf("later task step lost queued notice: %#v", fake.record("card-1").UnresolvedNotices)
	}
}

func TestLateNoticeUpdatesPreserveVerifiedDeliveryEvidence(t *testing.T) {
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name          string
		receipt       NoticeReceipt
		notifierErr   error
		queueReceipt  string
		wantConflict  bool
		interleaveCAS bool
	}{
		{
			name:          "queued success loses CAS to exact delivery acknowledgement",
			receipt:       NoticeReceipt{ReceiptID: "queue-1", DeliveryState: NoticeQueued},
			queueReceipt:  "queue-1",
			interleaveCAS: true,
		},
		{
			name:         "conflicting queued success is held",
			receipt:      NoticeReceipt{ReceiptID: "queue-late", DeliveryState: NoticeQueued},
			queueReceipt: "queue-verified",
			wantConflict: true,
		},
		{
			name:         "conflicting delivered success is held",
			receipt:      NoticeReceipt{ReceiptID: "delivery-late", DeliveryState: NoticeDelivered},
			queueReceipt: "queue-verified",
			wantConflict: true,
		},
		{
			name:          "adapter error cannot erase delivery acknowledgement",
			notifierErr:   errors.New("relay response lost"),
			queueReceipt:  "queue-verified",
			interleaveCAS: true,
		},
		{
			name:          "malformed adapter receipt cannot erase delivery acknowledgement",
			receipt:       NoticeReceipt{ReceiptID: "", DeliveryState: NoticeQueued},
			queueReceipt:  "queue-verified",
			interleaveCAS: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newMetadataServer(t)
			fake.add("card-1", testRecord(StateReady))
			notifier := gatedNoticeNotifier{
				started: make(chan struct{}),
				release: make(chan struct{}),
				receipt: tc.receipt,
				err:     tc.notifierErr,
			}
			service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted}}, now)
			service.Config.Notifier = notifier
			done := make(chan error, 1)
			go func() {
				_, err := service.RunOnce(context.Background())
				done <- err
			}()
			awaitTestSignal(t, notifier.started, "notifier start")

			if tc.interleaveCAS {
				fake.mu.Lock()
				postStarted := make(chan struct{}, 1)
				postRelease := make(chan struct{})
				fake.metadataPostStarted = postStarted
				fake.metadataPostRelease = postRelease
				fake.mu.Unlock()
				close(notifier.release)
				awaitTestSignal(t, postStarted, "stale notice CAS write")
				if changed, err := service.AcknowledgeNotice(context.Background(), "card-1", fake.record("card-1").Notice.ID, tc.queueReceipt, "displayed-1"); err != nil || !changed {
					t.Fatalf("delivery acknowledgement = %v, %v", changed, err)
				}
				close(postRelease)
			} else {
				if changed, err := service.AcknowledgeNotice(context.Background(), "card-1", fake.record("card-1").Notice.ID, tc.queueReceipt, "displayed-1"); err != nil || !changed {
					t.Fatalf("delivery acknowledgement = %v, %v", changed, err)
				}
				close(notifier.release)
			}

			err := awaitTestError(t, done, "worker notice completion")
			if tc.wantConflict && !errors.Is(err, ErrNoticeReceiptConflict) {
				t.Fatalf("late receipt conflict = %v, want ErrNoticeReceiptConflict", err)
			}
			if !tc.wantConflict && err != nil {
				t.Fatal(err)
			}
			notice := fake.record("card-1").Notice
			if notice.State != "delivered" || notice.QueueReceiptID != tc.queueReceipt || notice.ReceiptID != "displayed-1" {
				t.Fatalf("late notifier outcome replaced verified delivery evidence: %#v", notice)
			}
		})
	}
}

func TestLateRetainedNoticeAlsoPersistsItsExactJournalReceipt(t *testing.T) {
	fake := newMetadataServer(t)
	record := testRecord(StateCompleted)
	record.Outcome = &Outcome{RunID: "run-new", Kind: string(ResultCompleted)}
	record.Journal = &Journal{ID: "journal-run-new", State: "posted", CommentID: "comment-new"}
	record.Notice = &Notice{ID: "notice-run-new", State: "delivered", ReceiptID: "delivery-new"}
	record.UnresolvedJournals = []Journal{{ID: "journal-run-old", State: "sending"}}
	record.UnresolvedNotices = []Notice{{ID: "notice-run-old", State: "prepared"}}
	fake.add("card-1", record)
	fake.mu.Lock()
	fake.metadataConflicts = 1
	fake.mu.Unlock()
	service := testService(fake, nil, time.Now().UTC())

	if err := service.markJournal(context.Background(), "card-1", "run-old", "notice-run-old", "posted", "comment-old", noticeUpdate{State: "prepared"}); err != nil {
		t.Fatal(err)
	}
	updated := fake.record("card-1")
	if journal := updated.UnresolvedJournals[0]; journal.State != "posted" || journal.CommentID != "comment-old" {
		t.Fatalf("late retained journal receipt was lost: %#v", journal)
	}
	if current := updated.Journal; current.ID != "journal-run-new" || current.CommentID != "comment-new" {
		t.Fatalf("late retained update changed current journal: %#v", current)
	}
}

func TestCustomNotifierReceiptIsValidatedBeforeDelivery(t *testing.T) {
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, receipt := range []NoticeReceipt{
		{ReceiptID: "receipt-1", DeliveryState: "eventually"},
		{ReceiptID: ""},
		{ReceiptID: strings.Repeat("r", maxStableIDSize+1)},
	} {
		fake := newMetadataServer(t)
		fake.add("card-1", testRecord(StateReady))
		service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultCompleted}}, now)
		service.Config.Notifier = &receiptNotifier{receipt: receipt}
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := fake.record("card-1").Notice; got.State != "unknown" || got.ReceiptID != "" || got.QueueReceiptID != "" {
			t.Fatalf("invalid custom notifier receipt recorded as delivery: %#v", got)
		}
	}
}

func TestAcknowledgeNoticeDeliveryMatchesExactCurrentOrRetainedNotice(t *testing.T) {
	fake := newMetadataServer(t)
	record := testRecord(StateCompleted)
	record.Notice = &Notice{ID: "notice-current", State: "queued", QueueReceiptID: "queue-current"}
	record.UnresolvedNotices = []Notice{{ID: "notice-old", State: "queued", QueueReceiptID: "queue-old"}}
	fake.add("card-1", record)
	fake.mu.Lock()
	fake.metadata["card-1"]["keep"] = json.RawMessage(`{"large":9007199254740993,"nested":[true,null]}`)
	keep := append([]byte(nil), fake.metadata["card-1"]["keep"]...)
	fake.metadataConflicts = 1
	fake.mu.Unlock()
	service := testService(fake, nil, time.Now().UTC())

	changed, err := service.AcknowledgeNotice(context.Background(), "card-1", "notice-old", "queue-old", "delivery-old")
	if err != nil || !changed {
		t.Fatalf("retained acknowledgement = %v, %v", changed, err)
	}
	updated := fake.record("card-1")
	if updated.Notice.ID != "notice-current" || updated.Notice.State != "queued" {
		t.Fatalf("retained acknowledgement changed current notice: %#v", updated.Notice)
	}
	old := updated.UnresolvedNotices[0]
	if old.State != "delivered" || old.QueueReceiptID != "queue-old" || old.ReceiptID != "delivery-old" {
		t.Fatalf("retained acknowledgement = %#v", old)
	}
	fake.mu.Lock()
	if string(fake.metadata["card-1"]["keep"]) != string(keep) {
		fake.mu.Unlock()
		t.Fatalf("unrelated JSON changed: %s", fake.metadata["card-1"]["keep"])
	}
	fake.mu.Unlock()
	if changed, err = service.AcknowledgeNotice(context.Background(), "card-1", "notice-old", "queue-old", "delivery-old"); err != nil || changed {
		t.Fatalf("exact repeated acknowledgement = %v, %v", changed, err)
	}
	if _, err := service.AcknowledgeNotice(context.Background(), "card-1", "notice-old", "queue-old", "different-delivery"); err == nil {
		t.Fatal("different delivered receipt rewrote a durable acknowledgement")
	}
	if _, err := service.AcknowledgeNotice(context.Background(), "card-1", "notice-current", "wrong-queue", "delivery-current"); err == nil {
		t.Fatal("mismatched queue receipt was accepted")
	}
	changed, err = service.AcknowledgeNotice(context.Background(), "card-1", "notice-current", "queue-current", "delivery-current")
	if err != nil || !changed {
		t.Fatalf("current acknowledgement = %v, %v", changed, err)
	}
	updated = fake.record("card-1")
	if updated.Notice.State != "delivered" || updated.Notice.QueueReceiptID != "queue-current" || updated.Notice.ReceiptID != "delivery-current" {
		t.Fatalf("current acknowledgement = %#v", updated.Notice)
	}
	updated.Notice = &Notice{ID: "notice-uncertain", State: "unknown"}
	fake.setRecord("card-1", updated)
	changed, err = service.AcknowledgeNotice(context.Background(), "card-1", "notice-uncertain", "queue-reconciled", "delivery-reconciled")
	if err != nil || !changed {
		t.Fatalf("explicit unknown reconciliation = %v, %v", changed, err)
	}
	updated = fake.record("card-1")
	if updated.Notice.State != "delivered" || updated.Notice.QueueReceiptID != "queue-reconciled" || updated.Notice.ReceiptID != "delivery-reconciled" {
		t.Fatalf("unknown reconciliation = %#v", updated.Notice)
	}
	if _, err := service.AcknowledgeNotice(context.Background(), "card-1", "stale-notice", "queue-stale", "delivery-stale"); err == nil {
		t.Fatal("stale notice ID was accepted")
	}
	for _, state := range []string{"prepared", "not_configured"} {
		updated.Notice = &Notice{ID: "notice-" + state, State: state}
		fake.setRecord("card-1", updated)
		if _, err := service.AcknowledgeNotice(context.Background(), "card-1", updated.Notice.ID, "queue-external", "delivery-external"); err == nil {
			t.Fatalf("%s notice accepted an operator delivery receipt", state)
		}
	}
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

func TestScheduledPassRunsConfiguredObserverWithoutDelegatingSuggestion(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("registry", newSuggestionRegistryRecord())
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted}}
	observer := &countedObserver{events: []Suggestion{{SourceID: "source-1", Title: "proposal", Proposal: "fixture"}}}
	service := testService(fake, runner, now)
	service.Config.Observer = observer
	service.Config.ObserverRegistryCardID = "registry"
	service.Config.ObserverListID = "list-1"
	service.Config.ObserverTimeout = time.Minute
	if err := service.runScheduledPass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if observer.Calls() != 1 || runner.Calls() != 0 {
		t.Fatalf("observer/runner calls = %d/%d", observer.Calls(), runner.Calls())
	}
	fake.mu.Lock()
	created := fake.created
	fake.mu.Unlock()
	if created != 1 {
		t.Fatalf("observer created %d suggestions", created)
	}
	if got := fake.record("suggestion-1"); got.State != StateSuggested || got.Delegation.Delegated {
		t.Fatalf("observer proposal was delegated: %#v", got)
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

func TestDecisionMustMatchItsCurrentQuestion(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("user-card", testRecord(StateWaitingUser))
	service := testService(fake, &countedRunner{}, now)
	if changed, err := service.Decide(context.Background(), "user-card", "unrelated-question", json.RawMessage(`true`)); err == nil || changed {
		t.Fatalf("unrelated decision = %v, %v", changed, err)
	}
	if got := fake.record("user-card"); got.State != StateWaitingUser || got.Decision.ID != "decision-1" {
		t.Fatalf("unrelated decision changed record: %#v", got)
	}
}

func TestWaitingUserQuestionsGetDistinctStableIDs(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("user-card", testRecord(StateReady))
	service := testService(fake, &countedRunner{result: AdapterResult{Status: ResultWaitingUser, DecisionPrompt: "confirm?"}}, now)
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := fake.record("user-card").Decision.ID
	if first == "" {
		t.Fatal("first waiting question has no stable ID")
	}
	if changed, err := service.Decide(context.Background(), "user-card", first, json.RawMessage(`true`)); err != nil || !changed {
		t.Fatalf("first decision = %v, %v", changed, err)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := fake.record("user-card").Decision.ID
	if second == "" || second == first {
		t.Fatalf("question IDs = %q, %q", first, second)
	}
	if changed, err := service.Decide(context.Background(), "user-card", first, json.RawMessage(`true`)); err != nil || changed {
		t.Fatalf("old decision replay = %v, %v", changed, err)
	}
	if got := fake.record("user-card").State; got != StateWaitingUser {
		t.Fatalf("old decision resumed new question: %s", got)
	}
}

func TestEventReceiptCapacityNeverEvictsOldEvidence(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	record := testRecord(StateWaitingEvent)
	record.EventID = "new-event"
	record.EventReceipts = []string{"old-event"}
	for i := 1; i < maxReceiptIDs; i++ {
		record.EventReceipts = append(record.EventReceipts, fmt.Sprintf("event-%d", i))
	}
	fake.add("event-card", record)
	service := testService(fake, &countedRunner{}, now)
	if changed, err := service.Wake(context.Background(), "event-card", "new-event"); err == nil || changed {
		t.Fatalf("receipt capacity = %v, %v", changed, err)
	}
	if got := fake.record("event-card"); !got.HasEventReceipt("old-event") || got.State != StateWaitingEvent {
		t.Fatalf("receipt capacity evicted evidence or woke work: %#v", got)
	}
}

func TestEnrollExplicitlyPromotesSuggestedRecord(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	suggested := testRecord(StateReady)
	suggested.State = StateSuggested
	suggested.Delegation = Delegation{}
	fake.add("suggestion-card", suggested)
	service := testService(fake, &countedRunner{}, now)
	if err := service.Enroll(context.Background(), "suggestion-card", Enrollment{Goal: "explicit goal", CompletionCriteria: "receipt", Authorization: json.RawMessage(`{"scope":"fixture"}`)}); err != nil {
		t.Fatal(err)
	}
	got := fake.record("suggestion-card")
	if got.State != StateReady || !got.Delegation.Delegated || got.Goal != "explicit goal" {
		t.Fatalf("suggestion was not explicitly promoted: %#v", got)
	}
}

func TestPreparedJournalRecoversAfterCommittedWriteResponseIsLost(t *testing.T) {
	fake := newMetadataServer(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	fake.add("card-1", testRecord(StateReady))
	fake.dropCompletedResponse = true
	runner := &countedRunner{result: AdapterResult{Status: ResultCompleted, Summary: "done"}}
	service := testService(fake, runner, now)
	if _, err := service.RunOnce(context.Background()); err == nil {
		t.Fatal("lost response after a committed outcome must surface uncertainty")
	}
	if got := fake.record("card-1"); got.State != StateCompleted || got.Journal == nil || got.Journal.State != "prepared" {
		t.Fatalf("committed outbox state = %#v", got)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.Calls() != 1 {
		t.Fatalf("prepared journal recovery reran adapter %d times", runner.Calls())
	}
	fake.mu.Lock()
	comments := fake.comments
	fake.mu.Unlock()
	if comments != 1 {
		t.Fatalf("prepared journal recovery comments = %d", comments)
	}
	if got := fake.record("card-1").Journal; got.State != "posted" || got.CommentID != "comment-1" {
		t.Fatalf("recovered journal = %#v", got)
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
