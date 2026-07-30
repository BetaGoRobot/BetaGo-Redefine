package agentcardstore

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(database *gorm.DB) *Repository {
	database = db.WithoutQueryCache(database)
	if database != nil {
		database = database.Session(&gorm.Session{Logger: logger.Discard})
	}
	return &Repository{db: database}
}
