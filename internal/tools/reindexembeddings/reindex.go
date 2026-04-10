package reindexembeddings

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

var atMentionPattern = regexp.MustCompile(`<at[^>]*>([^<]+)</at>`)

type SourceDoc struct {
	RawMessage string    `json:"raw_message"`
	MessageV2  []float32 `json:"message_v2,omitempty"`
}

type BulkUpdate struct {
	ID     string
	Vector []float32
}

type ScanDocument struct {
	ID    string
	Index string
	Doc   SourceDoc
}

type PendingDocument struct {
	ID    string
	Index string
	Text  string
}

type AnalyzeStats struct {
	TotalDocs          int
	WithMessageField   int
	WithMessageV2Field int
	MissingMessageV2   int
}

type RunOptions struct {
	Index          string
	Model          string
	Dimensions     int
	Days           int
	DryRun         bool
	BatchSize      int
	Concurrency    int
	ScrollSize     int
	ScrollTimeout  time.Duration
	RequestTimeout time.Duration
	ExpectedTotal  int
}

type RunSummary struct {
	Processed       int
	Updated         int
	Errors          int
	SkippedNoText   int
	SkippedExisting int
	PromptTokens    int
	TotalTokens     int
}

func ShouldProcessDocument(doc SourceDoc) bool {
	return strings.TrimSpace(doc.RawMessage) != "" && len(doc.MessageV2) == 0
}

func ExtractText(raw string) string {
	stripped := strings.TrimSpace(raw)
	if stripped == "" {
		return ""
	}

	if looksLikeJSONObject(stripped) {
		var obj any
		if err := json.Unmarshal([]byte(stripped), &obj); err == nil {
			return cleanAtMentions(extractFromJSON(obj))
		}
	}

	return cleanAtMentions(stripped)
}

func BuildScanQuery(days int) []byte {
	mustClauses := []any{
		map[string]any{
			"exists": map[string]any{
				"field": "raw_message",
			},
		},
	}
	if days > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02T15:04:05")
		mustClauses = append(mustClauses, map[string]any{
			"range": map[string]any{
				"create_time_v2": map[string]any{
					"gte": cutoff,
				},
			},
		})
	}

	query := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must": mustClauses,
				"must_not": []any{
					map[string]any{
						"exists": map[string]any{
							"field": "message_v2",
						},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(query)
	return data
}

func BuildBulkUpdatePayload(updates []BulkUpdate) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, update := range updates {
		meta := map[string]any{
			"update": map[string]any{
				"_id": update.ID,
			},
		}
		doc := map[string]any{
			"doc": map[string]any{
				"message_v2": update.Vector,
			},
		}
		if err := enc.Encode(meta); err != nil {
			return nil, fmt.Errorf("encode bulk meta: %w", err)
		}
		if err := enc.Encode(doc); err != nil {
			return nil, fmt.Errorf("encode bulk doc: %w", err)
		}
	}
	return buf.Bytes(), nil
}

func BuildEmbeddingRequests(modelName string, dimensions int, texts []string) []model.MultiModalEmbeddingRequest {
	requests := make([]model.MultiModalEmbeddingRequest, 0, len(texts))
	for _, text := range texts {
		requests = append(requests, model.MultiModalEmbeddingRequest{
			Input: []model.MultimodalEmbeddingInput{{
				Type: model.MultiModalEmbeddingInputTypeText,
				Text: &text,
			}},
			Model:      modelName,
			Dimensions: &dimensions,
		})
	}
	return requests
}

func CreateOpenSearchClient(cfg *infraConfig.OpensearchConfig) (*opensearchapi.Client, error) {
	if cfg == nil || cfg.Domain == "" {
		return nil, fmt.Errorf("opensearch config missing")
	}
	os.Setenv("NO_PROXY", "*")
	os.Setenv("no_proxy", "*")

	return opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			Addresses: []string{
				"https://" + cfg.Domain + ":9200",
			},
			Username: cfg.User,
			Password: cfg.Password,
		},
	})
}

func CreateArkClient(apiKey string, timeout time.Duration) *arkruntime.Client {
	options := []arkruntime.ConfigOption{
		arkruntime.WithTimeout(timeout),
	}
	if baseURL := strings.TrimSpace(os.Getenv("ARK_BASE_URL")); baseURL != "" {
		options = append(options, arkruntime.WithBaseUrl(baseURL))
	}
	return arkruntime.NewClientWithApiKey(apiKey, options...)
}

func AnalyzeIndex(ctx context.Context, client *opensearchapi.Client, index string) (AnalyzeStats, map[string]any, error) {
	totalResp, err := client.Indices.Stats(ctx, &opensearchapi.IndicesStatsReq{
		Indices: []string{index},
	})
	if err != nil {
		return AnalyzeStats{}, nil, err
	}

	var totalDocs int
	for _, item := range totalResp.Indices {
		totalDocs += item.Primaries.Docs.Count
	}

	withMessageField, err := countByQuery(ctx, client, index, map[string]any{
		"query": map[string]any{
			"exists": map[string]any{
				"field": "message",
			},
		},
	})
	if err != nil {
		return AnalyzeStats{}, nil, err
	}
	withMessageV2Field, err := countByQuery(ctx, client, index, map[string]any{
		"query": map[string]any{
			"exists": map[string]any{
				"field": "message_v2",
			},
		},
	})
	if err != nil {
		return AnalyzeStats{}, nil, err
	}
	missingMessageV2, err := countByQuery(ctx, client, index, map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must": []any{
					map[string]any{
						"exists": map[string]any{
							"field": "raw_message",
						},
					},
				},
				"must_not": []any{
					map[string]any{
						"exists": map[string]any{
							"field": "message_v2",
						},
					},
				},
			},
		},
	})
	if err != nil {
		return AnalyzeStats{}, nil, err
	}

	mappingResp, err := client.Indices.Mapping.Get(ctx, &opensearchapi.MappingGetReq{
		Indices: []string{index},
	})
	if err != nil {
		return AnalyzeStats{
			TotalDocs:          totalDocs,
			WithMessageField:   withMessageField,
			WithMessageV2Field: withMessageV2Field,
			MissingMessageV2:   missingMessageV2,
		}, nil, nil
	}

	mappingInfo := make(map[string]any)
	for _, item := range mappingResp.Indices {
		var raw map[string]any
		if err := json.Unmarshal(item.Mappings, &raw); err == nil {
			mappingInfo = raw
		}
		break
	}

	return AnalyzeStats{
		TotalDocs:          totalDocs,
		WithMessageField:   withMessageField,
		WithMessageV2Field: withMessageV2Field,
		MissingMessageV2:   missingMessageV2,
	}, mappingInfo, nil
}

func Run(ctx context.Context, osClient *opensearchapi.Client, arkClient *arkruntime.Client, opts RunOptions) (RunSummary, error) {
	if opts.Index == "" {
		return RunSummary{}, fmt.Errorf("index is required")
	}
	if opts.Model == "" && !opts.DryRun {
		return RunSummary{}, fmt.Errorf("model is required")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 50
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 32
	}
	if opts.ScrollSize <= 0 {
		opts.ScrollSize = 500
	}
	if opts.ScrollTimeout <= 0 {
		opts.ScrollTimeout = 5 * time.Minute
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 24 * time.Hour
	}

	var summary RunSummary
	pendingByIndex := make(map[string][]PendingDocument)
	visitedTargets := 0

	printProgress := func(forceNewline bool) {
		if opts.ExpectedTotal <= 0 {
			return
		}
		const width = 30
		done := visitedTargets
		if done > opts.ExpectedTotal {
			done = opts.ExpectedTotal
		}
		filled := 0
		if opts.ExpectedTotal > 0 {
			filled = done * width / opts.ExpectedTotal
		}
		bar := strings.Repeat("=", filled)
		if filled < width {
			bar += ">"
			bar += strings.Repeat(".", width-filled-1)
		}
		if filled >= width {
			bar = strings.Repeat("=", width)
		}
		msg := fmt.Sprintf("\rProgress [%s] %d/%d processed=%d updated=%d errors=%d skipped_no_text=%d",
			bar, done, opts.ExpectedTotal, summary.Processed, summary.Updated, summary.Errors, summary.SkippedNoText)
		msg += fmt.Sprintf(" prompt_tokens=%d total_tokens=%d", summary.PromptTokens, summary.TotalTokens)
		fmt.Print(msg)
		if forceNewline || done == opts.ExpectedTotal {
			fmt.Println()
		}
	}

	flush := func() error {
		if len(pendingByIndex) == 0 {
			return nil
		}

		indices := make([]string, 0, len(pendingByIndex))
		for index := range pendingByIndex {
			indices = append(indices, index)
		}
		sort.Strings(indices)

		for _, index := range indices {
			pending := pendingByIndex[index]
			updated, errs, promptTokens, totalTokens := processPendingBatch(ctx, osClient, arkClient, index, opts, pending)
			summary.Updated += updated
			summary.Errors += errs
			summary.PromptTokens += promptTokens
			summary.TotalTokens += totalTokens
		}
		clear(pendingByIndex)
		printProgress(false)
		return nil
	}

	err := scanDocuments(ctx, osClient, opts, func(item ScanDocument) error {
		if !ShouldProcessDocument(item.Doc) {
			summary.SkippedExisting++
			return nil
		}
		visitedTargets++
		text := ExtractText(item.Doc.RawMessage)
		if text == "" {
			summary.SkippedNoText++
			printProgress(false)
			return nil
		}
		summary.Processed++
		pendingByIndex[item.Index] = append(pendingByIndex[item.Index], PendingDocument{
			ID:    item.ID,
			Index: item.Index,
			Text:  text,
		})

		totalPending := 0
		for _, docs := range pendingByIndex {
			totalPending += len(docs)
		}
		if totalPending >= opts.BatchSize {
			return flush()
		}
		printProgress(false)
		return nil
	})
	if err != nil {
		return summary, err
	}
	if err := flush(); err != nil {
		return summary, err
	}
	printProgress(true)

	return summary, nil
}

func cleanAtMentions(text string) string {
	return atMentionPattern.ReplaceAllString(text, "@$1")
}

func looksLikeJSONObject(s string) bool {
	if strings.HasPrefix(s, "[") && len(s) < 30 && !strings.Contains(s, ",") && !strings.Contains(s, "{") {
		return false
	}
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

func extractFromJSON(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := extractFromJSON(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if hasOnlyKeys(typed, "file_key", "image_key") {
			return ""
		}
		if title, ok := typed["title"].(string); ok && strings.TrimSpace(title) != "" {
			return strings.TrimSpace(title)
		}
		if text, ok := typed["text"].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		if content, ok := typed["content"]; ok {
			if text := extractFromJSON(content); text != "" {
				return text
			}
		}

		parts := make([]string, 0, len(typed))
		for _, value := range typed {
			if text := extractFromJSON(value); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func hasOnlyKeys(m map[string]any, allowed ...string) bool {
	if len(m) == 0 {
		return false
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key := range m {
		if _, ok := allowedSet[key]; !ok {
			return false
		}
	}
	return true
}

func countByQuery(ctx context.Context, client *opensearchapi.Client, index string, query map[string]any) (int, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return 0, fmt.Errorf("marshal count query: %w", err)
	}
	resp, err := client.Indices.Count(ctx, &opensearchapi.IndicesCountReq{
		Indices: []string{index},
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		return 0, err
	}
	return resp.Count, nil
}

func scanDocuments(ctx context.Context, client *opensearchapi.Client, opts RunOptions, yield func(ScanDocument) error) error {
	body := map[string]any{}
	if err := json.Unmarshal(BuildScanQuery(opts.Days), &body); err != nil {
		return fmt.Errorf("build scan query: %w", err)
	}
	body["_source"] = []string{"raw_message", "message_v2"}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal scan body: %w", err)
	}

	resp, err := client.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{opts.Index},
		Body:    bytes.NewReader(data),
		Params: opensearchapi.SearchParams{
			Scroll: opts.ScrollTimeout,
			Size:   intPtr(opts.ScrollSize),
		},
	})
	if err != nil {
		return err
	}

	scrollID := resp.ScrollID
	for {
		for _, hit := range resp.Hits.Hits {
			var doc SourceDoc
			if err := json.Unmarshal(hit.Source, &doc); err != nil {
				return fmt.Errorf("unmarshal source %s: %w", hit.ID, err)
			}
			if err := yield(ScanDocument{
				ID:    hit.ID,
				Index: hit.Index,
				Doc:   doc,
			}); err != nil {
				return err
			}
		}

		if scrollID == nil || len(resp.Hits.Hits) == 0 {
			break
		}
		next, err := client.Scroll.Get(ctx, opensearchapi.ScrollGetReq{
			ScrollID: *scrollID,
			Params: opensearchapi.ScrollGetParams{
				Scroll: opts.ScrollTimeout,
			},
		})
		if err != nil {
			return err
		}
		resp.ScrollID = next.ScrollID
		resp.Hits.Hits = next.Hits.Hits
		scrollID = next.ScrollID
		if len(next.Hits.Hits) == 0 {
			break
		}
	}

	if scrollID != nil && *scrollID != "" {
		_, _ = client.Scroll.Delete(ctx, opensearchapi.ScrollDeleteReq{
			ScrollIDs: []string{*scrollID},
		})
	}
	return nil
}

func processPendingBatch(ctx context.Context, osClient *opensearchapi.Client, arkClient *arkruntime.Client, index string, opts RunOptions, pending []PendingDocument) (int, int, int, int) {
	if len(pending) == 0 {
		return 0, 0, 0, 0
	}
	if opts.DryRun {
		for _, item := range pending {
			fmt.Printf("  [%s] %q\n", item.ID, preview(item.Text, 80))
		}
		return len(pending), 0, 0, 0
	}

	texts := make([]string, 0, len(pending))
	for _, item := range pending {
		texts = append(texts, item.Text)
	}
	vectors, errs, promptTokens, totalTokens := batchEmbed(ctx, arkClient, opts, texts)

	updates := make([]BulkUpdate, 0, len(pending))
	errorCount := errs
	for i, vector := range vectors {
		if len(vector) == 0 {
			errorCount++
			continue
		}
		updates = append(updates, BulkUpdate{
			ID:     pending[i].ID,
			Vector: vector,
		})
	}
	if len(updates) == 0 {
		return 0, errorCount, promptTokens, totalTokens
	}

	payload, err := BuildBulkUpdatePayload(updates)
	if err != nil {
		return 0, len(pending), promptTokens, totalTokens
	}
	resp, err := osClient.Bulk(ctx, opensearchapi.BulkReq{
		Index: index,
		Body:  bytes.NewReader(payload),
	})
	if err != nil {
		return 0, len(pending), promptTokens, totalTokens
	}

	bulkErrors := 0
	for _, item := range resp.Items {
		for _, result := range item {
			if result.Error != nil || result.Status >= 300 {
				bulkErrors++
			}
		}
	}
	return len(updates) - bulkErrors, errorCount + bulkErrors, promptTokens, totalTokens
}

func batchEmbed(ctx context.Context, client *arkruntime.Client, opts RunOptions, texts []string) ([][]float32, int, int, int) {
	if len(texts) == 0 {
		return nil, 0, 0, 0
	}

	requests := BuildEmbeddingRequests(opts.Model, opts.Dimensions, texts)
	results := make([][]float32, len(requests))
	type job struct {
		index int
		req   model.MultiModalEmbeddingRequest
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	errors := 0
	promptTokens := 0
	totalTokens := 0

	workerCount := opts.Concurrency
	if workerCount <= 0 {
		workerCount = 1
	}
	if workerCount > len(requests) {
		workerCount = len(requests)
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				reqCtx, cancel := context.WithTimeout(ctx, opts.RequestTimeout)
				resp, err := client.CreateMultiModalEmbeddings(reqCtx, item.req)
				cancel()

				mu.Lock()
				if err != nil || len(resp.Data.Embedding) == 0 {
					errors++
				} else {
					results[item.index] = resp.Data.Embedding
					promptTokens += resp.Usage.PromptTokens
					totalTokens += resp.Usage.TotalTokens
				}
				mu.Unlock()
			}
		}()
	}

	for i, req := range requests {
		jobs <- job{index: i, req: req}
	}
	close(jobs)
	wg.Wait()

	return results, errors, promptTokens, totalTokens
}

func intPtr(v int) *int {
	return &v
}

func preview(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}
