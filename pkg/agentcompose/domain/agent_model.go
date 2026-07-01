package domain

import "time"

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
