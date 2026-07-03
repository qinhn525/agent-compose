package sessionstore

import (
	"agent-compose/pkg/storage/sessionstore"
)

const (
	VMStatusPending = sessionstore.VMStatusPending
	VMStatusRunning = sessionstore.VMStatusRunning
	VMStatusStopped = sessionstore.VMStatusStopped
	VMStatusFailed  = sessionstore.VMStatusFailed

	CellTypeAgent = sessionstore.CellTypeAgent
)

type (
	SessionTag         = sessionstore.SessionTag
	SessionEnvVar      = sessionstore.SessionEnvVar
	SessionSummary     = sessionstore.SessionSummary
	SessionListOptions = sessionstore.SessionListOptions
	SessionListResult  = sessionstore.SessionListResult
	SessionWorkspace   = sessionstore.SessionWorkspace
	Session            = sessionstore.Session
	NotebookCell       = sessionstore.NotebookCell
	SessionEvent       = sessionstore.SessionEvent
	AgentRun           = sessionstore.AgentRun
	VMState            = sessionstore.VMState
	ProxyState         = sessionstore.ProxyState
	Store              = sessionstore.Store
)

var (
	NewStore      = sessionstore.NewStore
	NewWithConfig = sessionstore.NewWithConfig
	FromConfig    = sessionstore.FromConfig
)
