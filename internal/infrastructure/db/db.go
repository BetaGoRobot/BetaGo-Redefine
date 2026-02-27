package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/jinzhu/copier"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
)

func Init(config *config.DBConfig) {
	if config == nil {
		panic("db config is nil")
	}
	db, err := gorm.Open(postgres.Open(config.DSN()), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	// query cache callbacks...
	db.Callback().Query().Replace("gorm:query", callbackQuery)
	db.Callback().Query().After("gorm:after_query").Register("gorm:after_query_done", callbackAfter)
	query.SetDefault(db)
}

func callbackQuery(d *gorm.DB) {
	if d.Error == nil {
		callbacks.BuildQuerySQL(d)
		sql := d.Statement.SQL.String()
		vars := d.Statement.Vars
		for i, v := range vars {
			sql = strings.Replace(sql, fmt.Sprintf("$%d", i+1), fmt.Sprintf("%v", v), 1)
		}
		cacheResult := cache.Get(sql)
		if cacheResult != nil {
			logs.L().With(zap.Any("cache_result", cacheResult.Value())).Debug("cache hit, sql: " + sql)
			copier.Copy(d.Statement.Dest, cacheResult.Value())
			d.DryRun = true
		}

		if !d.DryRun && d.Error == nil {
			rows, err := d.Statement.ConnPool.QueryContext(d.Statement.Context, d.Statement.SQL.String(), d.Statement.Vars...)
			if err != nil {
				d.AddError(err)
				return
			}
			defer func() {
				d.AddError(rows.Close())
			}()
			gorm.Scan(rows, d, 0)

			if d.Statement.Result != nil {
				d.Statement.Result.RowsAffected = d.RowsAffected
			}
		}
	}
}

func callbackAfter(d *gorm.DB) {
	if d.Error != nil {
		return
	}
	sql := d.Statement.SQL.String()
	vars := d.Statement.Vars
	for i, v := range vars {
		sql = strings.Replace(sql, fmt.Sprintf("$%d", i+1), fmt.Sprintf("%v", v), 1)
	}
	result := d.Statement.Dest
	cache.Set(sql, result, time.Minute)
}
