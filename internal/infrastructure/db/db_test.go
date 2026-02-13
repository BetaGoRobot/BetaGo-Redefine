package db

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	. "github.com/bytedance/mockey"
	. "github.com/smartystreets/goconvey/convey"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func InitWithoutCache(config *config.DBConfig) {
	if config == nil {
		panic("db config is nil")
	}
	db, err := gorm.Open(postgres.Open(config.DSN()), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	query.SetDefault(db)
}

func TestCacheDBRead(t *testing.T) {
	config := config.LoadFile("../../../.dev/config.toml")
	InitWithoutCache(config.DBConfig)
	otel.Init(config.OtelConfig)
	logs.Init()
	ctx := context.Background()
	baseData, err := query.Q.Administrator.WithContext(ctx).Order(query.Administrator.ID.Desc()).Limit(10).Find()
	if err != nil {
		panic(err)
	}
	PatchConvey(
		"Test NoCacheDB Read", t, func() {
			result, err := query.Q.Administrator.WithContext(ctx).Order(query.Administrator.ID.Desc()).Limit(10).Find()
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(len(result), ShouldBeLessThanOrEqualTo, 10)
			So(result, ShouldEqual, baseData)
		},
	)
	PatchConvey(
		"Test CacheDB Read", t, func() {
			Init(config.DBConfig)
			ctx := context.Background()
			for i := 0; i < 10; i++ {
				result, err := query.Q.Administrator.WithContext(ctx).Order(query.Administrator.ID.Desc()).Limit(10).Find()
				So(err, ShouldBeNil)
				So(result, ShouldNotBeNil)
				So(len(result), ShouldBeLessThanOrEqualTo, 10)
				So(result, ShouldEqual, baseData)
			}
		},
	)
}

func BenchmarkDBRead(b *testing.B) {
	config := config.LoadFile("../../../.dev/config.toml")
	otel.Init(config.OtelConfig)
	logs.Init()
	b.Run("withoutCache", func(b *testing.B) {
		InitWithoutCache(config.DBConfig)
		b.ResetTimer()
		ctx := context.Background()
		for i := 0; i < b.N; i++ {
			_, err := query.Q.Administrator.WithContext(ctx).Order(query.Administrator.ID.Desc()).Limit(10).Find()
			if err != nil {
				panic(err)
			}
		}
	})
	b.Run("withCache", func(b *testing.B) {
		Init(config.DBConfig)
		b.ResetTimer()
		ctx := context.Background()
		for i := 0; i < b.N; i++ {
			_, err := query.Q.Administrator.WithContext(ctx).Order(query.Administrator.ID.Desc()).Limit(10).Find()
			if err != nil {
				panic(err)
			}
		}
	})
}
