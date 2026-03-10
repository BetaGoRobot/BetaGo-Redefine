package history

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/defensestation/osquery"
)

func TestHelperBuildRequestMatchesBuilderState(t *testing.T) {
	helper := New(context.Background()).
		Index("custom-index").
		Query(osquery.Bool().Must(osquery.Term("chat_id", "chat-1"))).
		Source("raw_message", "user_id").
		Size(5).
		Sort("create_time", "desc")

	reqJSON := utils.MustMarshalString(helper.buildRequest())

	for _, fragment := range []string{
		`"chat_id"`,
		`"chat-1"`,
		`"raw_message"`,
		`"user_id"`,
		`"size":5`,
		`"create_time"`,
		`"desc"`,
	} {
		if !strings.Contains(reqJSON, fragment) {
			t.Fatalf("request JSON %q does not contain %q", reqJSON, fragment)
		}
	}
}

func TestHelperResolvedIndexPrefersExplicitIndex(t *testing.T) {
	helper := New(context.Background()).Index("custom-index")

	if got := helper.resolvedIndex(); got != "custom-index" {
		t.Fatalf("resolvedIndex() = %q, want %q", got, "custom-index")
	}
}
