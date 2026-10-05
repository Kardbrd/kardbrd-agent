package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

// verifyCurrentClaim runs immediately before any workspace or executor side
// effect, including after a comment has waited behind another card session.
func (m *Manager) verifyCurrentClaim(ctx context.Context, claim mentionClaim) error {
	m.mu.Lock()
	revision, botID, instanceID := m.CapabilityRevision, m.BotID, m.InstanceID
	m.mu.Unlock()
	if revision == "" || botID == "" || instanceID == "" || !m.verifiedPair(claim.Model, claim.Effort) {
		return fmt.Errorf("accepted structured request is held: no compatible live registration")
	}
	if reader, ok := m.Client.(interface {
		GetExecutionCapabilities(context.Context, string, string) (api.ExecutionCapabilityView, error)
	}); ok {
		view, err := reader.GetExecutionCapabilities(ctx, botID, m.BoardID)
		if err != nil || view.Revision != revision || view.InstanceID != instanceID || view.BoardID != m.BoardID || view.Executor != m.ExecutorType || !view.ExpiresAt.After(time.Now()) {
			return fmt.Errorf("accepted structured request is held: live registration changed")
		}
	}
	return nil
}
