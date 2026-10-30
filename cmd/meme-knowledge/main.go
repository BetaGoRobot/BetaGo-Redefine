// meme-knowledge publishes immutable knowledge beside a sealed meme generation.
// It never activates a corpus, changes source cards or rebuilds search indexes.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
	_ "github.com/lib/pq"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dbEnv := flag.String("database-env", "MEME_KNOWLEDGE_DATABASE_URL", "name of an existing environment variable containing the catalog DSN")
	schema := flag.String("schema", "meme_reply", "existing catalog schema")
	scope := flag.String("scope", "wiki", "existing visibility scope")
	expected := flag.String("expected-generation", "", "required generation when publishing")
	publish := flag.Bool("publish", false, "write immutable sidecar; default only verifies and compiles")
	export := flag.String("export", "", "optional local snapshot JSON path")
	flag.Parse()
	if flag.NArg() != 0 || (*publish && *expected == "") {
		return errors.New("publish requires an explicit expected-generation")
	}
	dsn := os.Getenv(*dbEnv)
	if dsn == "" {
		return errors.New("catalog database environment is missing")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return errors.New("catalog configuration invalid")
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	store, err := memestore.New(db, *schema)
	if err != nil {
		return errors.New("catalog schema invalid")
	}
	scoped := store.ForScope(*scope)
	manifest, err := scoped.Active(ctx)
	if err != nil {
		return errors.New("active corpus unavailable")
	}
	if *expected != "" && manifest.Generation != *expected {
		return errors.New("active generation differs from expected generation")
	}
	started := time.Now()
	records, err := scoped.GenerationRecords(ctx, manifest)
	if err != nil {
		return errors.New("sealed corpus membership verification failed")
	}
	loadedMS := time.Since(started).Milliseconds()
	snapshot, err := memes.CompileKnowledgeSnapshot(memestore.KnowledgeBinding(*scope, manifest), records)
	if err != nil {
		return errors.New("knowledge compilation failed")
	}
	if _, err = memes.PrepareKnowledge(snapshot); err != nil {
		return errors.New("knowledge dictionary preparation failed")
	}
	if *export != "" {
		file, err := os.OpenFile(*export, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("snapshot export cannot be created")
		}
		err = json.NewEncoder(file).Encode(snapshot)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return errors.New("snapshot export failed")
		}
	}
	if *publish {
		if err = store.Migrate(ctx); err != nil {
			return errors.New("knowledge table initialization failed")
		}
		if err = scoped.PublishKnowledge(ctx, manifest, snapshot); err != nil {
			return errors.New("knowledge publication failed")
		}
		readback, e := scoped.Knowledge(ctx, manifest)
		if e != nil || readback.Fingerprint != snapshot.Fingerprint {
			return errors.New("knowledge readback failed")
		}
	}
	after, err := scoped.Active(ctx)
	if err != nil || after.Generation != manifest.Generation || after.CorpusHash != manifest.CorpusHash || after.ProjectionHash != manifest.ProjectionHash {
		return errors.New("active corpus changed during knowledge operation")
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Binding      memes.KnowledgeBinding `json:"binding"`
		Fingerprint  string                 `json:"fingerprint"`
		RecordCount  int                    `json:"record_count"`
		EntryCount   int                    `json:"entry_count"`
		Published    bool                   `json:"published"`
		MembershipMS int64                  `json:"membership_ms"`
		ElapsedMS    int64                  `json:"elapsed_ms"`
	}{snapshot.Binding, snapshot.Fingerprint, len(records), len(snapshot.Entries), *publish, loadedMS, time.Since(started).Milliseconds()})
}
