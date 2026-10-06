package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

func (m *Manager) BuildExecutionCapabilities() (api.ExecutionCapabilities, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg := m.CommentExecution
	if cfg == nil || cfg.Verification.Source == "" || m.InstanceID == "" || (m.ExecutorType != "codex" && m.ExecutorType != "claude") {
		return api.ExecutionCapabilities{}, false
	}
	payload := api.ExecutionCapabilities{Version: 1, InstanceID: m.InstanceID, Executor: m.ExecutorType, BoardID: m.BoardID, Models: []api.ExecutionModel{}, Provenance: api.ExecutionProvenance{Source: cfg.Verification.Source, VerifiedAt: cfg.Verification.VerifiedAt}}
	for _, model := range cfg.Models {
		entry := api.ExecutionModel{ID: model.ID, Efforts: []string{}}
		for _, effort := range model.Efforts {
			if !m.suppressedPairs[model.ID+"\x00"+effort] {
				entry.Efforts = append(entry.Efforts, effort)
			}
		}
		if len(entry.Efforts) > 0 {
			payload.Models = append(payload.Models, entry)
		}
	}
	if cfg.Defaults.Model != "" {
		value := cfg.Defaults.Model
		payload.Defaults.Model = &value
	}
	if cfg.Defaults.Effort != "" {
		value := cfg.Defaults.Effort
		payload.Defaults.Effort = &value
	}
	if payload.Defaults.Model != nil && payload.Defaults.Effort != nil && m.suppressedPairs[*payload.Defaults.Model+"\x00"+*payload.Defaults.Effort] {
		payload.Defaults.Model = nil
		payload.Defaults.Effort = nil
	}
	fingerprintInput, _ := json.Marshal(struct {
		Executor   string
		Config     any
		Suppressed map[string]bool
	}{m.ExecutorType, cfg, m.suppressedPairs})
	digest := sha256.Sum256(fingerprintInput)
	payload.Provenance.ConfigFingerprint = hex.EncodeToString(digest[:])
	return payload, true
}

func (m *Manager) withdrawRejectedPair(model, effort, errorText string) bool {
	if model == "" || effort == "" {
		return false
	}
	lower := strings.ToLower(errorText)
	if (!strings.Contains(lower, "not supported") && !strings.Contains(lower, "not available") && !strings.Contains(lower, "unknown model")) || (!strings.Contains(lower, "model") && !strings.Contains(lower, strings.ToLower(model))) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.suppressedPairs[model+"\x00"+effort] = true
	m.CapabilityRevision = ""
	return true
}
