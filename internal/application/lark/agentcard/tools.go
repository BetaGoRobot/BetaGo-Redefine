package agentcard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
)

type AuthoringComposeRequest struct {
	Context         agentcardtool.ComposeContext
	Purpose         string
	InteractionMode ActionMode
	Spec            CardSpec
	ExpiresAt       time.Time
	IdempotencyKey  string
}

type AuthoringComposer interface {
	Compose(context.Context, AuthoringComposeRequest) (*CardSurface, error)
}

type ToolServiceOptions struct {
	Catalog           *Catalog
	Composer          AuthoringComposer
	Policy            PolicyConfig
	MaxRepairAttempts int
	DefaultExpiry     time.Duration
	Now               func() time.Time
}

type ToolService struct {
	catalog           *Catalog
	composer          AuthoringComposer
	policy            PolicyConfig
	maxRepairAttempts int
	defaultExpiry     time.Duration
	now               func() time.Time

	mu       sync.Mutex
	attempts map[string]int
}

func NewToolService(options ToolServiceOptions) *ToolService {
	if options.Catalog == nil {
		options.Catalog = NewCatalog()
	}
	if options.MaxRepairAttempts <= 0 {
		options.MaxRepairAttempts = 2
	}
	if options.DefaultExpiry <= 0 {
		options.DefaultExpiry = 10 * time.Minute
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &ToolService{
		catalog: options.Catalog, composer: options.Composer,
		policy: options.Policy, maxRepairAttempts: options.MaxRepairAttempts,
		defaultExpiry: options.DefaultExpiry, now: options.Now,
		attempts: make(map[string]int),
	}
}

func (s *ToolService) DiscoverComponents(
	_ context.Context,
	request agentcardtool.DiscoverRequest,
) (agentcardtool.DiscoverResponse, error) {
	if s == nil || s.catalog == nil {
		return agentcardtool.DiscoverResponse{}, errors.New(
			"agent card tool service is not configured",
		)
	}
	version := strings.TrimSpace(request.Version)
	if version == "" {
		version = VersionV1
	}
	entries := s.catalog.Discover(CatalogFilter{
		Version: version, Category: CatalogCategory(strings.TrimSpace(request.Category)),
		Name: strings.TrimSpace(request.Name),
	})
	components := make([]agentcardtool.Component, 0, len(entries))
	for _, entry := range entries {
		modes := make([]string, 0, len(entry.ActionModes))
		for _, mode := range entry.ActionModes {
			modes = append(modes, string(mode))
		}
		components = append(components, agentcardtool.Component{
			Name: entry.Name, Category: string(entry.Category),
			Version: entry.Version, Purpose: entry.Purpose,
			Fields:     append([]string(nil), entry.Fields...),
			BudgetCost: entry.BudgetCost, SafeExample: entry.SafeExample,
			DisallowedUse: entry.DisallowedUse,
			Lifecycles: append(
				[]string(nil),
				entry.LifecycleCompatibility...,
			),
			ActionModes: modes,
		})
	}
	return agentcardtool.DiscoverResponse{
		Version: version, Components: components,
	}, nil
}

func (s *ToolService) ComposeCard(
	ctx context.Context,
	toolContext agentcardtool.ComposeContext,
	request agentcardtool.ComposeRequest,
) (agentcardtool.ComposeResponse, error) {
	if s == nil || s.catalog == nil {
		return agentcardtool.ComposeResponse{}, errors.New(
			"agent card tool service is not configured",
		)
	}
	spec, decodeIssues := decodeToolCard(request.Card)
	issues := append([]ValidationIssue(nil), decodeIssues...)
	if strings.TrimSpace(request.Purpose) == "" {
		issues = append(issues, ValidationIssue{
			Code: "purpose_required", Path: "$.purpose",
		})
	}
	mode := ActionMode(strings.TrimSpace(request.Interaction.Mode))
	if mode != ActionModeUI &&
		mode != ActionModeCapabilityConfirm &&
		mode != ActionModeServer {
		issues = append(issues, ValidationIssue{
			Code: "interaction_mode_invalid", Path: "$.interaction.mode",
		})
	}
	if request.Interaction.ExpiresInSeconds != 0 &&
		(request.Interaction.ExpiresInSeconds < 60 ||
			request.Interaction.ExpiresInSeconds > 3600) {
		issues = append(issues, ValidationIssue{
			Code:  "expiry_out_of_range",
			Path:  "$.interaction.expires_in_seconds",
			Limit: 3600, Actual: request.Interaction.ExpiresInSeconds,
		})
	}
	if len(decodeIssues) == 0 {
		if spec.Meta.Purpose == "" ||
			spec.Meta.Purpose == "agent_authored" {
			spec.Meta.Purpose = strings.TrimSpace(request.Purpose)
		}
		issues = append(issues, ValidateCardSpec(spec)...)
		issues = append(issues, CheckPolicy(spec, s.policy)...)
		for index, action := range spec.Actions {
			if action.Mode != mode {
				issues = append(issues, ValidationIssue{
					Code: "interaction_mode_mismatch",
					Path: "$.card.actions[" + intString(index) + "].mode",
				})
			}
		}
	}
	if len(issues) != 0 {
		attempt := s.recordRepairAttempt(repairKey(toolContext, request))
		response := agentcardtool.ComposeResponse{
			Status: "repair_required", Attempt: attempt,
			Issues: toToolIssues(issues),
		}
		if attempt >= s.maxRepairAttempts {
			response.Status = "fallback_required"
			response.Fallback =
				"卡片连续两次未通过校验；请改用简洁文本回复，不要发送卡片，也不要请求任何密码、令牌或验证码。"
		}
		return response, nil
	}
	if s.composer == nil {
		return agentcardtool.ComposeResponse{}, errors.New(
			"agent card composer is not configured",
		)
	}
	s.clearRepairAttempt(repairKey(toolContext, request))
	expiry := time.Duration(request.Interaction.ExpiresInSeconds) * time.Second
	if expiry <= 0 {
		expiry = s.defaultExpiry
	}
	key := composeToolKey(toolContext, request)
	surface, err := s.composer.Compose(ctx, AuthoringComposeRequest{
		Context: toolContext, Purpose: strings.TrimSpace(request.Purpose),
		InteractionMode: mode, Spec: spec,
		ExpiresAt: s.now().UTC().Add(expiry), IdempotencyKey: key,
	})
	if err != nil {
		return agentcardtool.ComposeResponse{}, err
	}
	if surface == nil {
		return agentcardtool.ComposeResponse{}, errors.New(
			"agent card composer returned no surface",
		)
	}
	return agentcardtool.ComposeResponse{
		Status: string(surface.Status), CardRef: surface.ID,
		MessageID: surface.MessageID, InteractionID: surface.InteractionID,
		Revision: surface.Revision,
	}, nil
}

func decodeToolCard(input agentcardtool.Card) (CardSpec, []ValidationIssue) {
	if input.Version == "" {
		input.Version = VersionV1
	}
	if input.Meta.Purpose == "" {
		input.Meta.Purpose = "agent_authored"
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return CardSpec{}, []ValidationIssue{{
			Code: "card_document_invalid", Path: "$.card",
		}}
	}
	var spec CardSpec
	if json.Unmarshal(encoded, &spec) != nil {
		return CardSpec{}, []ValidationIssue{{
			Code: "card_document_invalid", Path: "$.card",
		}}
	}
	return spec, nil
}

func toToolIssues(input []ValidationIssue) []agentcardtool.Issue {
	result := make([]agentcardtool.Issue, 0, len(input))
	for _, issue := range input {
		result = append(result, agentcardtool.Issue{
			Code: issue.Code, Path: issue.Path,
			Limit: issue.Limit, Actual: issue.Actual,
		})
	}
	return result
}

func (s *ToolService) recordRepairAttempt(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempts[key] < s.maxRepairAttempts {
		s.attempts[key]++
	}
	return s.attempts[key]
}

func (s *ToolService) clearRepairAttempt(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attempts, key)
}

func repairKey(
	toolContext agentcardtool.ComposeContext,
	request agentcardtool.ComposeRequest,
) string {
	return strings.Join([]string{
		toolContext.ChatID,
		toolContext.ActorOpenID,
		toolContext.ReplyToMessageID,
		toolContext.TriggerEventID,
		strings.TrimSpace(request.Purpose),
	}, "\x00")
}

func composeToolKey(
	toolContext agentcardtool.ComposeContext,
	request agentcardtool.ComposeRequest,
) string {
	encoded, _ := json.Marshal(request)
	sum := sha256.Sum256(append(
		[]byte(repairKey(toolContext, request)+"\x00"),
		encoded...,
	))
	return "compose_card_" + hex.EncodeToString(sum[:])
}

func intString(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
