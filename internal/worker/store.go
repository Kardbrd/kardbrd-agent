package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

var (
	ErrConflict = errors.New("worker metadata conflict")
	ErrNotFound = errors.New("worker card not found")
)

// Client is intentionally smaller than api.Client so worker tests can use a
// real HTTP-shaped fake without exposing unrelated board operations.
type Client interface {
	GetBoard(context.Context, string, bool) (json.RawMessage, error)
	GetCard(context.Context, string) (json.RawMessage, error)
	GetCardMarkdown(context.Context, string) (string, error)
	GetCardMetadata(context.Context, string) (api.CardMetadata, error)
	UpdateCardMetadata(context.Context, string, api.MetadataPatch) (json.RawMessage, error)
	CreateCard(context.Context, string, string, string, string) (json.RawMessage, error)
	AddCommentOnce(context.Context, string, string) (json.RawMessage, error)
}

type cardView struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	IsArchived bool   `json:"is_archived"`
	Board      struct {
		ID string `json:"id"`
	} `json:"board"`
}

type boardView struct {
	ID    string `json:"id"`
	Lists []struct {
		Cards []cardView `json:"cards"`
	} `json:"lists"`
}

type Snapshot struct {
	Card      cardView
	Metadata  api.CardMetadata
	Record    Record
	HasRecord bool
	ParseErr  error
}

type Store struct {
	Client Client
}

func (s Store) Scan(ctx context.Context, boardID string) ([]Snapshot, error) {
	raw, err := s.Client.GetBoard(ctx, boardID, true)
	if err != nil {
		return nil, err
	}
	var board boardView
	if err := json.Unmarshal(raw, &board); err != nil {
		return nil, fmt.Errorf("decode worker board: %w", err)
	}
	if board.ID != "" && board.ID != boardID {
		return nil, fmt.Errorf("board response %q does not match %q", board.ID, boardID)
	}
	var snapshots []Snapshot
	for _, list := range board.Lists {
		for _, card := range list.Cards {
			if card.ID == "" || card.IsArchived {
				continue
			}
			if card.Board.ID != "" && card.Board.ID != boardID {
				continue
			}
			snapshot, err := s.loadWithCard(ctx, card)
			if err != nil {
				return nil, err
			}
			snapshots = append(snapshots, snapshot)
		}
	}
	return snapshots, nil
}

func (s Store) Load(ctx context.Context, cardID string) (Snapshot, error) {
	raw, err := s.Client.GetCard(ctx, cardID)
	if err != nil {
		return Snapshot{}, err
	}
	var card cardView
	if err := json.Unmarshal(raw, &card); err != nil {
		return Snapshot{}, fmt.Errorf("decode worker card %q: %w", cardID, err)
	}
	if card.ID == "" {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrNotFound, cardID)
	}
	return s.loadWithCard(ctx, card)
}

func (s Store) loadWithCard(ctx context.Context, card cardView) (Snapshot, error) {
	metadata, err := s.Client.GetCardMetadata(ctx, card.ID)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Card: card, Metadata: metadata}
	raw, ok := metadata.Metadata[MetadataKey]
	if !ok {
		return snapshot, nil
	}
	snapshot.HasRecord = true
	snapshot.Record, snapshot.ParseErr = ParseRecord(raw)
	return snapshot, nil
}

// Put changes only the worker's literal top-level metadata key. api metadata
// writes retain every unrelated key and enforce the provided revision.
func (s Store) Put(ctx context.Context, snapshot Snapshot, record Record) (Snapshot, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return snapshot, err
	}
	_, err = s.Client.UpdateCardMetadata(ctx, snapshot.Card.ID, api.MetadataPatch{
		Set:              map[string]json.RawMessage{MetadataKey: encoded},
		ExpectedRevision: snapshot.Metadata.MetadataRevision,
	})
	if err != nil {
		if isConflict(err) {
			return snapshot, ErrConflict
		}
		return snapshot, err
	}
	snapshot.Record = record
	snapshot.HasRecord = true
	snapshot.ParseErr = nil
	snapshot.Metadata.MetadataRevision++
	snapshot.Metadata.Metadata[MetadataKey] = encoded
	return snapshot, nil
}

func (s Store) Live(ctx context.Context, boardID, cardID string) (Snapshot, error) {
	snapshot, err := s.Load(ctx, cardID)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Card.IsArchived || snapshot.Card.Board.ID != boardID {
		return Snapshot{}, fmt.Errorf("worker card %q is archived or no longer on board %q", cardID, boardID)
	}
	return snapshot, nil
}

func (s Store) CreateSuggested(ctx context.Context, boardID, listID string, proposal Suggestion) (string, error) {
	raw, err := s.Client.CreateCard(ctx, boardID, listID, proposal.Title, proposal.Proposal)
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		if err == nil {
			err = errors.New("create suggestion response has no card ID")
		}
		return "", err
	}
	snapshot, err := s.Load(ctx, created.ID)
	if err != nil {
		return "", err
	}
	record := Record{
		Version:            SchemaVersion,
		Goal:               proposal.Title,
		CompletionCriteria: "Review and explicitly delegate this suggestion before any execution.",
		State:              StateSuggested,
		Sources:            []SourceRef{{ID: proposal.SourceID, Reference: proposal.Reference}},
	}
	if _, err := s.Put(ctx, snapshot, record); err != nil {
		return "", err
	}
	return created.ID, nil
}

func isConflict(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == 409 || strings.EqualFold(apiErr.Code, "METADATA_CONFLICT"))
}
