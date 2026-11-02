package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/accounts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/hindsight"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/media"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/meili"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musiccards"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musicmedia"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
	_ "github.com/lib/pq"
)

type components struct {
	memeDB         *sql.DB
	memeReferences memereference.Provider
	memeRevalidate memereference.Revalidator
	db             *sql.DB
	store          *pgstore.Store
	accounts       *accounts.Store
	index          *meili.Client
	memory         *hindsight.Client
	embed          *modelapi.Embedder
	rank           *modelapi.Reranker
	media          *media.Processor
	lark           *lark.Client
	musicCards     atoms.MusicCardPresenter
	musicService   *musiccards.Service
	musicMedia     *musicmedia.Service
	musicAudio     *business.LarkAudioUploader
}

func initialize(ctx context.Context, c config.Config) (result *components, err error) {
	result = &components{}
	defer func() {
		if err != nil {
			result.close()
		}
	}()
	result.db, err = sql.Open("postgres", c.Database.URL)
	if err != nil {
		return result, errors.New("independent database initialization failed")
	}
	result.db.SetMaxOpenConns(c.Database.MaxConnections)
	result.db.SetMaxIdleConns(c.Database.MaxConnections)
	result.db.SetConnMaxIdleTime(5 * time.Minute)
	startup, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err = result.db.PingContext(startup); err != nil {
		return result, errors.New("independent database unavailable")
	}
	result.store, err = pgstore.New(result.db, c.Database.Schema)
	if err != nil {
		return result, errors.New("independent schema configuration invalid")
	}
	if err = result.store.Migrate(startup); err != nil {
		return result, errors.New("independent schema migration failed")
	}
	if c.Capabilities.LuckinCredentialsKey != "" {
		key, decodeErr := hex.DecodeString(c.Capabilities.LuckinCredentialsKey)
		if decodeErr != nil {
			return result, errors.New("personal account encryption configuration invalid")
		}
		result.accounts, err = accounts.New(result.db, c.Database.Schema, key)
		if err != nil {
			return result, errors.New("personal account storage initialization failed")
		}
		if err = result.accounts.Migrate(startup); err != nil {
			return result, errors.New("personal account storage migration failed")
		}
	}
	e := c.Models.Embedding
	result.embed, err = modelapi.NewEmbedder(modelapi.EmbeddingConfig{Endpoint: endpoint(e.Endpoint), Dimensions: e.Dimensions, Concurrency: e.Concurrency, MaxInputs: e.MaxInputs})
	if err != nil {
		return result, errors.New("embedding adapter initialization failed")
	}
	if err = initializeMemeReferences(startup, c, result); err != nil {
		return result, err
	}
	result.rank, err = modelapi.NewReranker(endpoint(c.Models.Rerank))
	if err != nil {
		return result, errors.New("reranking adapter initialization failed")
	}
	fingerprint, _ := json.Marshal([]any{e.BaseURL, e.Protocol, e.Model, e.Dimensions})
	digest := sha256.Sum256(fingerprint)
	result.index, err = meili.New(meili.Options{Endpoint: c.Search.Endpoint, APIKey: c.Search.APIKey, MessagesIndex: c.Search.MessagesIndex, ContextsIndex: c.Search.ContextsIndex, EmbeddingModel: hex.EncodeToString(digest[:]), VectorDimensions: e.Dimensions})
	if err != nil {
		return result, errors.New("search adapter initialization failed")
	}
	if err = result.index.Initialize(startup); err != nil {
		return result, errors.New("search initialization failed")
	}
	result.memory, err = hindsight.New(hindsight.Options{URL: c.Memory.Endpoint, APIKey: c.Memory.APIKey, Timeout: 120 * time.Second})
	if err != nil {
		return result, errors.New("memory adapter initialization failed")
	}
	result.lark, err = lark.NewClient(lark.ClientConfig{AppID: c.Lark.AppID, AppSecret: c.Lark.AppSecret, BotOpenID: c.Lark.BotOpenID, TenantID: c.App.TenantID, AllowedChatIDs: c.App.AllowedChatIDs, LocalMessages: result.store, ValidateTarget: musicTargetValidator(result), ValidateMusicCard: func(ctx context.Context, s domain.Scope, id string) error {
		if result.musicService == nil {
			return domain.ErrUnauthorized
		}
		return result.musicService.CheckMessage(ctx, s, id)
	}})
	if err != nil {
		return result, errors.New("Lark atomic adapter initialization failed")
	}
	objects, err := media.NewMinIO(media.MinIOConfig{Endpoint: c.Objects.Endpoint, AccessKey: c.Objects.AccessKey, SecretKey: c.Objects.SecretKey, Bucket: c.Objects.Bucket, Secure: c.Objects.Secure})
	if err != nil {
		return result, errors.New("original media store initialization failed")
	}
	if err = objects.Initialize(startup); err != nil {
		return result, errors.New("original media bucket initialization failed")
	}
	mediaConfig := media.Config{Downloader: result.lark, Objects: objects}
	if c.Models.Vision.Model != "" {
		vision, e := modelapi.NewChat(endpoint(c.Models.Vision))
		if e != nil {
			return result, errors.New("vision adapter initialization failed")
		}
		mediaConfig.Vision = vision
	}
	if c.Models.Audio.Model != "" {
		speech, e := modelapi.NewChat(endpoint(c.Models.Audio))
		if e != nil {
			return result, errors.New("audio adapter initialization failed")
		}
		mediaConfig.Speech = speech
	}
	result.media, err = media.New(mediaConfig)
	if err != nil {
		return result, errors.New("media processor initialization failed")
	}
	if err = initializeMusic(startup, c, result); err != nil {
		return result, errors.New("music presentation initialization failed")
	}
	return result, nil
}
func readyHandler(db *sql.DB, extra func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"live":true}`))
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if extra != nil && !extra() {
			http.Error(w, "receiver not ready", 503)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if db == nil || db.PingContext(ctx) != nil {
			http.Error(w, "database not ready", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ready":true}`))
	})
	return mux
}
func owner() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("process identity generation failed")
	}
	return "agent-" + hex.EncodeToString(b[:]), nil
}
