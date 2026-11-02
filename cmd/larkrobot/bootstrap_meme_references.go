package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	cfg "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mememilvus"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mememodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	_ "github.com/lib/pq"
)

func masterMemeSettings(base *cfg.BaseConfig) (memeruntime.Settings, error) {
	var settings memeruntime.Settings
	if base == nil || base.MemeConfig == nil || !base.MemeConfig.Enabled {
		return settings, nil
	}
	settings = *base.MemeConfig
	if raw, ok := os.LookupEnv("BETAGO_MEME_KNOWLEDGE_FIRST"); ok {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return settings, errors.New("invalid meme knowledge mode")
		}
		settings.KnowledgeFirst = value
	}
	if settings.DatabaseURL == "" && base.DBConfig != nil {
		settings.DatabaseURL = base.DBConfig.DSN()
	}
	if ark := base.ArkConfig; ark != nil {
		if settings.QueryModel == "" {
			settings.QueryModel = ark.NormalModel
		}
		if settings.EmbeddingModel == "" {
			settings.EmbeddingModel = ark.EmbeddingModel
		}
		if settings.EmbeddingModel != ark.EmbeddingModel {
			return settings, errors.New("meme embedding model must match MASTER embedding adapter")
		}
	}
	return settings.Normalize()
}

func masterMemeModule(base *cfg.BaseConfig) appruntime.Module {
	var connection *sql.DB
	return appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name: "meme-references",
		Init: func(ctx context.Context) (err error) {
			handlers.ConfigureMemeReferences(nil)
			settings, err := masterMemeSettings(base)
			if err != nil {
				return err
			}
			if !settings.Enabled {
				return appruntime.ErrDisabled
			}
			if base.LarkConfig == nil || base.LarkConfig.AppID == "" {
				return errors.New("meme references require configured bot identity")
			}
			connection, err = sql.Open("postgres", settings.DatabaseURL)
			if err != nil {
				return errors.New("meme catalog database initialization failed")
			}
			connection.SetMaxOpenConns(6)
			connection.SetMaxIdleConns(3)
			store, err := memestore.New(connection, settings.Schema)
			if err != nil {
				return err
			}
			startup, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if err = store.Migrate(startup); err != nil {
				return errors.New("meme catalog initialization failed")
			}
			gate, err := store.Gate("ark:meme-reference", time.Duration(settings.StartIntervalMillis)*time.Millisecond, settings.MaxConcurrency)
			if err != nil {
				return err
			}
			botID := "lark:" + base.LarkConfig.AppID
			scope := llmusage.Scope{SourceType: llmusage.SourceTypeUser, Source: "meme_reference"}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: settings.InsecureSkipVerify}
			catalog := store.ForScope(settings.Scope)
			index, err := mememilvus.New(mememilvus.Options{Endpoint: settings.SearchEndpoint, Username: settings.SearchUsername, Password: settings.SearchPassword, HTTPClient: &http.Client{Transport: transport, Timeout: 10 * time.Second}, IndexPrefix: settings.IndexPrefix, Scope: settings.Scope, EmbeddingModel: settings.EmbeddingModel, Dimensions: settings.Dimensions, Embedder: &mememodel.Embedder{Scope: scope, Gate: gate}, Ledger: catalog, Records: catalog})
			if err != nil {
				return err
			}
			service, err := memeruntime.New(memeruntime.Options{Catalog: catalog, Knowledge: catalog, KnowledgeFirst: settings.KnowledgeFirst, Scope: settings.Scope, Index: index, BotID: botID, AllowedChats: settings.AllowedChatIDs, Model: func(_ context.Context, chatID string) (memes.Completion, error) {
				chatScope := scope
				chatScope.ChatID = chatID
				return mememodel.New(mememodel.Options{ModelID: settings.QueryModel, Scope: chatScope, Gate: gate})
			}, Reference: memes.ReferenceOptions{RequestTimeout: time.Duration(settings.RequestTimeoutSeconds) * time.Second, StageTimeout: time.Duration(settings.QueryTimeoutSeconds) * time.Second, Metadata: memes.RunMetadata{Model: settings.QueryModel}}})
			if err != nil {
				return err
			}
			if err = service.Warm(startup); err != nil {
				logs.L().Ctx(ctx).Warn("meme knowledge warmup unavailable; compatible retrieval enabled")
			}
			handlers.ConfigureMemeReferenceRuntime(service.References, service.Revalidate)
			return nil
		},
		Stop: func(context.Context) error {
			handlers.ConfigureMemeReferences(nil)
			if connection != nil {
				return connection.Close()
			}
			return nil
		},
	})
}
