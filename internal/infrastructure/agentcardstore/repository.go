package agentcardstore

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var _ agentcard.Store = (*Repository)(nil)

type Repository struct {
	db     *gorm.DB
	tenant tenant.Tenant
}

func NewRepository(database *gorm.DB, owner tenant.Tenant) (*Repository, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	database = db.WithoutQueryCache(database)
	if database != nil {
		database = database.Session(&gorm.Session{Logger: logger.Discard})
		database = database.Scopes(scopeTenant(owner.ID))
	}
	return &Repository{db: database, tenant: owner}, nil
}

func scopeTenant(tenantID string) func(*gorm.DB) *gorm.DB {
	return func(database *gorm.DB) *gorm.DB {
		table := database.Statement.Table
		modelValue := database.Statement.Model
		if modelValue == nil {
			modelValue = database.Statement.Dest
		}
		if table == "" && modelValue != nil {
			_ = database.Statement.Parse(modelValue)
			table = database.Statement.Table
		}
		switch table {
		case "agent_sessions", "sessions",
			"agent_runs", "runs",
			"agent_steps", "steps",
			"agent_capability_executions",
			"agent_projection_outbox",
			"agent_card_surfaces", "surfaces":
		default:
			return database
		}
		return database.Where(clause.Eq{
			Column: clause.Column{Table: clause.CurrentTable, Name: "tenant_id"},
			Value:  tenantID,
		})
	}
}
