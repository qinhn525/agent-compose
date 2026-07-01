package domain

import (
	"strings"
	"time"
)

const (
	DefaultAgentProvider = "codex"

	AgentSessionTagSource    = "source"
	AgentSessionTagSourceVal = "agent"
	AgentSessionTagID        = "agent_id"
	AgentSessionTagName      = "agent_name"
)

type AgentDefinition struct {
	ID                     string          `json:"id"`
	Name                   string          `json:"name"`
	Description            string          `json:"description,omitempty"`
	Enabled                bool            `json:"enabled"`
	DeletedAt              time.Time       `json:"deleted_at,omitempty"`
	Provider               string          `json:"provider"`
	Model                  string          `json:"model,omitempty"`
	SystemPrompt           string          `json:"system_prompt,omitempty"`
	Driver                 string          `json:"driver,omitempty"`
	GuestImage             string          `json:"guest_image,omitempty"`
	WorkspaceID            string          `json:"workspace_id,omitempty"`
	EnvItems               []SessionEnvVar `json:"env_items,omitempty"`
	ConfigJSON             string          `json:"config_json"`
	CapsetIDs              []string        `json:"capset_ids,omitempty"`
	ManagedProjectID       string          `json:"managed_project_id,omitempty"`
	ManagedProjectRevision int64           `json:"managed_project_revision,omitempty"`
	ManagedAgentName       string          `json:"managed_agent_name,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

type AgentDefinitionListOptions struct {
	Query           string
	IncludeDisabled bool
	Offset          int
	Limit           int
}

type AgentDefinitionListResult struct {
	Agents     []AgentDefinition
	TotalCount int
	HasMore    bool
	NextOffset int
}

type AgentCurrentRunSummary struct {
	RunningSessionCount int
}

type AgentLatestRunSummary struct {
	RunType string
	Status  string
	RunID   string
	Title   string
	At      time.Time
}

func NormalizeAgentKind(agent string) string {
	agent = strings.ToLower(strings.TrimSpace(agent))
	switch agent {
	case "":
		return ""
	case "codex":
		return "codex"
	case "claude", "claude-code", "claude_code":
		return "claude"
	case "gemini", "gemini-cli", "gemini_cli":
		return "gemini"
	case "opencode", "open-code", "open_code":
		return "opencode"
	default:
		return agent
	}
}

func SessionHasAgentTag(session *Session, agentID string) bool {
	if session == nil {
		return false
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	hasSource := false
	hasAgentID := false
	for _, tag := range session.Summary.Tags {
		name := strings.TrimSpace(tag.Name)
		value := strings.TrimSpace(tag.Value)
		if name == AgentSessionTagSource && value == AgentSessionTagSourceVal {
			hasSource = true
		}
		if name == AgentSessionTagID && value == agentID {
			hasAgentID = true
		}
	}
	return hasSource && hasAgentID
}
