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
	revision, botID, instanceID, expiry := m.CapabilityRevision, m.BotID, m.InstanceID, m.CapabilityExpiresAt
	m.mu.Unlock()
	if revision == "" || botID != claim.TargetBotID || instanceID == "" || (!expiry.IsZero() && !expiry.After(time.Now())) || !m.verifiedPair(claim.Model, claim.Effort) || !modelsContainPair(claim.AcceptedModels, claim.Model, claim.Effort) {
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
	m.mu.Lock()
	stillCurrent := m.CapabilityRevision == revision && m.BotID == botID && m.InstanceID == instanceID && (m.CapabilityExpiresAt.IsZero() || m.CapabilityExpiresAt.After(time.Now()))
	m.mu.Unlock()
	if !stillCurrent || !m.verifiedPair(claim.Model, claim.Effort) {
		return fmt.Errorf("accepted structured request is held: registration changed during validation")
	}
	return nil
}
