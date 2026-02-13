package db

import (
	"strconv"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/jinzhu/copier"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gen"
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
	sql := d.Statement.SQL.String()
	result := d.Statement.Dest
	cache.Set(sql, result, time.Minute)
}

type DBCacheHelper[T, E any] struct {
	gen.IGenericsDo[T, E]
	model   *E
	keyFunc func(*E) string
}

func NewCache[T, E any](dao gen.IGenericsDo[T, E]) *DBCacheHelper[T, E] {
	return &DBCacheHelper[T, E]{
		IGenericsDo: dao,
		keyFunc:     func(e *E) string { return strconv.FormatInt(time.Now().Unix(), 10) },
		model:       new(E),
	}
}

func (h *DBCacheHelper[T, E]) WithKeyFunc(keyFunc func(*E) string) *DBCacheHelper[T, E] {
	h.keyFunc = keyFunc
	return h
}

func (h *DBCacheHelper[T, E]) WithModel(model *E) *DBCacheHelper[T, E] {
	h.model = model
	return h
}

func (h *DBCacheHelper[T, E]) Find() ([]E, error) {
	res, err := h.IGenericsDo.Find()

	// 缓存一下
	return res, err
}

func test() {
	NewCache(query.Q.Administrator.WithContext(nil)).Select().Find()
}
