package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/schema"
	opensearchschema "github.com/BetaGoRobot/BetaGo-Redefine/script/opensearch"
	opensearchclient "github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	uuid "github.com/satori/go.uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestZeroTouchBootstrapTwoBotsAndRestart(t *testing.T) {
	ctx := context.Background()
	database, schemaName := zeroTouchDatabase(t)
	firstReport, err := (&schema.Runner{
		DB: database, Schema: schemaName, Revision: "integration-first",
		Migrations: schema.DefaultMigrations(),
	}).Apply(ctx)
	if err != nil {
		t.Fatalf("cold-start PostgreSQL bootstrap: %v", err)
	}
	secondReport, err := (&schema.Runner{
		DB: database, Schema: schemaName, Revision: "integration-restart",
		Migrations: schema.DefaultMigrations(),
	}).Apply(ctx)
	if err != nil {
		t.Fatalf("restart PostgreSQL bootstrap: %v", err)
	}
	if len(firstReport.Applied) != len(schema.DefaultMigrations()) ||
		len(secondReport.Applied) != 0 ||
		len(secondReport.Skipped) != len(schema.DefaultMigrations()) {
		t.Fatalf(
			"migration reports are not cold-start/restart safe: first=%#v second=%#v",
			firstReport,
			secondReport,
		)
	}

	owners := []tenant.Tenant{
		mustTenant(t, "shared-app", "bot-a"),
		mustTenant(t, "shared-app", "bot-b"),
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	cohortIDs := make([]string, 0, len(owners))
	for _, owner := range owners {
		repository, repoErr := evaluationstore.NewRepository(database, owner)
		if repoErr != nil {
			t.Fatal(repoErr)
		}
		cohort, ensureErr := repository.EnsureRollingCohort(
			ctx,
			conversationeval.MessageInput{
				AppID: owner.AppID, BotOpenID: owner.BotOpenID,
				ChatID: "shared-chat", EventID: "shared-event",
				MessageID: "shared-message", OccurredAt: now,
			},
			6*time.Hour,
		)
		if ensureErr != nil {
			t.Fatalf("automatic cohort for %s: %v", owner.ID, ensureErr)
		}
		active, activeErr := repository.ActiveCohorts(ctx, "shared-chat", now)
		if activeErr != nil {
			t.Fatal(activeErr)
		}
		if len(active) != 1 || active[0].TenantID != owner.ID ||
			active[0].ID != cohort.ID {
			t.Fatalf("tenant cohort visibility for %s = %#v", owner.ID, active)
		}
		cohortIDs = append(cohortIDs, cohort.ID)
	}
	if cohortIDs[0] == cohortIDs[1] {
		t.Fatalf("same logical cohort collided across tenants: %q", cohortIDs[0])
	}

	search := newFakeOpenSearch(t)
	for _, owner := range owners {
		for _, target := range []struct {
			base    string
			version string
			mapping []byte
		}{
			{"agent_conversation_events", "conversation_event.v1", opensearchschema.ConversationEventsV1},
			{"agent_conversation_evaluations", "conversation_evaluation.v1", opensearchschema.ConversationEvaluationsV1},
		} {
			first, ensureErr := search.provisioner.EnsureTenantIndex(
				ctx, owner, target.base, target.version, target.mapping,
			)
			if ensureErr != nil {
				t.Fatalf("cold-start OpenSearch bootstrap: %v", ensureErr)
			}
			restarted, ensureErr := search.provisioner.EnsureTenantIndex(
				ctx, owner, target.base, target.version, target.mapping,
			)
			if ensureErr != nil {
				t.Fatalf("restart OpenSearch bootstrap: %v", ensureErr)
			}
			if first != restarted {
				t.Fatalf("OpenSearch resource changed on restart: %#v != %#v", first, restarted)
			}
		}
	}
	if search.createCalls != 4 {
		t.Fatalf("OpenSearch creates = %d, want two indices per tenant", search.createCalls)
	}
	for _, base := range []string{
		"agent_conversation_events",
		"agent_conversation_evaluations",
	} {
		firstAlias, _ := owners[0].IndexAlias(base)
		secondAlias, _ := owners[1].IndexAlias(base)
		if firstAlias == secondAlias ||
			search.aliasTargets[firstAlias] == search.aliasTargets[secondAlias] {
			t.Fatalf("tenant OpenSearch resources overlap for %s", base)
		}
	}
}

func zeroTouchDatabase(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	configPath := strings.TrimSpace(os.Getenv("BETAGO_CONFIG_PATH"))
	if configPath == "" {
		t.Skip("BETAGO_CONFIG_PATH is not set")
	}
	cfg, err := infraConfig.LoadFileE(configPath)
	if err != nil || cfg == nil || cfg.DBConfig == nil {
		t.Skip("PostgreSQL test configuration is unavailable")
	}
	root, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Skipf("PostgreSQL is unavailable: %v", err)
	}
	rootSQL, err := root.DB()
	if err != nil || rootSQL.PingContext(context.Background()) != nil {
		t.Skip("PostgreSQL is unavailable")
	}
	schemaName := "zerotouch_e2e_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	if err := root.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schemaName)).Error; err != nil {
		t.Fatalf("create temporary schema: %v", err)
	}
	testConfig := *cfg.DBConfig
	testConfig.SearchPath = schemaName
	testConfig.ApplicationName = schemaName
	database, err := gorm.Open(postgres.Open(testConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		if dropErr := root.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schemaName)).Error; dropErr != nil {
			t.Errorf("drop temporary schema: %v", dropErr)
		}
		_ = rootSQL.Close()
	})
	return database, schemaName
}

func mustTenant(t *testing.T, appID, botOpenID string) tenant.Tenant {
	t.Helper()
	owner, err := tenant.New(appID, botOpenID)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

type fakeOpenSearch struct {
	provisioner  *opensearch.Provisioner
	server       *httptest.Server
	mu           sync.Mutex
	aliasTargets map[string]string
	mappings     map[string]json.RawMessage
	createCalls  int
}

func newFakeOpenSearch(t *testing.T) *fakeOpenSearch {
	t.Helper()
	state := &fakeOpenSearch{
		aliasTargets: make(map[string]string),
		mappings:     make(map[string]json.RawMessage),
	}
	state.server = httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	t.Cleanup(state.server.Close)
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearchclient.Config{Addresses: []string{state.server.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	state.provisioner, err = opensearch.NewProvisionerWithClient(client)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func (s *fakeOpenSearch) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	switch {
	case request.Method == http.MethodGet &&
		strings.HasPrefix(request.URL.Path, "/_alias/"):
		alias := strings.TrimPrefix(request.URL.Path, "/_alias/")
		physical, ok := s.aliasTargets[alias]
		if !ok {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"status":404}`))
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			physical: map[string]any{
				"aliases": map[string]any{
					alias: map[string]any{"is_write_index": true},
				},
			},
		})
	case request.Method == http.MethodGet &&
		strings.HasSuffix(request.URL.Path, "/_mapping"):
		physical := strings.TrimSuffix(
			strings.TrimPrefix(request.URL.Path, "/"),
			"/_mapping",
		)
		mapping, ok := s.mappings[physical]
		if !ok {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"status":404}`))
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			physical: map[string]any{"mappings": mapping},
		})
	case request.Method == http.MethodPut:
		var body struct {
			Aliases  map[string]json.RawMessage `json:"aliases"`
			Mappings json.RawMessage            `json:"mappings"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		physical := strings.TrimPrefix(request.URL.Path, "/")
		for alias := range body.Aliases {
			s.aliasTargets[alias] = physical
		}
		s.mappings[physical] = append(json.RawMessage(nil), body.Mappings...)
		s.createCalls++
		_, _ = writer.Write([]byte(`{"acknowledged":true}`))
	default:
		http.Error(writer, "unexpected request", http.StatusNotFound)
	}
}
