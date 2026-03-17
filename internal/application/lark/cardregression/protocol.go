package cardregression

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
)

type BuiltCardMode string

const (
	BuiltCardModeTemplate BuiltCardMode = "template"
	BuiltCardModeCardJSON BuiltCardMode = "card_json"
)

type BuiltCard struct {
	Mode         BuiltCardMode
	Label        string
	TemplateID   string
	TemplateName string
	TemplateCard *larktpl.TemplateCardContent
	CardJSON     map[string]any
}

type ReceiveTarget struct {
	ReceiveIDType string
	ReceiveID     string
}

func (t ReceiveTarget) String() string {
	return fmt.Sprintf("%s=%s", strings.TrimSpace(t.ReceiveIDType), strings.TrimSpace(t.ReceiveID))
}

func (t ReceiveTarget) Valid() error {
	if strings.TrimSpace(t.ReceiveIDType) == "" {
		return fmt.Errorf("receive_id_type is required")
	}
	if strings.TrimSpace(t.ReceiveID) == "" {
		return fmt.Errorf("receive_id is required")
	}
	return nil
}

type CardBusinessContext struct {
	ChatID       string
	ActorOpenID  string
	TargetOpenID string
	MessageID    string
	Scope        string
	ObjectID     string
}

type CardBuildRequest struct {
	Business CardBusinessContext
	Args     map[string]string
}

type CardRequirementSet struct {
	NeedBusinessChatID  bool
	NeedActorOpenID     bool
	NeedTargetOpenID    bool
	NeedObjectID        bool
	NeedDB              bool
	NeedRedis           bool
	NeedFeatureRegistry bool
	NeedExternalIO      bool
}

type CardRegressionCase struct {
	Name        string
	Description string
	Args        map[string]string
	Requires    CardRequirementSet
	Tags        []string
}

type TestCardBuildRequest struct {
	Business CardBusinessContext
	Case     CardRegressionCase
	Args     map[string]string
	DryRun   bool
}

type CardSceneMeta struct {
	Name        string
	Description string
	Tags        []string
	Owner       string
}

type CardSceneProtocol interface {
	SceneKey() string
	Meta() CardSceneMeta
	BuildCard(ctx context.Context, req CardBuildRequest) (*BuiltCard, error)
	TestCases() []CardRegressionCase
	BuildTestCard(ctx context.Context, req TestCardBuildRequest) (*BuiltCard, error)
}

func mergeArgs(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	for k, v := range override {
		merged[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return merged
}

func hasTag(tags []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return true
	}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == want {
			return true
		}
	}
	return false
}

func sortedCaseNames(cases []CardRegressionCase) []string {
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		if name := strings.TrimSpace(c.Name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
