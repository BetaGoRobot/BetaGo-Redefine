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
	dataMap := map[string]func(detailType gorm.ColumnType) (dataType string){
		// 针对 text[] 数组
		"text[]": func(detailType gorm.ColumnType) (dataType string) {
			return "pq.StringArray"
		},
	}

	g.WithDataTypeMap(dataMap)
	// 预编译正则，用于匹配 GORM tag 中的 type 属性
	// typeRegex := regexp.MustCompile(`type:[^;]+`)

	// 2. 拦截并修改字段的 GORM Tag
	g.WithOpts(gen.FieldModify(func(f gen.Field) gen.Field {
		if f.Type == "pq.StringArray" {
			f.GORMTag.Append("type", "text[]")
		}
		return f
	}))
	tables := g.GenerateAllTable()
	g.ApplyBasic(tables...)
	// Generate the code
	g.Execute()
}
