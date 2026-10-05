package cli

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/agent"
	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

func publishExecutionCapabilities(ctx context.Context, manager *agent.Manager, client *api.Client) error {
	payload, ok := manager.BuildExecutionCapabilities()
	if !ok {
		manager.SetCapabilityRevision("")
		return nil
	}
	result, err := client.RegisterExecutionCapabilities(ctx, payload)
	if err != nil {
		manager.SetCapabilityRegistration(payload.InstanceID, "", time.Time{})
		return err
	}
	if result.Revision == "" || !result.ExpiresAt.After(time.Now()) {
		manager.SetCapabilityRegistration(payload.InstanceID, "", time.Time{})
		return errors.New("capability registration returned no fresh revision")
	}
	if !manager.SetCapabilityRegistration(payload.InstanceID, result.Revision, result.ExpiresAt) {
		return context.Canceled
	}
	return nil
}

type capabilityRegistrationLoop struct {
	mu        sync.Mutex
	publishMu sync.Mutex
	replayMu  sync.Mutex
	cancel    context.CancelFunc
	manager   *agent.Manager
	client    *api.Client
}

func (r *capabilityRegistrationLoop) connected(parent context.Context, botID, instanceID string) {
	r.disconnected()
	r.manager.SetConnectedBot(botID, instanceID)
	if botID == "" || instanceID == "" {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()
	go func() {
		for {
			err := r.publish(ctx)
			wait := 10 * time.Second
			if err == nil {
				go r.reconcile(ctx)
				if expiry := r.manager.CurrentCapabilityExpiry(); expiry.After(time.Now()) {
					wait = time.Until(expiry) / 2
					if wait > time.Minute {
						wait = time.Minute
					}
					if wait < time.Second {
						wait = time.Second
					}
				} else {
					wait = time.Minute
				}
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func (r *capabilityRegistrationLoop) disconnected() {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.mu.Unlock()
	if r.manager != nil {
		r.manager.SetConnectedBot("", "")
	}
}

func (r *capabilityRegistrationLoop) refresh(ctx context.Context) {
	if err := r.publish(ctx); err == nil {
		go r.reconcile(ctx)
	}
}

func (r *capabilityRegistrationLoop) reconcile(ctx context.Context) {
	r.replayMu.Lock()
	defer r.replayMu.Unlock()
	if ctx.Err() != nil || r.manager.CurrentCapabilityRevision() == "" {
		return
	}
	if err := r.manager.ReconcileExecutionRequests(ctx); err != nil {
		log.Printf("structured request replay held: %v", err)
		return
	}
	if err := r.manager.RecoverAcceptedMentions(ctx); err != nil {
		log.Printf("local structured request reconciliation held: %v", err)
	}
}

func (r *capabilityRegistrationLoop) publish(ctx context.Context) error {
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	return publishExecutionCapabilities(ctx, r.manager, r.client)
}
