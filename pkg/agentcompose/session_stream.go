package agentcompose

import (
	"agent-compose/pkg/agentcompose/sessions"

	"github.com/samber/do/v2"
)

type (
	sessionWatchEventType = sessions.WatchEventType
	sessionWatchEvent     = sessions.WatchEvent
	SessionStreamBroker   = sessions.StreamBroker
)

const (
	sessionWatchEventTypeUnspecified    = sessions.WatchEventTypeUnspecified
	sessionWatchEventTypeSessionUpdated = sessions.WatchEventTypeSessionUpdated
	sessionWatchEventTypeCellStarted    = sessions.WatchEventTypeCellStarted
	sessionWatchEventTypeCellOutput     = sessions.WatchEventTypeCellOutput
	sessionWatchEventTypeCellCompleted  = sessions.WatchEventTypeCellCompleted
	sessionWatchEventTypeEventAdded     = sessions.WatchEventTypeEventAdded
)

func NewSessionStreamBroker(di do.Injector) (*SessionStreamBroker, error) {
	return sessions.NewStreamBroker(di)
}
