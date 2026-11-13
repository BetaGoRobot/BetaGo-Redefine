// music-server runs the same runtime module without starting shared Bot services.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/music"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	dsn := os.Getenv("MUSIC_DATABASE_URL")
	if dsn == "" {
		log.Fatal("MUSIC_DATABASE_URL required")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		log.Fatal("music database connection failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	opts := music.OptionsFromEnv(func() *gorm.DB { return db })
	m := music.NewModule(opts)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = m.Init(ctx); err != nil {
		log.Fatal(err)
	}
	if err = m.Start(ctx); err != nil {
		log.Fatal(err)
	}
	if err = m.Ready(ctx); err != nil {
		log.Fatal(err)
	}
	log.Printf("music ready on %s (isolated module)", opts.Addr)
	<-ctx.Done()
	stop, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	if err = m.Stop(stop); err != nil {
		log.Print(err)
	}
}
