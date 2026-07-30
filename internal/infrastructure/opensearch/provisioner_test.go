package opensearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		case request.Method == http.MethodGet &&
			request.URL.Path == "/"+alias+"-v1/_mapping":
			encoded, encodeErr := json.Marshal(map[string]any{
				alias + "-v1": map[string]any{
					"mappings": createBody["mappings"],
				},
			})
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			_, _ = writer.Write(encoded)
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
		switch request.URL.Path {
		case "/_alias/" + alias:
			_, _ = writer.Write([]byte(`{"events-physical":{"aliases":{"` +
				alias + `":{"is_write_index":true}}}}`))
		case "/events-physical/_mapping":
			_, _ = writer.Write([]byte(`{"events-physical":{"mappings":{
				"_meta":{
					"schema_name":"events",
					"schema_version":"events.v1",
					"tenant_id":"` + owner.ID + `",
					"app_id":"app-a",
					"bot_open_id":"bot-a"
				},
				"properties":{
					"tenant_id":{"type":"keyword"},
					"app_id":{"type":"keyword"},
					"bot_open_id":{"type":"keyword"}
				}
			}}}`))
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

func TestProvisionerRejectsExistingAliasWithIncompatibleTenantMapping(t *testing.T) {
	owner, _ := tenant.New("app-a", "bot-a")
	alias, _ := owner.IndexAlias("events")
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/_alias/" + alias:
			_, _ = writer.Write([]byte(`{"events-physical":{"aliases":{"` +
				alias + `":{"is_write_index":true}}}}`))
		case "/events-physical/_mapping":
			_, _ = writer.Write([]byte(`{"events-physical":{"mappings":{
				"_meta":{
					"schema_name":"conversation_event",
					"schema_version":"conversation_event.v1",
					"tenant_id":"another-tenant",
					"app_id":"app-a",
					"bot_open_id":"bot-a"
				},
				"properties":{
					"tenant_id":{"type":"keyword"},
					"app_id":{"type":"keyword"},
					"bot_open_id":{"type":"keyword"}
				}
			}}}`))
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
	if _, err := (&Provisioner{client: client}).EnsureTenantIndex(
		context.Background(), owner, "events", "conversation_event.v1",
		[]byte(`{"mappings":{"properties":{}}}`),
	); err == nil {
		t.Fatal("EnsureTenantIndex() accepted an alias owned by another tenant")
	}
}

func TestProvisionerFailsClosedWhenMappingPermissionIsDenied(t *testing.T) {
	owner, _ := tenant.New("app-a", "bot-a")
	alias, _ := owner.IndexAlias("events")
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/_alias/" + alias:
			_, _ = writer.Write([]byte(`{"events-physical":{"aliases":{"` +
				alias + `":{"is_write_index":true}}}}`))
		case "/events-physical/_mapping":
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"error":"forbidden"}`))
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
	if _, err := (&Provisioner{client: client}).EnsureTenantIndex(
		context.Background(), owner, "events", "events.v1",
		[]byte(`{"mappings":{"properties":{}}}`),
	); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("EnsureTenantIndex() permission error = %v", err)
	}
}

func TestValidateMappingSubsetRejectsNestedFieldDrift(t *testing.T) {
	expected := map[string]any{
		"dynamic": false,
		"properties": map[string]any{
			"control": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reply_text": map[string]any{"type": "text"},
				},
			},
		},
	}
	actual := map[string]any{
		"dynamic": false,
		"properties": map[string]any{
			"control": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reply_text": map[string]any{"type": "keyword"},
				},
			},
		},
	}
	if err := validateMappingSubset(actual, expected, "mappings"); err == nil ||
		!strings.Contains(err.Error(), "reply_text.type") {
		t.Fatalf("nested mapping drift error = %v", err)
	}
}

func TestValidateMappingSubsetAcceptsOpenSearchBooleanStrings(t *testing.T) {
	actual := map[string]any{"dynamic": "false"}
	expected := map[string]any{"dynamic": false}

	if err := validateMappingSubset(actual, expected, "mappings"); err != nil {
		t.Fatalf("semantically equal dynamic mapping rejected: %v", err)
	}
}

func TestProvisionerAdoptsCompatibleOrphanPhysicalIndex(t *testing.T) {
	owner, _ := tenant.New("app-a", "bot-a")
	alias, _ := owner.IndexAlias("events")
	physical := alias + "-v1"
	bound := false
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/_alias/"+alias:
			if !bound {
				writer.WriteHeader(http.StatusNotFound)
				_, _ = writer.Write([]byte(`{"status":404}`))
				return
			}
			_, _ = writer.Write([]byte(`{"` + physical +
				`":{"aliases":{"` + alias +
				`":{"is_write_index":true}}}}`))
		case request.Method == http.MethodPut &&
			request.URL.Path == "/"+physical:
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"resource_already_exists_exception"}`))
		case request.Method == http.MethodGet &&
			request.URL.Path == "/"+physical+"/_mapping":
			_, _ = writer.Write([]byte(`{"` + physical + `":{"mappings":{
				"_meta":{
					"schema_name":"events",
					"schema_version":"events.v1",
					"tenant_id":"` + owner.ID + `",
					"app_id":"app-a",
					"bot_open_id":"bot-a"
				},
				"properties":{
					"tenant_id":{"type":"keyword"},
					"app_id":{"type":"keyword"},
					"bot_open_id":{"type":"keyword"}
				}
			}}}`))
		case request.Method == http.MethodPost &&
			request.URL.Path == "/_aliases":
			bound = true
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
		context.Background(), owner, "events", "events.v1",
		[]byte(`{"mappings":{"properties":{}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bound || resource.Alias != alias || resource.PhysicalIndex != physical {
		t.Fatalf("adopted resource = %#v, bound=%v", resource, bound)
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
	indexMappings := make(map[string]json.RawMessage)
	createCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		mutex.Lock()
		defer mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			if strings.HasSuffix(request.URL.Path, "/_mapping") {
				physical := strings.TrimSuffix(
					strings.TrimPrefix(request.URL.Path, "/"),
					"/_mapping",
				)
				mapping, exists := indexMappings[physical]
				if !exists {
					writer.WriteHeader(http.StatusNotFound)
					_, _ = writer.Write([]byte(`{"status":404}`))
					return
				}
				encoded, encodeErr := json.Marshal(map[string]any{
					physical: map[string]any{"mappings": mapping},
				})
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				_, _ = writer.Write(encoded)
				return
			}
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
			Aliases  map[string]json.RawMessage `json:"aliases"`
			Mappings json.RawMessage            `json:"mappings"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Aliases) != 1 {
			t.Fatalf("aliases = %#v", body.Aliases)
		}
		physical := request.URL.Path[1:]
		indexMappings[physical] = append(json.RawMessage(nil), body.Mappings...)
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
