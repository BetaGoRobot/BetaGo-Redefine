package mememilvus

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func analyzer() map[string]any {
	return map[string]any{"tokenizer": "jieba", "filter": []string{"lowercase"}}
}
func (c *Client) collectionSchema(name string, field memes.Field) map[string]any {
	textParams := map[string]any{"max_length": 65535}
	if field != memes.FieldDescription {
		textParams["enable_analyzer"] = true
		textParams["analyzer_params"] = analyzer()
	}
	schemaFields := []map[string]any{
		{"fieldName": "use_id", "dataType": "VarChar", "isPrimary": true, "elementTypeParams": map[string]any{"max_length": 1024}},
		{"fieldName": "fingerprint", "dataType": "VarChar", "elementTypeParams": map[string]any{"max_length": 64}},
		{"fieldName": "text", "dataType": "VarChar", "elementTypeParams": textParams},
		{"fieldName": "vector", "dataType": "FloatVector", "elementTypeParams": map[string]any{"dim": c.options.Dimensions}},
		{"fieldName": "relation_kinds", "dataType": "Array", "elementDataType": "VarChar", "elementTypeParams": map[string]any{"max_capacity": 128, "max_length": 1024}},
	}
	schema := map[string]any{"autoId": false, "enableDynamicField": false, "fields": schemaFields}
	indexes := []map[string]any{{"fieldName": "vector", "indexName": "vector", "indexType": "HNSW", "metricType": "L2", "params": map[string]any{"M": 16, "efConstruction": 128}}}
	if field != memes.FieldDescription {
		schemaFields = append(schemaFields, map[string]any{"fieldName": "sparse", "dataType": "SparseFloatVector"})
		schema["fields"] = schemaFields
		schema["functions"] = []map[string]any{{"name": "text_bm25", "type": "BM25", "inputFieldNames": []string{"text"}, "outputFieldNames": []string{"sparse"}, "params": map[string]any{}}}
		indexes = append(indexes, map[string]any{"fieldName": "sparse", "indexName": "sparse", "indexType": "SPARSE_INVERTED_INDEX", "metricType": "BM25", "params": map[string]any{"inverted_index_algo": "DAAT_MAXSCORE", "bm25_k1": 1.2, "bm25_b": .75}})
	}
	return map[string]any{"collectionName": name, "schema": schema, "indexParams": indexes, "params": map[string]any{"consistencyLevel": "Strong", "shardsNum": 1}}
}
func (c *Client) has(ctx context.Context, name string) (bool, error) {
	var out struct {
		Has *bool `json:"has"`
	}
	if e := c.request(ctx, "collections/has", map[string]any{"collectionName": name}, &out); e != nil {
		return false, e
	}
	if out.Has == nil {
		return false, ErrResponse
	}
	return *out.Has, nil
}
func (c *Client) checkSchema(ctx context.Context, name string, field memes.Field) error {
	var out struct {
		CollectionName string `json:"collectionName"`
		Dynamic        bool   `json:"enableDynamicField"`
		AutoID         bool   `json:"autoId"`
		Fields         []struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			Primary bool   `json:"primaryKey"`
			Params  []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"params"`
		} `json:"fields"`
		Indexes []struct {
			Field  string `json:"fieldName"`
			Metric string `json:"metricType"`
		} `json:"indexes"`
	}
	if e := c.request(ctx, "collections/describe", map[string]any{"collectionName": name}, &out); e != nil {
		return e
	}
	expected := map[string]string{"use_id": "VarChar", "fingerprint": "VarChar", "text": "VarChar", "vector": "FloatVector", "relation_kinds": "Array"}
	if field != memes.FieldDescription {
		expected["sparse"] = "SparseFloatVector"
	}
	if out.CollectionName != name || out.Dynamic || out.AutoID || len(out.Fields) != len(expected) {
		return ErrConflict
	}
	for _, f := range out.Fields {
		if expected[f.Name] != f.Type {
			return ErrConflict
		}
		if (f.Name == "use_id") != f.Primary {
			return ErrConflict
		}
		params := map[string]string{}
		for _, p := range f.Params {
			params[p.Key] = p.Value
		}
		if f.Name == "vector" && params["dim"] != strconv.Itoa(c.options.Dimensions) {
			return ErrConflict
		}
		if f.Name == "text" && field != memes.FieldDescription {
			if params["enable_analyzer"] != "true" {
				return ErrConflict
			}
			var a map[string]any
			if json.Unmarshal([]byte(params["analyzer_params"]), &a) != nil {
				return ErrConflict
			}
			actual, _ := memes.Fingerprint(a)
			want, _ := memes.Fingerprint(analyzer())
			if actual != want {
				return ErrConflict
			}
		}
		delete(expected, f.Name)
	}
	if len(expected) != 0 {
		return ErrConflict
	}
	metrics := map[string]string{}
	for _, idx := range out.Indexes {
		metrics[idx.Field] = idx.Metric
	}
	if metrics["vector"] != "L2" || (field != memes.FieldDescription && metrics["sparse"] != "BM25") {
		return ErrConflict
	}
	return nil
}
func (c *Client) ensureCollection(ctx context.Context, generation, name string, field memes.Field, sealed bool) error {
	found, e := c.has(ctx, name)
	if e != nil {
		return e
	}
	if sealed {
		if !found {
			return ErrConflict
		}
		return c.checkSchema(ctx, name, field)
	}
	key := generation + ":milvus:create:" + name
	var intent map[string]any
	previous, e := c.get(ctx, key, &intent)
	if e != nil {
		return e
	}
	if found && !previous {
		return ErrConflict
	}
	if !previous {
		if e = c.put(ctx, key, c.collectionSchema(name, field)); e != nil {
			return e
		}
	}
	if !found {
		writeErr := c.request(ctx, "collections/create", c.collectionSchema(name, field), nil)
		found, e = c.has(ctx, name)
		if e != nil {
			return e
		}
		if !found {
			if writeErr != nil {
				return writeErr
			}
			return ErrResponse
		}
	}
	if e = c.checkSchema(ctx, name, field); e != nil {
		return e
	}
	return c.request(ctx, "collections/load", map[string]any{"collectionName": name}, nil)
}
func (c *Client) readDocuments(ctx context.Context, name string, ids []string) (map[string]document, error) {
	raw, _ := json.Marshal(ids)
	var out []document
	if e := c.request(ctx, "entities/query", map[string]any{"collectionName": name, "filter": "use_id in " + string(raw), "outputFields": []string{"use_id", "fingerprint", "text", "vector", "relation_kinds"}, "limit": len(ids), "consistencyLevel": "Strong"}, &out); e != nil {
		return nil, e
	}
	docs := map[string]document{}
	for _, d := range out {
		if !slices.Contains(ids, d.UseID) || docs[d.UseID].UseID != "" || !validVector(d.Vector, c.options.Dimensions) {
			return nil, ErrConflict
		}
		docs[d.UseID] = d
	}
	return docs, nil
}
func (c *Client) count(ctx context.Context, name string) (int, error) {
	var out []map[string]json.RawMessage
	if e := c.request(ctx, "entities/query", map[string]any{"collectionName": name, "filter": "", "outputFields": []string{"count(*)"}, "consistencyLevel": "Strong"}, &out); e != nil {
		return 0, e
	}
	if len(out) != 1 {
		return 0, ErrResponse
	}
	var n int
	if json.Unmarshal(out[0]["count(*)"], &n) != nil || n < 0 {
		return 0, ErrResponse
	}
	return n, nil
}
func batchKey(generation, name string, docs []document) string {
	fp, _ := memes.Fingerprint(docs)
	return fmt.Sprintf("%s:milvus:insert:%s:%s", generation, name, fp)
}
