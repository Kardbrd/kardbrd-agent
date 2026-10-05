package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

// replayIdentity is the public identity of one durable request. Its position
// in replaySequence is local history, not an absolute rank in Web's mutable
// collection. Omitted requests retain their place for local recovery.
type replayIdentity struct {
	CardID    string `json:"card_id"`
	CommentID string `json:"comment_id"`
}

type replaySequence struct {
	Version        int              `json:"version"`
	BoardID        string           `json:"board_id"`
	TargetBotID    string           `json:"target_bot_id"`
	Items          []replayIdentity `json:"items"`
	Current        []replayIdentity `json:"current,omitempty"`
	Prefix         []replayIdentity `json:"prefix,omitempty"`
	Complete       bool             `json:"complete,omitempty"`
	Scanning       bool             `json:"scanning,omitempty"`
	UncertainCards []string         `json:"uncertain_cards,omitempty"`
}

func (m *Manager) replaySequencePath(botID string) string {
	dir := m.ClaimDir
	if dir == "" {
		dir = filepath.Join(m.CWD, "state", "agent-claims")
	}
	digest := sha256.Sum256([]byte(m.BoardID + "\x00" + botID))
	return filepath.Join(dir, "replay-"+hex.EncodeToString(digest[:])+".sequence")
}

func (m *Manager) readReplaySequence(botID string) (replaySequence, error) {
	sequence := replaySequence{Version: 1, BoardID: m.BoardID, TargetBotID: botID}
	data, err := os.ReadFile(m.replaySequencePath(botID))
	if errors.Is(err, os.ErrNotExist) {
		return m.migrateReplaySequence(sequence)
	}
	if err != nil {
		return sequence, err
	}
	if err := json.Unmarshal(data, &sequence); err != nil {
		return sequence, err
	}
	if sequence.Version != 1 || sequence.BoardID != m.BoardID || sequence.TargetBotID != botID {
		return sequence, fmt.Errorf("replay sequence identity mismatch")
	}
	known := make(map[replayIdentity]bool, len(sequence.Items))
	for _, item := range sequence.Items {
		if item.CardID == "" || item.CommentID == "" || known[item] {
			return sequence, fmt.Errorf("replay sequence contains invalid or duplicate identity")
		}
		known[item] = true
	}
	for _, item := range sequence.Current {
		if !known[item] {
			return sequence, fmt.Errorf("replay current identity is absent from durable sequence")
		}
	}
	for _, item := range sequence.Prefix {
		if !known[item] {
			return sequence, fmt.Errorf("replay prefix identity is absent from durable sequence")
		}
	}
	return sequence, nil
}

// Old PR #73 sidecars contain a rank from one historical scan. They provide
// a one-time relative order for migration, including requests now omitted by
// Web. The ranks are never compared with a later scan's positions.
func (m *Manager) migrateReplaySequence(sequence replaySequence) (replaySequence, error) {
	dir := filepath.Dir(m.replaySequencePath(sequence.TargetBotID))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return sequence, nil
	}
	if err != nil {
		return sequence, err
	}
	type ranked struct {
		identity replayIdentity
		order    uint64
		created  time.Time
	}
	var rankedItems []ranked
	uncertain := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return sequence, err
		}
		var claim mentionClaim
		if err := json.Unmarshal(data, &claim); err != nil {
			return sequence, err
		}
		if claim.Version != 1 || claim.BoardID != sequence.BoardID || claim.TargetBotID != sequence.TargetBotID {
			continue
		}
		order, err := m.readReplayOrder(claim)
		if err != nil {
			uncertain[claim.CardID] = true
		}
		created, _ := time.Parse(time.RFC3339Nano, claim.CreatedAt)
		if order != 0 {
			rankedItems = append(rankedItems, ranked{replayIdentity{claim.CardID, claim.CommentID}, order, created})
		} else if err != nil {
			rankedItems = append(rankedItems, ranked{replayIdentity{claim.CardID, claim.CommentID}, ^uint64(0), created})
		}
	}
	sort.SliceStable(rankedItems, func(i, j int) bool { return rankedItems[i].order < rankedItems[j].order })
	seenTies := map[string]bool{}
	for _, item := range rankedItems {
		// Old absolute ranks have no scan provenance. Even unequal ranks
		// cannot order two tied requests from different mutable scans.
		key := item.identity.CardID + "\x00" + item.created.Format(time.RFC3339Nano)
		if item.created.IsZero() {
			key = item.identity.CardID + "\x00unknown"
		}
		if seenTies[key] {
			uncertain[item.identity.CardID] = true
		}
		seenTies[key] = true
		sequence.Items = append(sequence.Items, item.identity)
	}
	for cardID := range uncertain {
		sequence.UncertainCards = append(sequence.UncertainCards, cardID)
	}
	sort.Strings(sequence.UncertainCards)
	return sequence, nil
}

func (m *Manager) recordReplaySequence(seen []replayIdentity, complete bool) error {
	m.mu.Lock()
	botID := m.verifiedBotID
	verified := botID != "" && (m.BotID == "" || m.BotID == botID)
	m.mu.Unlock()
	if !verified {
		return fmt.Errorf("replay sequence owner is unverified")
	}
	sequence, err := m.readReplaySequence(botID)
	if err != nil {
		return err
	}
	priorItems, priorCurrent, priorPrefix, priorComplete, priorScanning := sequence.Items, sequence.Current, sequence.Prefix, sequence.Complete, sequence.Scanning
	observed := make(map[replayIdentity]bool, len(seen))
	for _, item := range seen {
		if observed[item] {
			return fmt.Errorf("replay repeated request %s", item.CommentID)
		}
		observed[item] = true
	}
	// The prefix from this scan is canonical among currently visible rows.
	// Keep all omitted historical IDs after it for local identity/outbox
	// recovery. Their relative order has no bearing on new execution: a
	// completed scan holds omitted unstarted claims until they reappear.
	merged := make([]replayIdentity, 0, len(seen)+len(sequence.Items))
	merged = append(merged, seen...)
	for _, item := range sequence.Items {
		if !observed[item] {
			merged = append(merged, item)
		}
	}
	sequence.Items = merged
	if complete {
		sequence.Current = append([]replayIdentity(nil), seen...)
		sequence.Prefix = nil
		sequence.Complete = true
		sequence.Scanning = false
	} else {
		sequence.Prefix = append([]replayIdentity(nil), seen...)
		sequence.Scanning = true
	}
	if priorComplete == sequence.Complete && priorScanning == sequence.Scanning && slices.Equal(priorItems, sequence.Items) && slices.Equal(priorCurrent, sequence.Current) && slices.Equal(priorPrefix, sequence.Prefix) {
		return nil
	}
	path := m.replaySequencePath(botID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(sequence)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "replay-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncClaimDirectory(filepath.Dir(path))
}
