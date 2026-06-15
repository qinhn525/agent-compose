package agentcompose

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

const stalePendingSessionLastError = "session startup interrupted before runtime reached running state"

func (s *Service) reconcilePersistedSessions(ctx context.Context) error {
	result, err := s.store.ListSessions(ctx, SessionListOptions{Limit: 1 << 30})
	if err != nil {
		return err
	}
	for _, session := range result.Sessions {
		reconciled, err := s.reconcilePendingSessionState(ctx, session)
		if err != nil {
			slog.Warn("failed to reconcile pending session state", "session_id", session.Summary.ID, "error", err)
			continue
		}
		if _, err := s.reconcileSessionRuntimeState(ctx, reconciled); err != nil {
			slog.Warn("failed to reconcile session runtime state", "session_id", session.Summary.ID, "error", err)
		}
	}
	return nil
}

func (s *Service) reconcilePendingSessionState(ctx context.Context, session *Session) (*Session, error) {
	if session == nil || session.Summary.VMStatus != VMStatusPending {
		return session, nil
	}
	if !session.Summary.CreatedAt.Before(s.startedAt) {
		return session, nil
	}
	vmState, err := s.store.GetVMState(session.Summary.ID)
	if err != nil {
		return nil, err
	}
	if !vmState.StartedAt.IsZero() {
		return session, nil
	}
	now := time.Now().UTC()
	vmState.StoppedAt = now
	vmState.BoxID = ""
	if strings.TrimSpace(vmState.LastError) == "" {
		vmState.LastError = stalePendingSessionLastError
	}
	if err := s.store.SaveVMState(session.Summary.ID, vmState); err != nil {
		return nil, err
	}
	session.Summary.VMStatus = VMStatusFailed
	if err := s.store.UpdateSession(ctx, session); err != nil {
		return nil, err
	}
	_ = s.store.AddEvent(ctx, session.Summary.ID, SessionEvent{
		ID:        uuid.NewString(),
		Type:      "session.startup_interrupted",
		Level:     "warn",
		Message:   "session marked failed after a previous startup was interrupted before the VM became ready",
		CreatedAt: now,
	})
	return s.store.GetSession(ctx, session.Summary.ID)
}
