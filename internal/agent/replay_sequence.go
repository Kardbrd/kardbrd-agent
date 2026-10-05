package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	for i, item := range rankedItems {
		if i > 0 {
			prior := rankedItems[i-1]
			if prior.order == item.order && prior.identity.CardID == item.identity.CardID && (prior.created.IsZero() || item.created.IsZero() || prior.created.Equal(item.created)) {
				uncertain[item.identity.CardID] = true
			}
		}
		sequence.Items = append(sequence.Items, item.identity)
	}
	for cardID := range uncertain {
		sequence.UncertainCards = append(sequence.UncertainCards, cardID)
	}
	sort.Strings(sequence.UncertainCards)
	return sequence, nil
}

func (m *Manager) recordReplaySequence(seen []replayIdentity) error {
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
	current := make(map[replayIdentity]bool, len(seen))
	created := make(map[replayIdentity]time.Time, len(sequence.Items)+len(seen))
	for _, item := range seen {
		if current[item] {
			return fmt.Errorf("replay repeated request %s", item.CommentID)
		}
		current[item] = true
		claim, err := m.readMentionClaim(item.CardID, item.CommentID)
		if err != nil || claim.BoardID != m.BoardID || claim.TargetBotID != botID {
			return fmt.Errorf("replay request %s has no owned durable claim: %w", item.CommentID, err)
		}
		created[item], _ = time.Parse(time.RFC3339Nano, claim.CreatedAt)
	}
	known := make(map[replayIdentity]bool, len(sequence.Items))
	for _, item := range sequence.Items {
		known[item] = true
		if _, ok := created[item]; !ok {
			claim, err := m.readMentionClaim(item.CardID, item.CommentID)
			if err != nil {
				return err
			}
			created[item], _ = time.Parse(time.RFC3339Nano, claim.CreatedAt)
		}
	}
	// Insert each newly seen identity ahead of its next known successor in
	// Web's current order. If there is none, append it. Existing identities
	// never acquire a different identity or lose their historical order.
	for i, item := range seen {
		if known[item] {
			continue
		}
		at := len(sequence.Items)
		for _, successor := range seen[i+1:] {
			if !known[successor] {
				continue
			}
			for j, old := range sequence.Items {
				if old == successor {
					at = j
					break
				}
			}
			break
		}
		sequence.Items = append(sequence.Items, replayIdentity{})
		copy(sequence.Items[at+1:], sequence.Items[at:])
		sequence.Items[at] = item
		known[item] = true
	}
	// Creation time is immutable and orders requests across omitted rows;
	// the stable merge above supplies the server's tie order where observed.
	// With an older claim lacking creation time, preserve its known relative
	// order instead of applying an incomplete timestamp comparison.
	allCreatedKnown := true
	for _, item := range sequence.Items {
		if created[item].IsZero() {
			allCreatedKnown = false
			break
		}
	}
	if allCreatedKnown {
		sort.SliceStable(sequence.Items, func(i, j int) bool {
			return created[sequence.Items[i]].Before(created[sequence.Items[j]])
		})
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
