package runs

import "agent-compose/pkg/agentcompose/domain"

type SessionResult struct {
	Session *domain.Session
	Created bool
}
