package agentcompose

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/samber/do/v2"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage"
	"agent-compose/pkg/storage/configstore"
)

const storedUnixMillisecondThreshold int64 = configstore.StoredUnixMillisecondThreshold

type ConfigStore struct {
	*storage.ConfigStore
	db *sql.DB
}

func NewConfigStore(di do.Injector) (*ConfigStore, error) {
	inner, err := storage.NewConfigStore(di)
	if err != nil {
		return nil, err
	}
	store := &ConfigStore{
		ConfigStore: inner,
		db:          inner.DB(),
	}
	if err := store.initSchema(context.Background()); err != nil {
		if store.db != nil {
			_ = store.db.Close()
		}
		return nil, err
	}
	return store, nil
}

func (s *ConfigStore) initSchema(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("config store is required")
	}
	inner := s.coreStore()
	if err := inner.InitCoreSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureLLMSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureCapabilityGatewaySchema(ctx); err != nil {
		return err
	}
	if err := s.ensureLoaderSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureEventSchema(ctx); err != nil {
		return err
	}
	return nil
}

func (s *ConfigStore) coreStore() *storage.ConfigStore {
	if s.ConfigStore == nil {
		s.ConfigStore = storage.FromDB(s.db)
	}
	if s.db == nil {
		s.db = s.DB()
	}
	return s.ConfigStore
}

func (s *ConfigStore) tableColumnTypes(ctx context.Context, tableName string) (map[string]string, error) {
	return s.coreStore().TableColumnTypes(ctx, tableName)
}

func (s *ConfigStore) ensureGlobalEnvSchema(ctx context.Context) error {
	return s.coreStore().EnsureGlobalEnvSchema(ctx)
}

func (s *ConfigStore) ensureWorkspaceConfigSchema(ctx context.Context) error {
	return s.coreStore().EnsureWorkspaceConfigSchema(ctx)
}

func (s *ConfigStore) ensureAgentDefinitionSchema(ctx context.Context) error {
	return s.coreStore().EnsureAgentDefinitionSchema(ctx)
}

func (s *ConfigStore) rebuildGlobalEnvTable(ctx context.Context) error {
	return s.coreStore().RebuildGlobalEnvTable(ctx)
}

func (s *ConfigStore) rebuildWorkspaceConfigTable(ctx context.Context) error {
	return s.coreStore().RebuildWorkspaceConfigTable(ctx)
}

func (s *ConfigStore) getAgentDefinitionIfExists(ctx context.Context, id string, includeDeleted bool) (domain.AgentDefinition, bool, error) {
	return s.coreStore().GetAgentDefinitionIfExists(ctx, id, includeDeleted)
}
