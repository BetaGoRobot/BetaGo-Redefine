package meili

import (
	"context"
	"errors"
	"net/http"
)

// Initialize creates missing indexes and waits for every asynchronous settings
// update. Existing indexes must use the stable scope/source primary key "id".
func (c *Client) Initialize(ctx context.Context) error {
	ctx, cancel := c.operation(ctx)
	defer cancel()
	for _, index := range []string{c.messages, c.contexts} {
		if err := c.ensureIndex(ctx, index); err != nil {
			return err
		}
		filter := []string{"tenant_id", "app_id", "chat_id", "visibility", "scope_hash", "occurred_sec", "occurred_nano", "updated_sec", "updated_nano"}
		settings := map[string]any{"searchableAttributes": []string{"text"}, "typoTolerance": map[string]bool{"enabled": false}, "prefixSearch": "disabled", "sortableAttributes": []string{"occurred_sec", "occurred_nano", "updated_sec", "updated_nano"}}
		if index == c.messages {
			// Effective word counts must not hide ignored query stop words.
			settings["stopWords"] = []string{}
			filter = append(filter, "author_id", "author_hash", "message_id")
		} else {
			filter = append(filter, "author_ids", "author_hashes", "embedding_model", "invalidated")
			settings["embedders"] = map[string]any{"external": map[string]any{"source": "userProvided", "dimensions": c.dimensions}}
		}
		settings["filterableAttributes"] = filter
		if err := c.mutate(ctx, http.MethodPatch, "/indexes/"+index+"/settings", settings); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) ensureIndex(ctx context.Context, index string) error {
	var info struct {
		PrimaryKey string `json:"primaryKey"`
	}
	err := c.request(ctx, http.MethodGet, "/indexes/"+index, nil, &info)
	if err == nil {
		if info.PrimaryKey != "id" {
			return errors.New("meili index primary key mismatch")
		}
		return nil
	}
	var response *responseError
	if !errors.As(err, &response) || response.status != http.StatusNotFound || response.code != "index_not_found" {
		return err
	}
	err = c.mutate(ctx, http.MethodPost, "/indexes", map[string]string{"uid": index, "primaryKey": "id"})
	if err == nil {
		return nil
	}
	var task *taskFailure
	// A concurrent initializer may win creation. Only that exact server code is
	// recoverable, and the winning index's key is checked before using it.
	if (errors.As(err, &task) && task.code == "index_already_exists") || (errors.As(err, &response) && response.code == "index_already_exists") {
		if err = c.request(ctx, http.MethodGet, "/indexes/"+index, nil, &info); err != nil {
			return err
		}
		if info.PrimaryKey != "id" {
			return errors.New("meili index primary key mismatch")
		}
		return nil
	}
	return err
}
