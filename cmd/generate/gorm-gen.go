package main

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"gorm.io/driver/postgres"
	"gorm.io/gen"
	"gorm.io/gorm"
)

func main() {
	config := config.LoadFile(".dev/config.toml")

	g := gen.NewGenerator(gen.Config{
		OutPath: "internal/infrastructure/db/query",
		Mode:    gen.WithDefaultQuery | gen.WithQueryInterface | gen.WithGeneric, // generate mode
	})
	var dsn string
	if config.DBConfig != nil {
		dsn = config.DBConfig.DSN()
	}
	gormdb, err := gorm.Open(postgres.Open(dsn))
	if err != nil {
		panic(err)
	}
	g.UseDB(gormdb) // reuse your gorm db
	tables := g.GenerateAllTable()
	g.ApplyBasic(tables...)
	// Generate the code
	g.Execute()
}
