package worker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrLostOwnership = errors.New("worker lost its durable run ownership")

type Config struct {
	BoardID       string
	WorkerID      string
	PollInterval  time.Duration
	LeaseDuration time.Duration
	RunTimeout    time.Duration
	MaxConcurrent int
	ArtifactDir   string
	OutputLimit   int
	PacketLimit   int
	NoticeTimeout time.Duration
	Runner        Runner
	Notifier      Notifier
	// Observer is optional and is run only by Serve's shared board-wide pass.
	// Its proposals remain non-delegated and are never passed to Runner.
	Observer               Observer
	ObserverRegistryCardID string
	ObserverListID         string
	ObserverTimeout        time.Duration
}

func (c Config) ValidateBase() error {
	if err := c.ValidateReadOnly(); err != nil {
		return err
	}
	if strings.TrimSpace(c.WorkerID) == "" {
		return errors.New("worker identity is required")
	}
	if c.PollInterval <= 0 || c.LeaseDuration <= 0 || c.RunTimeout <= 0 {
		return errors.New("worker poll interval, lease duration, and timeout must be positive")
	}
	if c.LeaseDuration <= c.RunTimeout/20 {
		return errors.New("worker lease duration must leave time for heartbeats")
	}
	if c.MaxConcurrent <= 0 {
		return errors.New("worker max concurrency must be positive")
	}
	if strings.TrimSpace(c.ArtifactDir) == "" {
		return errors.New("worker artifact directory is required")
	}
	if c.OutputLimit <= 0 {
		return errors.New("worker output limit must be positive")
	}
	if c.PacketLimit <= 0 {
		return errors.New("worker packet limit must be positive")
	}
	if c.NoticeTimeout <= 0 {
		return errors.New("worker notice timeout must be positive")
	}
	return nil
}

func (c Config) ValidateReadOnly() error {
	if strings.TrimSpace(c.BoardID) == "" {
		return errors.New("worker board ID is required")
	}
	return nil
}

func (c Config) ValidateExecution() error {
	if err := c.ValidateBase(); err != nil {
		return err
	}
	if c.Runner == nil {
		return errors.New("worker runner configuration is required")
	}
	return nil
}

func (c Config) ValidateScheduledObserver() error {
	if c.Observer == nil {
		return nil
	}
	if strings.TrimSpace(c.ObserverRegistryCardID) == "" || strings.TrimSpace(c.ObserverListID) == "" || c.ObserverTimeout <= 0 {
		return errors.New("configured observer requires registry card, list ID, and positive timeout")
	}
	return nil
}

type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	Store  Store
	Config Config
	Clock  Clock
	// NewLeaseTicker is injectable so fencing/cancellation tests do not need
	// wall-clock sleeps. Production leaves it nil and uses time.NewTicker.
	NewLeaseTicker func(time.Duration) LeaseTicker
}

type LeaseTicker interface {
	C() <-chan time.Time
	Stop()
}

type realLeaseTicker struct{ ticker *time.Ticker }

func (t realLeaseTicker) C() <-chan time.Time { return t.ticker.C }
func (t realLeaseTicker) Stop()               { t.ticker.Stop() }

func (s Service) leaseTicker(interval time.Duration) LeaseTicker {
	if s.NewLeaseTicker != nil {
		return s.NewLeaseTicker(interval)
	}
	return realLeaseTicker{ticker: time.NewTicker(interval)}
}

type Report struct {
	Scanned     int `json:"scanned"`
	Recognized  int `json:"recognized"`
	Due         int `json:"due"`
	Claimed     int `json:"claimed"`
	Processed   int `json:"processed"`
	NeedsReview int `json:"needs_review"`
	Ignored     int `json:"ignored"`
}

type StatusItem struct {
	CardID string     `json:"card_id"`
	State  State      `json:"state"`
	WakeAt *time.Time `json:"wake_at,omitempty"`
}

type CheckReport struct {
	Report
	Tasks []StatusItem `json:"tasks"`
}

func (s Service) clock() Clock {
	if s.Clock != nil {
		return s.Clock
	}
	return realClock{}
}

func (s Service) Check(ctx context.Context) (CheckReport, error) {
	if err := s.Config.ValidateReadOnly(); err != nil {
		return CheckReport{}, err
	}
	snapshots, err := s.Store.Scan(ctx, s.Config.BoardID)
	if err != nil {
		return CheckReport{}, err
	}
	report := CheckReport{Report: Report{Scanned: len(snapshots)}}
	now := s.clock().Now()
	for _, snapshot := range snapshots {
		if !snapshot.HasRecord || snapshot.ParseErr != nil {
			report.Ignored++
			continue
		}
		report.Recognized++
		report.Tasks = append(report.Tasks, StatusItem{CardID: snapshot.Card.ID, State: snapshot.Record.State, WakeAt: snapshot.Record.WakeAt})
		if snapshot.Record.Eligible(now) {
			report.Due++
		}
	}
	return report, nil
}

func (s Service) RunOnce(ctx context.Context) (Report, error) {
	if err := s.Config.ValidateExecution(); err != nil {
		return Report{}, err
	}
	snapshots, err := s.Store.Scan(ctx, s.Config.BoardID)
	if err != nil {
		return Report{}, err
	}
	report := Report{Scanned: len(snapshots)}
	now := s.clock().Now()
	var mu sync.Mutex
	var passErr error
	jobs := make(chan Snapshot)
	var workers sync.WaitGroup
	for i := 0; i < s.Config.MaxConcurrent; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for snapshot := range jobs {
				claimed, review, err := s.processDue(ctx, snapshot)
				if err != nil {
					mu.Lock()
					if passErr == nil {
						passErr = err
					}
					mu.Unlock()
					continue
				}
				mu.Lock()
				if claimed {
					report.Claimed++
				}
				if review {
					report.NeedsReview++
				} else if claimed {
					report.Processed++
				}
				mu.Unlock()
			}
		}()
	}
dispatch:
	for _, snapshot := range snapshots {
		if !snapshot.HasRecord || snapshot.ParseErr != nil {
			mu.Lock()
			report.Ignored++
			mu.Unlock()
			continue
		}
		mu.Lock()
		report.Recognized++
		mu.Unlock()
		if outcomeNeedsRecovery(snapshot.Record) {
			if err := s.recoverOutcome(ctx, snapshot); err != nil {
				mu.Lock()
				if passErr == nil {
					passErr = err
				}
				mu.Unlock()
			}
			continue
		}
		if snapshot.Record.State == StateRunning && snapshot.Record.Run != nil && !snapshot.Record.Run.LeaseExpires.After(now) {
			if err := s.reconcileExpired(ctx, snapshot); err == nil {
				mu.Lock()
				report.NeedsReview++
				mu.Unlock()
			} else if errors.Is(err, ErrLostOwnership) {
				// A current archive, cancellation, revocation, or renewed lease won
				// the race. Do not overwrite it with an old recovery transition.
				continue
			} else {
				mu.Lock()
				if passErr == nil {
					passErr = err
				}
				mu.Unlock()
			}
			continue
		}
		if !snapshot.Record.Eligible(now) {
			continue
		}
		mu.Lock()
		report.Due++
		mu.Unlock()
		select {
		case jobs <- snapshot:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	return report, passErr
}

func outcomeNeedsRecovery(record Record) bool {
	if record.Outcome == nil || record.Journal == nil {
		return false
	}
	return record.Journal.State == "prepared" || (record.Journal.State == "posted" && record.Notice != nil && record.Notice.State == "prepared")
}

func (s Service) Serve(ctx context.Context) error {
	if err := s.Config.ValidateExecution(); err != nil {
		return err
	}
	if err := s.Config.ValidateScheduledObserver(); err != nil {
		return err
	}
	// The first pass avoids a restart leaving already-due work idle until the
	// next ticker. Every later pass is still a single board-wide schedule.
	if err := s.runScheduledPass(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	ticker := time.NewTicker(s.Config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.runScheduledPass(ctx); err != nil && ctx.Err() == nil {
				return err
			}
		}
	}
}

func (s Service) runScheduledPass(ctx context.Context) error {
	if err := s.Config.ValidateScheduledObserver(); err != nil {
		return err
	}
	if _, err := s.RunOnce(ctx); err != nil {
		return err
	}
	if s.Config.Observer == nil {
		return nil
	}
	observerCtx, cancel := context.WithTimeout(ctx, s.Config.ObserverTimeout)
	defer cancel()
	events, err := s.Config.Observer.Observe(observerCtx)
	if err != nil {
		return err
	}
	_, err = s.IngestSuggestions(observerCtx, s.Config.ObserverRegistryCardID, s.Config.ObserverListID, events)
	return err
}

func (s Service) tryClaim(ctx context.Context, cardID string) (Snapshot, bool, error) {
	snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil {
		return Snapshot{}, false, err
	}
	if !snapshot.HasRecord || snapshot.ParseErr != nil || !snapshot.Record.Eligible(s.clock().Now()) {
		return snapshot, false, nil
	}
	runID, err := uniqueID("run")
	if err != nil {
		return snapshot, false, err
	}
	owner, err := uniqueID(s.Config.WorkerID)
	if err != nil {
		return snapshot, false, err
	}
	now := s.clock().Now()
	snapshot.Record.State = StateRunning
	fence, err := claimFence(snapshot.Record)
	if err != nil {
		return snapshot, false, err
	}
	snapshot.Record.Run = &Run{ID: runID, OwnerToken: owner, Fence: fence, StartedAt: now, HeartbeatAt: now, LeaseExpires: now.Add(s.Config.LeaseDuration)}
	claimed, err := s.Store.Put(ctx, snapshot, snapshot.Record)
	if errors.Is(err, ErrConflict) {
		// A stale claim is intentionally not refreshed and retried.
		return snapshot, false, nil
	}
	return claimed, err == nil, err
}

func (s Service) processDue(ctx context.Context, stale Snapshot) (bool, bool, error) {
	claimed, ok, err := s.tryClaim(ctx, stale.Card.ID)
	if err != nil || !ok {
		return false, false, err
	}
	cardMarkdown, err := s.Store.Client.GetCardMarkdown(ctx, claimed.Card.ID)
	if err != nil {
		return true, true, s.finishReview(ctx, claimed.Card.ID, claimed.Record.Run, "cannot load latest card context: "+err.Error())
	}
	// Markdown retrieval may take long enough for an operator to archive,
	// revoke, cancel, or narrow a task. Re-read the claim immediately before
	// the adapter is allowed to start; an old claim is never an execution
	// permission by itself.
	current, err := s.Store.Live(ctx, s.Config.BoardID, claimed.Card.ID)
	if err != nil {
		return true, true, err
	}
	if !ownsLive(current.Record, claimed.Record.Run, s.clock().Now()) {
		return false, false, nil
	}
	packet := Packet{
		Version:            SchemaVersion,
		CardID:             current.Card.ID,
		BoardID:            s.Config.BoardID,
		RunID:              current.Record.Run.ID,
		Goal:               current.Record.Goal,
		CompletionCriteria: current.Record.CompletionCriteria,
		Authorization:      current.Record.Delegation.Authorization,
		CardMarkdown:       cardMarkdown,
		Sources:            current.Record.Sources,
		EventReceipts:      current.Record.EventReceipts,
		Decision:           current.Record.Decision,
		Action:             current.Record.Action,
		AllowedResults:     []string{string(ResultCompleted), string(ResultScheduled), string(ResultWaitingEvent), string(ResultWaitingUser), string(ResultNeedsReview)},
	}
	if _, err := marshalBoundedPacket(packet, s.Config.PacketLimit); err != nil {
		return true, true, s.finishReview(ctx, current.Card.ID, current.Record.Run, err.Error())
	}
	result, err := s.runWithLease(ctx, current.Card.ID, current.Record.Run, packet)
	if err != nil {
		if errors.Is(err, ErrLostOwnership) {
			return false, false, nil
		}
		return true, true, s.finishReview(ctx, current.Card.ID, current.Record.Run, err.Error())
	}
	if err := s.finishResult(ctx, current.Card.ID, current.Record.Run, result); err != nil {
		if errors.Is(err, ErrLostOwnership) {
			return false, false, nil
		}
		return true, true, err
	}
	return true, result.Status == ResultNeedsReview, nil
}

func (s Service) runWithLease(ctx context.Context, cardID string, claim *Run, packet Packet) (AdapterResult, error) {
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, s.Config.RunTimeout)
	defer cancelTimeout()
	leaseDeadline := claim.LeaseExpires
	remainingLease := leaseDeadline.Sub(s.clock().Now())
	if remainingLease <= 0 {
		return AdapterResult{}, ErrLostOwnership
	}
	// A runner is never allowed to outlive the lease that was actually
	// committed. The timer cancels it independently of a blocked renewal HTTP
	// request; a successful renewal installs a new lease guard below.
	runCtx, cancelRun := context.WithCancel(timeoutCtx)
	defer cancelRun()
	leaseTimer := time.AfterFunc(remainingLease, cancelRun)
	defer func() { leaseTimer.Stop() }()
	resultCh := make(chan struct {
		result AdapterResult
		err    error
	}, 1)
	go func() {
		result, err := s.Config.Runner.Run(runCtx, packet)
		resultCh <- struct {
			result AdapterResult
			err    error
		}{result, err}
	}()
	ticker := s.leaseTicker(leaseRenewInterval(s.Config.LeaseDuration, remainingLease))
	defer func() { ticker.Stop() }()
	for {
		select {
		case finished := <-resultCh:
			return finished.result, finished.err
		case <-runCtx.Done():
			// Waiting for a context-aware runner here guarantees a lost/expired
			// lease also stops its process group before this method returns.
			<-resultCh
			if timeoutCtx.Err() != nil {
				return AdapterResult{}, timeoutCtx.Err()
			}
			return AdapterResult{}, ErrLostOwnership
		case <-ticker.C():
			actualRemaining := leaseDeadline.Sub(s.clock().Now())
			if actualRemaining <= 0 {
				cancelRun()
				<-resultCh
				return AdapterResult{}, ErrLostOwnership
			}
			renewCtx, renewCancel := context.WithTimeout(timeoutCtx, actualRemaining)
			renewedExpiry, err := s.renew(renewCtx, cardID, claim)
			renewCancel()
			if err != nil {
				cancelRun()
				<-resultCh
				if !leaseDeadline.After(s.clock().Now()) || runCtx.Err() != nil && timeoutCtx.Err() == nil {
					return AdapterResult{}, ErrLostOwnership
				}
				return AdapterResult{}, err
			}
			// The renewal's expiry was chosen before its metadata POST. Its
			// response may arrive much later, so the remaining guard must be
			// measured from the expiry that was actually written—not from now.
			leaseDeadline = renewedExpiry
			remainingLease = leaseDeadline.Sub(s.clock().Now())
			if remainingLease <= 0 || runCtx.Err() != nil {
				cancelRun()
				<-resultCh
				return AdapterResult{}, ErrLostOwnership
			}
			if !leaseTimer.Stop() {
				cancelRun()
				<-resultCh
				return AdapterResult{}, ErrLostOwnership
			}
			leaseTimer = time.AfterFunc(remainingLease, cancelRun)
			ticker.Stop()
			ticker = s.leaseTicker(leaseRenewInterval(s.Config.LeaseDuration, remainingLease))
		}
	}
}

func leaseRenewInterval(leaseDuration, remainingLease time.Duration) time.Duration {
	interval := leaseDuration / 3
	if halfRemaining := remainingLease / 2; halfRemaining > 0 && halfRemaining < interval {
		interval = halfRemaining
	}
	if interval <= 0 {
		return time.Nanosecond
	}
	return interval
}

// renew returns the exact lease expiry persisted by its successful metadata
// write. Callers must treat it as a durable deadline; response arrival is not
// a lease extension.
func (s Service) renew(ctx context.Context, cardID string, claim *Run) (time.Time, error) {
	snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil {
		return time.Time{}, ErrLostOwnership
	}
	if !ownsLive(snapshot.Record, claim, s.clock().Now()) {
		return time.Time{}, ErrLostOwnership
	}
	now := s.clock().Now()
	snapshot.Record.Run.HeartbeatAt = now
	snapshot.Record.Run.LeaseExpires = now.Add(s.Config.LeaseDuration)
	if written, err := s.Store.Put(ctx, snapshot, snapshot.Record); err == nil {
		return written.Record.Run.LeaseExpires, nil
	} else if !errors.Is(err, ErrConflict) {
		return time.Time{}, err
	}
	// This is a deliberately revalidated renewal, not a blind stale retry.
	snapshot, err = s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil || !ownsLive(snapshot.Record, claim, s.clock().Now()) {
		return time.Time{}, ErrLostOwnership
	}
	now = s.clock().Now()
	snapshot.Record.Run.HeartbeatAt = now
	snapshot.Record.Run.LeaseExpires = now.Add(s.Config.LeaseDuration)
	written, err := s.Store.Put(ctx, snapshot, snapshot.Record)
	if errors.Is(err, ErrConflict) {
		return time.Time{}, ErrLostOwnership
	}
	if err != nil {
		return time.Time{}, err
	}
	return written.Record.Run.LeaseExpires, nil
}

func (s Service) finishResult(ctx context.Context, cardID string, claim *Run, result AdapterResult) error {
	snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil {
		return ErrLostOwnership
	}
	if !ownsLive(snapshot.Record, claim, s.clock().Now()) {
		return ErrLostOwnership
	}
	if result.RunID != claim.ID {
		return s.finishReview(ctx, cardID, claim, fmt.Sprintf("adapter result run_id %q does not match active run", result.RunID))
	}
	if err := result.Validate(snapshot.Record.Action, s.clock().Now()); err != nil {
		return s.finishReview(ctx, cardID, claim, err.Error())
	}
	record := snapshot.Record
	record.Run = nil
	record.Outcome = &Outcome{RunID: claim.ID, Kind: string(result.Status), Summary: result.Summary, ReceiptID: result.ReceiptID, ReviewNote: result.ReviewNote}
	if result.ActionReceipt != nil {
		record.Action.Receipt = result.ActionReceipt
	}
	switch result.Status {
	case ResultCompleted:
		record.State = StateCompleted
		record.Decision = nil
		record.EventID = ""
	case ResultScheduled:
		record.State, record.WakeAt, record.Decision = StateScheduled, result.WakeAt, nil
		record.EventID = ""
	case ResultWaitingEvent:
		record.State, record.Decision = StateWaitingEvent, nil
		record.EventID = result.EventID
	case ResultWaitingUser:
		record.State, record.Decision = StateWaitingUser, &Decision{ID: "decision-" + claim.ID, Prompt: result.DecisionPrompt}
		record.EventID = ""
	case ResultNeedsReview:
		record.State = StateNeedsReview
	}
	return s.commitOutcome(ctx, snapshot, record, claim.ID)
}

func (s Service) finishReview(ctx context.Context, cardID string, claim *Run, reason string) error {
	snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil || !ownsLive(snapshot.Record, claim, s.clock().Now()) {
		return ErrLostOwnership
	}
	record := snapshot.Record
	record.State = StateNeedsReview
	record.Outcome = &Outcome{RunID: claim.ID, Kind: string(ResultNeedsReview), ReviewNote: truncate(reason, maxSummarySize)}
	record.Run = nil
	return s.commitOutcome(ctx, snapshot, record, claim.ID)
}

func (s Service) reconcileExpired(ctx context.Context, snapshot Snapshot) error {
	claim := snapshot.Record.Run
	if claim == nil {
		return nil
	}
	live, err := s.Store.Live(ctx, s.Config.BoardID, snapshot.Card.ID)
	if err != nil || !matchesClaim(live.Record, claim) || live.Record.Run.LeaseExpires.After(s.clock().Now()) {
		return ErrLostOwnership
	}
	// A running task may have produced an outward effect before its old worker
	// died. It is always held for human reconciliation instead of re-claimed.
	record := live.Record
	record.State = StateNeedsReview
	record.Outcome = &Outcome{RunID: claim.ID, Kind: string(ResultNeedsReview), ReviewNote: "run lease expired; reconcile receipts before any new action"}
	record.Run = nil
	return s.commitOutcome(ctx, live, record, claim.ID)
}

func (s Service) commitOutcome(ctx context.Context, snapshot Snapshot, record Record, runID string) error {
	record.RetainUnresolvedOutbox()
	record.Journal = &Journal{ID: "journal-" + runID, State: "prepared"}
	record.Notice = &Notice{ID: "notice-" + runID, State: "prepared"}
	written, err := s.Store.Put(ctx, snapshot, record)
	if err != nil {
		return err
	}
	return s.publishOutcome(ctx, written, runID)
}

// recoverOutcome drains only outbox stages known not to have sent an effect.
// A journal/notice marked sending or unknown may already have reached its
// target, so it is deliberately left for operator reconciliation rather than
// being replayed on a later worker pass.
func (s Service) recoverOutcome(ctx context.Context, stale Snapshot) error {
	live, err := s.Store.Live(ctx, s.Config.BoardID, stale.Card.ID)
	if err != nil {
		return err
	}
	if !live.HasRecord || live.ParseErr != nil || live.Record.Outcome == nil || live.Record.Journal == nil {
		return nil
	}
	switch live.Record.Journal.State {
	case "prepared":
		return s.publishOutcome(ctx, live, live.Record.Outcome.RunID)
	case "posted":
		return s.publishNotice(ctx, live.Card.ID, live.Record.Outcome.RunID, journalMessage(live.Record))
	default:
		return nil
	}
}

func (s Service) publishOutcome(ctx context.Context, snapshot Snapshot, runID string) error {
	claimed, ok, err := s.claimJournal(ctx, snapshot, runID)
	if err != nil {
		return err
	}
	if !ok {
		return s.publishNotice(ctx, snapshot.Card.ID, runID, journalMessage(snapshot.Record))
	}
	snapshot = claimed
	message := journalMessage(snapshot.Record)
	commentRaw, err := s.Store.Client.AddCommentOnce(ctx, snapshot.Card.ID, message)
	if err != nil {
		if markErr := s.markJournal(ctx, snapshot.Card.ID, runID, "unknown", "", "unknown", ""); markErr != nil {
			return fmt.Errorf("comment delivery and receipt persistence are both uncertain: %w", markErr)
		}
		return nil // The comment request itself is ambiguous and is never retried.
	}
	var comment struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(commentRaw, &comment)
	if comment.ID == "" {
		if markErr := s.markJournal(ctx, snapshot.Card.ID, runID, "unknown", "", "unknown", ""); markErr != nil {
			return fmt.Errorf("comment response had no receipt and uncertainty could not be recorded: %w", markErr)
		}
		return nil
	}
	if err := s.markJournal(ctx, snapshot.Card.ID, runID, "posted", comment.ID, "prepared", ""); err != nil {
		return fmt.Errorf("comment was posted but its durable receipt could not be recorded: %w", err)
	}
	return s.publishNotice(ctx, snapshot.Card.ID, runID, message)
}

// claimJournal fences the non-idempotent comment request. A committed
// terminal outcome with a prepared journal is safe to resume after a lost
// metadata response; once it is sending, a worker never blindly posts again.
func (s Service) claimJournal(ctx context.Context, snapshot Snapshot, runID string) (Snapshot, bool, error) {
	if snapshot.Record.Outcome == nil || snapshot.Record.Outcome.RunID != runID || snapshot.Record.Journal == nil {
		return snapshot, false, ErrLostOwnership
	}
	if snapshot.Record.Journal.State != "prepared" {
		return snapshot, false, nil
	}
	snapshot.Record.Journal.State = "sending"
	written, err := s.Store.Put(ctx, snapshot, snapshot.Record)
	return written, err == nil, err
}

func (s Service) publishNotice(ctx context.Context, cardID, runID, message string) error {
	snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil {
		return err
	}
	if snapshot.Record.Outcome == nil || snapshot.Record.Outcome.RunID != runID || snapshot.Record.Journal == nil || snapshot.Record.Journal.State != "posted" || snapshot.Record.Notice == nil {
		return ErrLostOwnership
	}
	if snapshot.Record.Notice.State != "prepared" {
		return nil
	}
	if s.Config.Notifier == nil {
		if err := s.markJournal(ctx, cardID, runID, "posted", "", "not_configured", ""); err != nil {
			return fmt.Errorf("notice state could not be recorded: %w", err)
		}
		return nil
	}
	snapshot.Record.Notice.State = "sending"
	if _, err := s.Store.Put(ctx, snapshot, snapshot.Record); err != nil {
		return err
	}
	noticeCtx, cancel := context.WithTimeout(ctx, s.Config.NoticeTimeout)
	defer cancel()
	receipt, err := s.Config.Notifier.Deliver(noticeCtx, NoticePacket{Version: SchemaVersion, NoticeID: "notice-" + runID, CardID: cardID, RunID: runID, Message: message})
	if err != nil {
		if markErr := s.markJournal(ctx, cardID, runID, "posted", "", "unknown", ""); markErr != nil {
			return fmt.Errorf("notice delivery and receipt persistence are both uncertain: %w", markErr)
		}
		return nil
	}
	if receipt.NoticeID != "notice-"+runID {
		if markErr := s.markJournal(ctx, cardID, runID, "posted", "", "unknown", ""); markErr != nil {
			return fmt.Errorf("notification receipt belonged to another notice and uncertainty could not be recorded: %w", markErr)
		}
		return nil
	}
	if err := s.markJournal(ctx, cardID, runID, "posted", "", "delivered", receipt.ReceiptID); err != nil {
		return fmt.Errorf("notice was delivered but its durable receipt could not be recorded: %w", err)
	}
	return nil
}

func (s Service) markJournal(ctx context.Context, cardID, runID, journalState, commentID, noticeState, receipt string) error {
	// This is an idempotent receipt update, not a stale claim retry. A single
	// conflict retry re-reads and proves the terminal outcome is still the same
	// run before applying only receipt fields; it never resends a message.
	for attempt := 0; attempt < 2; attempt++ {
		snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
		if err != nil {
			return err
		}
		if snapshot.Record.Outcome == nil || snapshot.Record.Outcome.RunID != runID || snapshot.Record.Journal == nil || snapshot.Record.Notice == nil {
			return ErrLostOwnership
		}
		snapshot.Record.Journal.State = journalState
		if commentID != "" {
			snapshot.Record.Journal.CommentID = commentID
		}
		snapshot.Record.Notice.State = noticeState
		snapshot.Record.Notice.ReceiptID = receipt
		if _, err = s.Store.Put(ctx, snapshot, snapshot.Record); err == nil {
			return nil
		} else if !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return ErrConflict
}

func matchesClaim(record Record, claim *Run) bool {
	if claim == nil || record.State != StateRunning || !record.Delegation.Delegated || record.Run == nil || record.Run.ID != claim.ID || record.Run.OwnerToken != claim.OwnerToken || record.Run.Fence != claim.Fence {
		return false
	}
	fence, err := claimFence(record)
	return err == nil && fence == claim.Fence
}

func ownsLive(record Record, claim *Run, now time.Time) bool {
	return matchesClaim(record, claim) && record.Run.LeaseExpires.After(now)
}

func claimFence(record Record) (string, error) {
	// Canonical JSON gives a stable fingerprint despite Go's map iteration.
	payload := struct {
		Goal               string          `json:"goal"`
		CompletionCriteria string          `json:"completion_criteria"`
		Authorization      json.RawMessage `json:"authorization"`
		Action             *ActionIntent   `json:"action,omitempty"`
		Sources            []SourceRef     `json:"sources,omitempty"`
		// Decision is an authorization input for a resumed task. Its stable
		// question ID, prompt, and exact answered value fence the claim just
		// like the operator's delegation and action intent do.
		Decision *Decision `json:"decision,omitempty"`
	}{Goal: record.Goal, CompletionCriteria: record.CompletionCriteria, Authorization: record.Delegation.Authorization, Action: record.Action, Sources: record.Sources, Decision: record.Decision}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func journalMessage(record Record) string {
	label := strings.ReplaceAll(string(record.State), "_", " ")
	message := "**Personal worker " + label + "**"
	if record.Outcome != nil && record.Outcome.RunID != "" {
		message += "\n\nRun: `" + record.Outcome.RunID + "`"
	}
	if record.Outcome != nil && record.Outcome.Summary != "" {
		message += "\n\n" + record.Outcome.Summary
	}
	if record.Outcome != nil && record.Outcome.ReviewNote != "" {
		message += "\n\nReview required: " + record.Outcome.ReviewNote
	}
	if record.State == StateWaitingUser && record.Decision != nil {
		message += "\n\nDecision needed: " + record.Decision.Prompt
	}
	return message
}

func uniqueID(prefix string) (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	prefix = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, prefix)
	return prefix + "-" + hex.EncodeToString(buf), nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
