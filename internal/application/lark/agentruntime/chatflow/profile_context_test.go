package chatflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

type fakeChatflowProfileAccessor struct {
	readEnabled  bool
	snippetLimit int
}

func (f fakeChatflowProfileAccessor) ChatflowProfileReadEnabled() bool { return f.readEnabled }
func (f fakeChatflowProfileAccessor) ChatflowProfileSnippetLimit() int { return f.snippetLimit }

func TestResolveChatflowProfileContextLinesSkipsWhenReadDisabled(t *testing.T) {
	originalAccessorBuilder := chatflowProfileAccessorBuilder
	originalLoader := chatflowProfileContextLoader
	defer func() {
		chatflowProfileAccessorBuilder = originalAccessorBuilder
		chatflowProfileContextLoader = originalLoader
	}()

	loaderCalled := 0
	chatflowProfileAccessorBuilder = func(context.Context, string, string) chatflowProfileConfigAccessor {
		return fakeChatflowProfileAccessor{readEnabled: false, snippetLimit: 3}
	}
	chatflowProfileContextLoader = func(context.Context, chatflowProfileContextRequest) ([]string, error) {
		loaderCalled++
		return []string{"profile: x"}, nil
	}

	got := resolveChatflowProfileContextLines(context.Background(), "oc_chat", "ou_user", chatflowProfileContextRequest{})
	if len(got) != 0 {
		t.Fatalf("lines = %+v, want empty", got)
	}
	if loaderCalled != 0 {
		t.Fatalf("loader called %d times, want 0", loaderCalled)
	}
}

func TestResolveChatflowProfileContextLinesAppliesTrimDedupAndLimit(t *testing.T) {
	originalAccessorBuilder := chatflowProfileAccessorBuilder
	originalLoader := chatflowProfileContextLoader
	defer func() {
		chatflowProfileAccessorBuilder = originalAccessorBuilder
		chatflowProfileContextLoader = originalLoader
	}()

	chatflowProfileAccessorBuilder = func(context.Context, string, string) chatflowProfileConfigAccessor {
		return fakeChatflowProfileAccessor{readEnabled: true, snippetLimit: 2}
	}
	chatflowProfileContextLoader = func(context.Context, chatflowProfileContextRequest) ([]string, error) {
		return []string{"  profile: role=pm ", "", "profile: role=pm", "profile: interest=finance", "profile: style=concise"}, nil
	}

	got := resolveChatflowProfileContextLines(context.Background(), "oc_chat", "ou_user", chatflowProfileContextRequest{})
	want := []string{"profile: role=pm", "profile: interest=finance"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %+v, want %+v", got, want)
	}
}

func TestResolveChatflowProfileContextLinesUsesConservativeLimitForReplyScoped(t *testing.T) {
	originalAccessorBuilder := chatflowProfileAccessorBuilder
	originalLoader := chatflowProfileContextLoader
	defer func() {
		chatflowProfileAccessorBuilder = originalAccessorBuilder
		chatflowProfileContextLoader = originalLoader
	}()

	chatflowProfileAccessorBuilder = func(context.Context, string, string) chatflowProfileConfigAccessor {
		return fakeChatflowProfileAccessor{readEnabled: true, snippetLimit: 3}
	}
	chatflowProfileContextLoader = func(context.Context, chatflowProfileContextRequest) ([]string, error) {
		return []string{"profile: role=moderator", "profile: pref=table"}, nil
	}

	got := resolveChatflowProfileContextLines(context.Background(), "oc_chat", "ou_user", chatflowProfileContextRequest{ReplyScoped: true})
	want := []string{"profile: role=moderator"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %+v, want %+v", got, want)
	}
}

func TestResolveChatflowProfileContextLinesSkipsOnLoaderError(t *testing.T) {
	originalAccessorBuilder := chatflowProfileAccessorBuilder
	originalLoader := chatflowProfileContextLoader
	defer func() {
		chatflowProfileAccessorBuilder = originalAccessorBuilder
		chatflowProfileContextLoader = originalLoader
	}()

	chatflowProfileAccessorBuilder = func(context.Context, string, string) chatflowProfileConfigAccessor {
		return fakeChatflowProfileAccessor{readEnabled: true, snippetLimit: 3}
	}
	chatflowProfileContextLoader = func(context.Context, chatflowProfileContextRequest) ([]string, error) {
		return nil, errors.New("unavailable")
	}

	got := resolveChatflowProfileContextLines(context.Background(), "oc_chat", "ou_user", chatflowProfileContextRequest{})
	if len(got) != 0 {
		t.Fatalf("lines = %+v, want empty", got)
	}
}

func TestDefaultChatflowProfileContextLoaderSkipsWithoutIndex(t *testing.T) {
	originalIndexResolver := chatflowProfileIndexResolver
	originalSearch := chatflowProfileSearch
	defer func() {
		chatflowProfileIndexResolver = originalIndexResolver
		chatflowProfileSearch = originalSearch
	}()

	searchCalled := 0
	chatflowProfileIndexResolver = func(context.Context, string, string) string { return "" }
	chatflowProfileSearch = func(context.Context, string, any) (*opensearchapi.SearchResp, error) {
		searchCalled++
		return nil, nil
	}

	lines, err := defaultChatflowProfileContextLoader(context.Background(), chatflowProfileContextRequest{
		ChatID:       "oc_chat",
		TargetOpenID: "ou_user",
	})
	if err != nil {
		t.Fatalf("defaultChatflowProfileContextLoader() error = %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("lines = %+v, want empty", lines)
	}
	if searchCalled != 0 {
		t.Fatalf("searchCalled = %d, want 0", searchCalled)
	}
}

func TestDefaultChatflowProfileContextLoaderBuildsProfileLinesFromHits(t *testing.T) {
	originalIndexResolver := chatflowProfileIndexResolver
	originalSearch := chatflowProfileSearch
	defer func() {
		chatflowProfileIndexResolver = originalIndexResolver
		chatflowProfileSearch = originalSearch
	}()

	chatflowProfileIndexResolver = func(context.Context, string, string) string { return "profile_idx" }
	chatflowProfileSearch = func(_ context.Context, _ string, query any) (*opensearchapi.SearchResp, error) {
		queryText := strings.ToLower(fmt.Sprintf("%v", query))
		if !strings.Contains(queryText, "oc_chat") || !strings.Contains(queryText, "ou_user") {
			t.Fatalf("query = %v, want contain chat/user filter", query)
		}
		return &opensearchapi.SearchResp{
			Hits: opensearchapi.SearchHits{
				Hits: []opensearchapi.SearchHit{
					{Source: []byte(`{"facet":"role","canonical_value":"moderator","confidence":0.92}`)},
					{Source: []byte(`{"facet":"interest","value":"finance"}`)},
					{Source: []byte(`{"facet":"noise","value":"joke","confidence":0.10}`)},
					{Source: []byte(`{"status":"deleted","facet":"role","canonical_value":"old"}`)},
				},
			},
		}, nil
	}

	lines, err := defaultChatflowProfileContextLoader(context.Background(), chatflowProfileContextRequest{
		ChatID:       "oc_chat",
		TargetOpenID: "ou_user",
	})
	if err != nil {
		t.Fatalf("defaultChatflowProfileContextLoader() error = %v", err)
	}
	want := []string{"画像线索: role=moderator (0.92)", "画像线索: interest=finance"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %+v, want %+v", lines, want)
	}
}

func TestProfileContextLineFromDocFiltersLowConfidenceAndDeletedState(t *testing.T) {
	cases := []struct {
		name string
		doc  map[string]any
		want string
	}{
		{
			name: "normal facet and value",
			doc:  map[string]any{"facet": "role", "canonical_value": "owner", "confidence": 0.8},
			want: "画像线索: role=owner (0.80)",
		},
		{
			name: "missing facet falls back to value",
			doc:  map[string]any{"value": "偏好简短回复"},
			want: "画像线索: 偏好简短回复",
		},
		{
			name: "deleted state filtered",
			doc:  map[string]any{"state": "deleted", "value": "x"},
			want: "",
		},
		{
			name: "low confidence filtered",
			doc:  map[string]any{"facet": "role", "value": "joker", "confidence": 0.2},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := profileContextLineFromDoc(tc.doc); got != tc.want {
				t.Fatalf("profileContextLineFromDoc() = %q, want %q", got, tc.want)
			}
		})
	}
}
