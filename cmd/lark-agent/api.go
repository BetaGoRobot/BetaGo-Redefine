package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type taskReader interface {
	TaskFacts(context.Context, domain.Scope, string) (json.RawMessage, error)
}

// This service key grants read-only diagnostics. It cannot approve, delegate,
// modify or execute a group task. Those actions require verified Lark identity.
func taskAPI(c config.Config, store taskReader) http.Handler {
	mux := http.NewServeMux()
	want := sha256.Sum256([]byte("Bearer " + c.App.APIKey))
	mux.HandleFunc("GET /api/tasks/{task_id}", func(w http.ResponseWriter, r *http.Request) {
		got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if c.App.APIKey == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			http.Error(w, "authentication required", 401)
			return
		}
		scope := domain.Scope{TenantID: r.Header.Get("X-Agent-Tenant-Key"), AppID: c.Lark.AppID, ChatID: r.Header.Get("X-Agent-Chat-ID"), Visibility: c.App.Visibility}
		allowed := len(c.App.AllowedChatIDs) == 0
		for _, chat := range c.App.AllowedChatIDs {
			if chat == scope.ChatID {
				allowed = true
				break
			}
		}
		if scope.Validate() != nil || !allowed || (c.App.TenantID != "" && scope.TenantID != c.App.TenantID) {
			http.Error(w, "scope not allowed", 403)
			return
		}
		id := r.PathValue("task_id")
		if len(id) == 0 || len(id) > 256 {
			http.Error(w, "invalid task ID", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		b, err := store.TaskFacts(ctx, scope, id)
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "task not found", 404)
			return
		}
		if err != nil || len(b) > 2<<20 || !json.Valid(b) {
			http.Error(w, "task facts unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	return mux
}
