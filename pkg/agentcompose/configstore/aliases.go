package configstore

import (
	"agent-compose/pkg/storage/configstore"
)

const StoredUnixMillisecondThreshold int64 = configstore.StoredUnixMillisecondThreshold

var (
	AgentMatchesQuery            = configstore.AgentMatchesQuery
	BoolToInt                    = configstore.BoolToInt
	DecodeAgentEnvJSON           = configstore.DecodeAgentEnvJSON
	EncodeAgentEnvJSON           = configstore.EncodeAgentEnvJSON
	EnsureColumn                 = configstore.EnsureColumn
	IsIntegerColumnType          = configstore.IsIntegerColumnType
	NormalizeSQLiteTimestampExpr = configstore.NormalizeSQLiteTimestampExpr
	NormalizeWorkspaceConfig     = configstore.NormalizeWorkspaceConfig
	ParseStoredLoaderTriggerTime = configstore.ParseStoredLoaderTriggerTime
	ParseStoredTime              = configstore.ParseStoredTime
	ParseStoredUnixTimeAuto      = configstore.ParseStoredUnixTimeAuto
	ScanAgentDefinition          = configstore.ScanAgentDefinition
	ScanWorkspaceConfig          = configstore.ScanWorkspaceConfig
	TableColumnTypes             = configstore.TableColumnTypes
)
