package opensearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func TestProvisionerCreatesTenantAliasAndPhysicalIndexAtomically(t *testing.T) {
	owner, err := tenant.New("app-a", "bot-a")
	if err != nil {
		t.Fatal(err)
	}
	alias, err := owner.IndexAlias("agent_conversation_evaluations")
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	created := false
	createCalls := 0
	var createBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		mutex.Lock()
		defer mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/_alias/"+alias:
			if !created {
				writer.WriteHeader(http.StatusNotFound)
				_, _ = writer.Write([]byte(`{"status":404}`))
				return
			}
			_, _ = writer.Write([]byte(`{"` + alias + `-v1":{"aliases":{"` +
				alias + `":{"is_write_index":true}}}}`))
		case request.Method == http.MethodPut:
			createCalls++
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err := json.Unmarshal(body, &createBody); err != nil {
				t.Fatal(err)
			}
			created = true
			_, _ = writer.Write([]byte(`{"acknowledged":true}`))
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{Addresses: []string{server.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := (&Provisioner{client: client}).EnsureTenantIndex(
		context.Background(),
		owner,
		"agent_conversation_evaluations",
		"conversation_evaluation.v1",
		[]byte(`{"settings":{"number_of_shards":1},"mappings":{"dynamic":false,"properties":{}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Alias != alias || resource.PhysicalIndex != alias+"-v1" {
		t.Fatalf("resource = %#v", resource)
	}
	if createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", createCalls)
	}
	aliases, ok := createBody["aliases"].(map[string]any)
	if !ok || aliases[alias] == nil {
		t.Fatalf("create body aliases = %#v", createBody["aliases"])
	}
	mappings, ok := createBody["mappings"].(map[string]any)
	if !ok {
		t.Fatalf("create body mappings = %#v", createBody["mappings"])
	}
	meta, ok := mappings["_meta"].(map[string]any)
	if !ok || meta["tenant_id"] != owner.ID ||
		meta["schema_version"] != "conversation_evaluation.v1" {
		t.Fatalf("mapping metadata = %#v", mappings["_meta"])
	}
	properties, ok := mappings["properties"].(map[string]any)
	if !ok || properties["tenant_id"] == nil ||
		properties["app_id"] == nil || properties["bot_open_id"] == nil {
		t.Fatalf("tenant properties = %#v", mappings["properties"])
	}
}

func TestProvisionerAdoptsExistingSingleTenantAliasWithoutCreating(t *testing.T) {
	owner, _ := tenant.New("app-a", "bot-a")
	alias, _ := owner.IndexAlias("events")
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet {
			t.Fatalf("unexpected mutating request: %s %s", request.Method, request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"events-physical":{"aliases":{"` +
			alias + `":{"is_write_index":true}}}}`))
	}))
	defer server.Close()
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{Addresses: []string{server.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := (&Provisioner{client: client}).EnsureTenantIndex(
		context.Background(), owner, "events", "events.v1",
		[]byte(`{"mappings":{"properties":{}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if resource.PhysicalIndex != "events-physical" {
		t.Fatalf("resource = %#v", resource)
	}
}

func TestProvisionerIsIdempotentAndSeparatesBotsSharingBaseName(t *testing.T) {
	owners := make([]tenant.Tenant, 0, 2)
	for _, identity := range [][2]string{
		{"shared-app", "bot-a"},
		{"shared-app", "bot-b"},
	} {
		owner, err := tenant.New(identity[0], identity[1])
		if err != nil {
			t.Fatal(err)
		}
		owners = append(owners, owner)
	}

	var mutex sync.Mutex
	aliasTargets := make(map[string]string)
	createCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		mutex.Lock()
		defer mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			alias := request.URL.Path[len("/_alias/"):]
			physical, exists := aliasTargets[alias]
			if !exists {
				writer.WriteHeader(http.StatusNotFound)
				_, _ = writer.Write([]byte(`{"status":404}`))
				return
			}
			_, _ = writer.Write([]byte(`{"` + physical +
				`":{"aliases":{"` + alias +
				`":{"is_write_index":true}}}}`))
			return
		}
		if request.Method != http.MethodPut {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			Aliases map[string]json.RawMessage `json:"aliases"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Aliases) != 1 {
			t.Fatalf("aliases = %#v", body.Aliases)
		}
		physical := request.URL.Path[1:]
		for alias := range body.Aliases {
			if _, exists := aliasTargets[alias]; exists {
				t.Fatalf("duplicate create for alias %q", alias)
			}
			aliasTargets[alias] = physical
		}
		createCalls++
		_, _ = writer.Write([]byte(`{"acknowledged":true}`))
	}))
	defer server.Close()
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{Addresses: []string{server.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	provisioner := &Provisioner{client: client}
	resources := make([]TenantIndex, 0, len(owners))
	for _, owner := range owners {
		first, err := provisioner.EnsureTenantIndex(
			context.Background(), owner, "shared-evaluations", "evaluation.v1",
			[]byte(`{"mappings":{"properties":{}}}`),
		)
		if err != nil {
			t.Fatal(err)
		}
		second, err := provisioner.EnsureTenantIndex(
			context.Background(), owner, "shared-evaluations", "evaluation.v1",
			[]byte(`{"mappings":{"properties":{}}}`),
		)
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Fatalf("restart changed resource: first=%#v second=%#v", first, second)
		}
		resources = append(resources, first)
	}
	if createCalls != 2 {
		t.Fatalf("create calls = %d, want one per tenant", createCalls)
	}
	if resources[0].Alias == resources[1].Alias ||
		resources[0].PhysicalIndex == resources[1].PhysicalIndex {
		t.Fatalf("tenant resources overlap: %#v", resources)
	}
}
