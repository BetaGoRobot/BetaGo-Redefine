package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/schema"
	uuid "github.com/satori/go.uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

var postgresIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func main() {
	configPath := strings.TrimSpace(os.Getenv("BETAGO_CONFIG_PATH"))
	if configPath == "" {
		configPath = ".dev/config.toml"
	}
	baseConfig := config.LoadFile(configPath)
	if baseConfig == nil || baseConfig.DBConfig == nil {
		panic("database config is required")
	}
	dbConfig := *baseConfig.DBConfig
	sourceSchema := firstSchema(dbConfig.SearchPath)
	if sourceSchema == "" {
		sourceSchema = "betago"
	}

	var cleanup func()
	if strings.EqualFold(
		strings.TrimSpace(os.Getenv("BETAGO_GEN_CLONE_SCHEMA")),
		"true",
	) {
		adminDB, err := gorm.Open(postgres.Open(dbConfig.DSN()))
		if err != nil {
			panic(err)
		}
		generatedSchema, cleanupClone, err := cloneSchema(
			context.Background(),
			adminDB,
			sourceSchema,
		)
		if err != nil {
			panic(err)
		}
		cleanup = cleanupClone
		defer cleanup()
		runner := &schema.Runner{
			DB: adminDB, Schema: generatedSchema, Revision: "gorm-gen",
			Migrations: schema.DefaultMigrations(),
		}
		if _, err = runner.Apply(context.Background()); err != nil {
			panic(err)
		}
		dbConfig.SearchPath = generatedSchema
	}

	g := gen.NewGenerator(gen.Config{
		OutPath: "internal/infrastructure/db/query",
		Mode:    gen.WithDefaultQuery | gen.WithQueryInterface | gen.WithGeneric, // generate mode
	})
	gormdb, err := gorm.Open(postgres.Open(dbConfig.DSN()))
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
	tableNames, err := gormdb.Migrator().GetTables()
	if err != nil {
		panic(err)
	}
	tables := make([]any, 0, len(tableNames))
	for _, tableName := range tableNames {
		switch tableName {
		case "permission_grants":
			tables = append(tables, g.GenerateModel(tableName,
				gen.FieldType("resource_chat_id", "*string"),
				gen.FieldType("resource_user_id", "*string"),
			))
		case "todo_items":
			tables = append(tables, g.GenerateModel(tableName,
				gen.FieldType("due_at", "*time.Time"),
				gen.FieldType("completed_at", "*time.Time"),
			))
		case "scheduled_tasks":
			tables = append(tables, g.GenerateModel(tableName,
				gen.FieldType("run_at", "*time.Time"),
				gen.FieldType("last_run_at", "*time.Time"),
			))
		case "luckin_pending_orders":
			tables = append(tables, g.GenerateModel(tableName,
				gen.FieldType("create_order_payload", "datatypes.JSON"),
				gen.FieldType("preview_result", "datatypes.JSON"),
				gen.FieldType("result_json", "datatypes.JSON"),
				gen.FieldGORMTag("create_order_payload", func(tag field.GormTag) field.GormTag {
					tag.Append("type", "jsonb")
					return tag
				}),
				gen.FieldGORMTag("preview_result", func(tag field.GormTag) field.GormTag {
					tag.Append("type", "jsonb")
					return tag
				}),
				gen.FieldGORMTag("result_json", func(tag field.GormTag) field.GormTag {
					tag.Append("type", "jsonb")
					return tag
				}),
			))
		default:
			tables = append(tables, g.GenerateModel(tableName))
		}
	}
	g.ApplyBasic(tables...)
	// Generate the code
	g.Execute()
}

func cloneSchema(
	ctx context.Context,
	database *gorm.DB,
	source string,
) (string, func(), error) {
	if database == nil || !postgresIdentifier.MatchString(source) {
		return "", nil, fmt.Errorf("invalid source schema %q", source)
	}
	target := "gorm_gen_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	if err := database.WithContext(ctx).Exec(
		`CREATE SCHEMA ` + quoteIdentifier(target),
	).Error; err != nil {
		return "", nil, fmt.Errorf("create generator schema: %w", err)
	}
	cleanup := func() {
		_ = database.Exec(`DROP SCHEMA ` + quoteIdentifier(target) + ` CASCADE`).Error
	}

	var tables []string
	if err := database.WithContext(ctx).Raw(`
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = ? AND table_type = 'BASE TABLE'
		ORDER BY table_name`,
		source,
	).Scan(&tables).Error; err != nil {
		cleanup()
		return "", nil, fmt.Errorf("list source schema tables: %w", err)
	}
	for _, table := range tables {
		if !postgresIdentifier.MatchString(table) {
			cleanup()
			return "", nil, fmt.Errorf("invalid source table %q", table)
		}
		statement := fmt.Sprintf(
			`CREATE TABLE %s.%s (LIKE %s.%s INCLUDING ALL)`,
			quoteIdentifier(target),
			quoteIdentifier(table),
			quoteIdentifier(source),
			quoteIdentifier(table),
		)
		if err := database.WithContext(ctx).Exec(statement).Error; err != nil {
			cleanup()
			return "", nil, fmt.Errorf("clone source table %s: %w", table, err)
		}
	}
	return target, cleanup, nil
}

func firstSchema(searchPath string) string {
	value := strings.TrimSpace(strings.Split(searchPath, ",")[0])
	return strings.Trim(value, `"`)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
