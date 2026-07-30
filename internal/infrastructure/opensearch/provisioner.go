package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

const maxIndexNameLength = 255

type TenantIndex struct {
	Alias         string
	PhysicalIndex string
}

type Provisioner struct {
	client *opensearchapi.Client
}

func NewProvisioner() (*Provisioner, error) {
	live, ok := backend.(liveBackend)
	if !ok || live.client == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, backend.Reason())
	}
	return NewProvisionerWithClient(live.client)
}

func NewProvisionerWithClient(
	client *opensearchapi.Client,
) (*Provisioner, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: provisioner client is required", ErrUnavailable)
	}
	return &Provisioner{client: client}, nil
}

func (p *Provisioner) EnsureTenantIndex(
	ctx context.Context,
	owner tenant.Tenant,
	baseAlias string,
	schemaVersion string,
	mapping []byte,
) (TenantIndex, error) {
	if p == nil || p.client == nil {
		return TenantIndex{}, fmt.Errorf("%w: provisioner is not configured", ErrUnavailable)
	}
	if err := owner.Validate(); err != nil {
		return TenantIndex{}, err
	}
	if strings.TrimSpace(schemaVersion) == "" ||
		strings.TrimSpace(schemaVersion) != schemaVersion {
		return TenantIndex{}, fmt.Errorf("opensearch schema version is invalid")
	}
	alias, err := owner.IndexAlias(baseAlias)
	if err != nil {
		return TenantIndex{}, err
	}
	if existing, found, err := p.aliasTarget(ctx, alias); err != nil {
		return TenantIndex{}, err
	} else if found {
		if err := p.validateIndex(
			ctx,
			existing,
			owner,
			schemaVersion,
			mapping,
		); err != nil {
			return TenantIndex{}, err
		}
		return TenantIndex{Alias: alias, PhysicalIndex: existing}, nil
	}

	physical := physicalIndexName(alias)
	body, err := tenantIndexBody(owner, alias, schemaVersion, mapping)
	if err != nil {
		return TenantIndex{}, err
	}
	status, err := p.perform(ctx, http.MethodPut, "/"+url.PathEscape(physical), body)
	if err != nil {
		return TenantIndex{}, fmt.Errorf("create tenant index %q: %w", physical, err)
	}
	if status < 200 || status >= 300 {
		// A concurrent replica may have won the deterministic create. The alias
		// is the source of truth; accept the race only after reading it back.
		if existing, found, lookupErr := p.aliasTarget(ctx, alias); lookupErr == nil && found {
			if validateErr := p.validateIndex(
				ctx,
				existing,
				owner,
				schemaVersion,
				mapping,
			); validateErr != nil {
				return TenantIndex{}, validateErr
			}
			return TenantIndex{Alias: alias, PhysicalIndex: existing}, nil
		}
		if validateErr := p.validateIndex(
			ctx,
			physical,
			owner,
			schemaVersion,
			mapping,
		); validateErr != nil {
			return TenantIndex{}, fmt.Errorf(
				"create tenant index %q returned HTTP %d and existing index is incompatible: %w",
				physical,
				status,
				validateErr,
			)
		}
		if bindErr := p.bindWriteAlias(ctx, physical, alias); bindErr != nil {
			return TenantIndex{}, bindErr
		}
	}
	existing, found, err := p.aliasTarget(ctx, alias)
	if err != nil {
		return TenantIndex{}, err
	}
	if !found {
		return TenantIndex{}, fmt.Errorf(
			"tenant alias %q is missing after index creation",
			alias,
		)
	}
	if err := p.validateIndex(
		ctx,
		existing,
		owner,
		schemaVersion,
		mapping,
	); err != nil {
		return TenantIndex{}, err
	}
	return TenantIndex{Alias: alias, PhysicalIndex: existing}, nil
}

func (p *Provisioner) bindWriteAlias(
	ctx context.Context,
	physical string,
	alias string,
) error {
	body, err := json.Marshal(map[string]any{
		"actions": []any{
			map[string]any{
				"add": map[string]any{
					"index":          physical,
					"alias":          alias,
					"is_write_index": true,
				},
			},
		},
	})
	if err != nil {
		return err
	}
	status, err := p.perform(ctx, http.MethodPost, "/_aliases", body)
	if err != nil {
		return fmt.Errorf("bind tenant alias %q: %w", alias, err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("bind tenant alias %q returned HTTP %d", alias, status)
	}
	return nil
}

func (p *Provisioner) validateIndex(
	ctx context.Context,
	index string,
	owner tenant.Tenant,
	schemaVersion string,
	expectedMapping []byte,
) error {
	path := "/" + url.PathEscape(index) + "/_mapping"
	status, body, err := p.performWithResponse(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("inspect tenant index %q mapping: %w", index, err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf(
			"inspect tenant index %q mapping returned HTTP %d",
			index,
			status,
		)
	}
	var response map[string]struct {
		Mappings map[string]any `json:"mappings"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode tenant index %q mapping: %w", index, err)
	}
	entry, ok := response[index]
	if !ok || entry.Mappings == nil {
		return fmt.Errorf("tenant index %q mapping response is incomplete", index)
	}
	meta, ok := entry.Mappings["_meta"].(map[string]any)
	if !ok {
		return fmt.Errorf("tenant index %q mapping metadata is missing", index)
	}
	schemaName := strings.SplitN(schemaVersion, ".", 2)[0]
	for field, want := range map[string]string{
		"schema_name":    schemaName,
		"schema_version": schemaVersion,
		"tenant_id":      owner.ID,
		"app_id":         owner.AppID,
		"bot_open_id":    owner.BotOpenID,
	} {
		got, _ := meta[field].(string)
		if got != want {
			return fmt.Errorf(
				"tenant index %q mapping %s mismatch: got=%q want=%q",
				index,
				field,
				got,
				want,
			)
		}
	}
	properties, ok := entry.Mappings["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("tenant index %q mapping properties are missing", index)
	}
	for _, field := range []string{"tenant_id", "app_id", "bot_open_id"} {
		definition, ok := properties[field].(map[string]any)
		if !ok || definition["type"] != "keyword" {
			return fmt.Errorf(
				"tenant index %q mapping field %s must be keyword",
				index,
				field,
			)
		}
	}
	required, err := mappingDefinition(expectedMapping)
	if err != nil {
		return err
	}
	if err := validateMappingSubset(entry.Mappings, required, "mappings"); err != nil {
		return fmt.Errorf("tenant index %q mapping is incompatible: %w", index, err)
	}
	return nil
}

func (p *Provisioner) aliasTarget(
	ctx context.Context,
	alias string,
) (string, bool, error) {
	path := "/_alias/" + url.PathEscape(alias)
	status, body, err := p.performWithResponse(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", false, fmt.Errorf("inspect tenant alias %q: %w", alias, err)
	}
	if status == http.StatusNotFound {
		return "", false, nil
	}
	if status < 200 || status >= 300 {
		return "", false, fmt.Errorf(
			"inspect tenant alias %q returned HTTP %d",
			alias,
			status,
		)
	}
	var indices map[string]struct {
		Aliases map[string]struct {
			IsWriteIndex *bool `json:"is_write_index"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal(body, &indices); err != nil {
		return "", false, fmt.Errorf("decode tenant alias %q: %w", alias, err)
	}
	if len(indices) != 1 {
		return "", false, fmt.Errorf(
			"tenant alias %q must target exactly one index, got %d",
			alias,
			len(indices),
		)
	}
	for index, value := range indices {
		definition, ok := value.Aliases[alias]
		if !ok {
			return "", false, fmt.Errorf(
				"tenant alias %q response does not contain the alias",
				alias,
			)
		}
		if definition.IsWriteIndex != nil && !*definition.IsWriteIndex {
			return "", false, fmt.Errorf(
				"tenant alias %q is not writable",
				alias,
			)
		}
		return index, true, nil
	}
	return "", false, nil
}

func (p *Provisioner) perform(
	ctx context.Context,
	method string,
	path string,
	body []byte,
) (int, error) {
	status, _, err := p.performWithResponse(ctx, method, path, body)
	return status, err
}

func (p *Provisioner) performWithResponse(
	ctx context.Context,
	method string,
	path string,
	body []byte,
) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := p.client.Client.Perform(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, responseBody, nil
}

func tenantIndexBody(
	owner tenant.Tenant,
	alias string,
	schemaVersion string,
	mapping []byte,
) ([]byte, error) {
	var body map[string]any
	if len(mapping) == 0 || json.Unmarshal(mapping, &body) != nil {
		return nil, fmt.Errorf("opensearch mapping must be a JSON object")
	}
	mappings, ok := body["mappings"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("opensearch mapping is missing mappings")
	}
	properties, ok := mappings["properties"].(map[string]any)
	if !ok {
		properties = make(map[string]any)
		mappings["properties"] = properties
	}
	for field, definition := range map[string]any{
		"tenant_id":   map[string]any{"type": "keyword"},
		"app_id":      map[string]any{"type": "keyword"},
		"bot_open_id": map[string]any{"type": "keyword"},
	} {
		properties[field] = definition
	}
	mappings["_meta"] = map[string]any{
		"schema_name":    strings.SplitN(schemaVersion, ".", 2)[0],
		"schema_version": schemaVersion,
		"tenant_id":      owner.ID,
		"app_id":         owner.AppID,
		"bot_open_id":    owner.BotOpenID,
	}
	body["aliases"] = map[string]any{
		alias: map[string]any{"is_write_index": true},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode tenant index mapping: %w", err)
	}
	return encoded, nil
}

func mappingDefinition(mapping []byte) (map[string]any, error) {
	var body struct {
		Mappings map[string]any `json:"mappings"`
	}
	if len(mapping) == 0 || json.Unmarshal(mapping, &body) != nil {
		return nil, fmt.Errorf("opensearch mapping must be a JSON object")
	}
	if body.Mappings == nil {
		return nil, fmt.Errorf("opensearch mapping is missing mappings")
	}
	return body.Mappings, nil
}

func validateMappingSubset(actual, expected any, path string) error {
	switch expectedValue := expected.(type) {
	case map[string]any:
		actualValue, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		for key, childExpected := range expectedValue {
			childActual, found := actualValue[key]
			childPath := path + "." + key
			if !found {
				return fmt.Errorf("%s is missing", childPath)
			}
			if err := validateMappingSubset(
				childActual,
				childExpected,
				childPath,
			); err != nil {
				return err
			}
		}
		return nil
	default:
		if !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf(
				"%s mismatch: got=%v want=%v",
				path,
				actual,
				expected,
			)
		}
		return nil
	}
}

func physicalIndexName(alias string) string {
	const suffix = "-v1"
	maxAliasLength := maxIndexNameLength - len(suffix)
	if len(alias) > maxAliasLength {
		alias = strings.TrimRight(alias[:maxAliasLength], "-_")
	}
	return alias + suffix
}
