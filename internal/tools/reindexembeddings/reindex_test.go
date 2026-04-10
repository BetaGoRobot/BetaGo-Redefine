package reindexembeddings

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestExtractText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "plain text preserved",
			raw:  "hello world",
			want: "hello world",
		},
		{
			name: "at mention normalized",
			raw:  `<at user_id="ou_xxx">张三</at> 你好`,
			want: "@张三 你好",
		},
		{
			name: "short bracket message treated as plain text",
			raw:  "[流泪]",
			want: "[流泪]",
		},
		{
			name: "card title extracted",
			raw:  `{"title":"日报","foo":"bar"}`,
			want: "日报",
		},
		{
			name: "text field extracted",
			raw:  `{"text":"正文内容"}`,
			want: "正文内容",
		},
		{
			name: "post content extracts nested text",
			raw:  `{"content":[[{"tag":"text","text":"第一段"},{"tag":"at","user_id":"u1","text":"小明"}],[{"tag":"text","text":"第二段"}]]}`,
			want: "第一段 小明 第二段",
		},
		{
			name: "file only skipped",
			raw:  `{"file_key":"file-123"}`,
			want: "",
		},
		{
			name: "image only skipped",
			raw:  `{"image_key":"img-123"}`,
			want: "",
		},
		{
			name: "invalid json falls back to raw text",
			raw:  `{"text":"oops"`,
			want: `{"text":"oops"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractText(tt.raw); got != tt.want {
				t.Fatalf("ExtractText(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestShouldProcessDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  SourceDoc
		want bool
	}{
		{
			name: "raw message with missing vector should process",
			doc: SourceDoc{
				RawMessage: "hello",
			},
			want: true,
		},
		{
			name: "raw message with empty vector should process",
			doc: SourceDoc{
				RawMessage: "hello",
				MessageV2:  []float32{},
			},
			want: true,
		},
		{
			name: "missing raw message skipped",
			doc: SourceDoc{
				MessageV2: nil,
			},
			want: false,
		},
		{
			name: "existing vector skipped",
			doc: SourceDoc{
				RawMessage: "hello",
				MessageV2:  []float32{0.1, 0.2},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ShouldProcessDocument(tt.doc); got != tt.want {
				t.Fatalf("ShouldProcessDocument(%+v) = %v, want %v", tt.doc, got, tt.want)
			}
		})
	}
}

func TestBuildScanQuery(t *testing.T) {
	t.Parallel()

	got := BuildScanQuery(7)

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("BuildScanQuery() returned invalid json: %v", err)
	}

	query, ok := decoded["query"].(map[string]any)
	if !ok {
		t.Fatalf("query missing: %#v", decoded)
	}
	boolPart, ok := query["bool"].(map[string]any)
	if !ok {
		t.Fatalf("bool missing: %#v", query)
	}
	must, ok := boolPart["must"].([]any)
	if !ok {
		t.Fatalf("must missing: %#v", boolPart)
	}
	mustNot, ok := boolPart["must_not"].([]any)
	if !ok {
		t.Fatalf("must_not missing: %#v", boolPart)
	}

	if len(must) != 2 {
		t.Fatalf("must len = %d, want 2", len(must))
	}
	if len(mustNot) != 1 {
		t.Fatalf("must_not len = %d, want 1", len(mustNot))
	}
}

func TestBuildBulkUpdatePayload(t *testing.T) {
	t.Parallel()

	ops := []BulkUpdate{
		{ID: "doc-1", Vector: []float32{1, 2}},
		{ID: "doc-2", Vector: []float32{3, 4}},
	}

	got, err := BuildBulkUpdatePayload(ops)
	if err != nil {
		t.Fatalf("BuildBulkUpdatePayload() error = %v", err)
	}

	want := "" +
		"{\"update\":{\"_id\":\"doc-1\"}}\n" +
		"{\"doc\":{\"message_v2\":[1,2]}}\n" +
		"{\"update\":{\"_id\":\"doc-2\"}}\n" +
		"{\"doc\":{\"message_v2\":[3,4]}}\n"
	if string(got) != want {
		t.Fatalf("payload = %q, want %q", string(got), want)
	}
}

func TestBuildEmbeddingRequests(t *testing.T) {
	t.Parallel()

	dims := 2048
	got := BuildEmbeddingRequests("ep-test", dims, []string{"a", "b"})

	if len(got) != 2 {
		t.Fatalf("len(requests) = %d, want 2", len(got))
	}
	if !reflect.DeepEqual(got[0].Input, []string{"a"}) {
		t.Fatalf("request[0].input = %#v, want %#v", got[0].Input, []string{"a"})
	}
	if !reflect.DeepEqual(got[1].Input, []string{"b"}) {
		t.Fatalf("request[1].input = %#v, want %#v", got[1].Input, []string{"b"})
	}
	if got[0].Model != "ep-test" {
		t.Fatalf("model = %q, want %q", got[0].Model, "ep-test")
	}
	if got[0].Dimensions != dims {
		t.Fatalf("dimensions = %d, want %d", got[0].Dimensions, dims)
	}
}
