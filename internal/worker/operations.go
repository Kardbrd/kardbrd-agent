package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Enrollment struct {
	Goal               string
	CompletionCriteria string
	Authorization      json.RawMessage
	Sources            []SourceRef
	Action             *ActionIntent
}

func (s Service) Enroll(ctx context.Context, cardID string, enrollment Enrollment) error {
	if err := s.Config.ValidateReadOnly(); err != nil {
		return err
	}
	snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil {
		return err
	}
	if snapshot.HasRecord {
		return errors.New("card already contains worker metadata; refuse to replace its delegation")
	}
	record, err := newDelegatedRecord(enrollment.Goal, enrollment.CompletionCriteria, enrollment.Authorization, enrollment.Sources, enrollment.Action)
	if err != nil {
		return err
	}
	_, err = s.Store.Put(ctx, snapshot, record)
	return err
}

// Wake accepts a stable event receipt and only changes the target card. A
// duplicate receipt is a no-op even when a later polling pass occurs.
func (s Service) Wake(ctx context.Context, cardID, eventID string) (bool, error) {
	if strings.TrimSpace(eventID) == "" {
		return false, errors.New("event ID is required")
	}
	snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil {
		return false, err
	}
	if !snapshot.HasRecord || snapshot.ParseErr != nil {
		return false, errors.New("card has no recognized worker metadata")
	}
	if snapshot.Record.HasEventReceipt(eventID) {
		return false, nil
	}
	if snapshot.Record.State != StateWaitingEvent && snapshot.Record.State != StateScheduled {
		return false, fmt.Errorf("card is %s, not waiting for an event", snapshot.Record.State)
	}
	if snapshot.Record.State == StateWaitingEvent && snapshot.Record.EventID != eventID {
		return false, fmt.Errorf("event ID %q does not match this card's awaited event", eventID)
	}
	snapshot.Record.AddEventReceipt(eventID)
	snapshot.Record.State = StateReady
	snapshot.Record.WakeAt = nil
	snapshot.Record.EventID = ""
	_, err = s.Store.Put(ctx, snapshot, snapshot.Record)
	return err == nil, err
}

// Decide records an explicit operator-provided decision before making the
// intended waiting card eligible. A runner sees that decision in its next
// packet but cannot write its own delegation.
func (s Service) Decide(ctx context.Context, cardID, decisionID string, value json.RawMessage) (bool, error) {
	if strings.TrimSpace(decisionID) == "" || !json.Valid(value) {
		return false, errors.New("decision ID and valid JSON decision value are required")
	}
	snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil {
		return false, err
	}
	if !snapshot.HasRecord || snapshot.ParseErr != nil {
		return false, errors.New("card has no recognized worker metadata")
	}
	if snapshot.Record.HasDecisionReceipt(decisionID) {
		return false, nil
	}
	if snapshot.Record.State != StateWaitingUser || snapshot.Record.Decision == nil {
		return false, fmt.Errorf("card is %s, not waiting for a user decision", snapshot.Record.State)
	}
	snapshot.Record.Decision.ID = decisionID
	snapshot.Record.Decision.Value = append(json.RawMessage(nil), value...)
	snapshot.Record.AddDecisionReceipt(decisionID)
	snapshot.Record.State = StateReady
	_, err = s.Store.Put(ctx, snapshot, snapshot.Record)
	return err == nil, err
}

// InitSuggestionRegistry marks an existing operator-selected card as the
// board's source-id reservation ledger. It is paused and non-delegated, so it
// cannot ever be picked up by a task runner.
func (s Service) InitSuggestionRegistry(ctx context.Context, cardID string) error {
	if err := s.Config.ValidateReadOnly(); err != nil {
		return err
	}
	snapshot, err := s.Store.Live(ctx, s.Config.BoardID, cardID)
	if err != nil {
		return err
	}
	if snapshot.HasRecord {
		if snapshot.ParseErr == nil && snapshot.Record.SuggestionRegistry {
			return nil
		}
		return errors.New("registry card already contains non-registry worker metadata")
	}
	_, err = s.Store.Put(ctx, snapshot, newSuggestionRegistryRecord())
	return err
}

type IngestReport struct {
	Received   int      `json:"received"`
	Created    []string `json:"created"`
	Duplicates int      `json:"duplicates"`
}

// IngestSuggestions only creates non-delegated suggested records. It has no
// Runner dependency by construction and cannot promote an observer proposal.
func (s Service) IngestSuggestions(ctx context.Context, registryCardID, listID string, suggestions []Suggestion) (IngestReport, error) {
	if err := s.Config.ValidateReadOnly(); err != nil {
		return IngestReport{}, err
	}
	if strings.TrimSpace(listID) == "" {
		return IngestReport{}, errors.New("suggestion list ID is required")
	}
	if strings.TrimSpace(registryCardID) == "" {
		return IngestReport{}, errors.New("suggestion registry card ID is required")
	}
	report := IngestReport{Received: len(suggestions)}
	for _, suggestion := range suggestions {
		if strings.TrimSpace(suggestion.SourceID) == "" || strings.TrimSpace(suggestion.Title) == "" || strings.TrimSpace(suggestion.Proposal) == "" {
			return report, errors.New("suggestion source ID, title, and proposal are required")
		}
		claimed, duplicate, err := s.reserveSuggestion(ctx, registryCardID, suggestion.SourceID)
		if err != nil {
			return report, err
		}
		if duplicate {
			report.Duplicates++
			continue
		}
		cardID, err := s.Store.CreateSuggested(ctx, s.Config.BoardID, listID, suggestion)
		if err != nil {
			if markErr := s.finishSuggestionClaim(ctx, registryCardID, suggestion.SourceID, claimed, "needs_review", ""); markErr != nil {
				return report, fmt.Errorf("suggestion creation is uncertain and claim persistence failed: %w", markErr)
			}
			return report, fmt.Errorf("suggestion creation is uncertain; source held for review: %w", err)
		}
		if err := s.finishSuggestionClaim(ctx, registryCardID, suggestion.SourceID, claimed, "created", cardID); err != nil {
			return report, fmt.Errorf("suggestion card %s created but source claim requires review: %w", cardID, err)
		}
		report.Created = append(report.Created, cardID)
	}
	return report, nil
}

func (s Service) reserveSuggestion(ctx context.Context, registryCardID, sourceID string) (SuggestionClaim, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, err := s.Store.Live(ctx, s.Config.BoardID, registryCardID)
		if err != nil {
			return SuggestionClaim{}, false, err
		}
		if !snapshot.HasRecord || snapshot.ParseErr != nil || !snapshot.Record.SuggestionRegistry {
			return SuggestionClaim{}, false, errors.New("configured suggestion registry has no recognized registry metadata")
		}
		if existing, ok := snapshot.Record.SuggestionClaims[sourceID]; ok {
			return existing, true, nil
		}
		claim := SuggestionClaim{State: "creating"}
		if snapshot.Record.SuggestionClaims == nil {
			snapshot.Record.SuggestionClaims = map[string]SuggestionClaim{}
		}
		snapshot.Record.SuggestionClaims[sourceID] = claim
		if _, err = s.Store.Put(ctx, snapshot, snapshot.Record); err == nil {
			return claim, false, nil
		} else if !errors.Is(err, ErrConflict) {
			return SuggestionClaim{}, false, err
		}
	}
	return SuggestionClaim{}, false, ErrConflict
}

func (s Service) finishSuggestionClaim(ctx context.Context, registryCardID, sourceID string, expected SuggestionClaim, state, cardID string) error {
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, err := s.Store.Live(ctx, s.Config.BoardID, registryCardID)
		if err != nil {
			return err
		}
		if !snapshot.HasRecord || snapshot.ParseErr != nil || !snapshot.Record.SuggestionRegistry {
			return errors.New("configured suggestion registry changed")
		}
		current, ok := snapshot.Record.SuggestionClaims[sourceID]
		if !ok || current.State != expected.State || current.CardID != expected.CardID {
			return errors.New("suggestion source claim ownership was lost")
		}
		snapshot.Record.SuggestionClaims[sourceID] = SuggestionClaim{State: state, CardID: cardID}
		if _, err = s.Store.Put(ctx, snapshot, snapshot.Record); err == nil {
			return nil
		} else if !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return ErrConflict
}
