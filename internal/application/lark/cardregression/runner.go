package cardregression

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type SuiteName string

const (
	SuiteSmoke     SuiteName = "smoke"
	SuiteLiveSmoke SuiteName = "live-smoke"
	SuiteSendSmoke SuiteName = "send-smoke"
)

type ErrorKind string

const (
	ErrorKindValidation ErrorKind = "validation_error"
	ErrorKindBuild      ErrorKind = "build_error"
	ErrorKindSend       ErrorKind = "send_error"
)

type RegressionResult struct {
	SceneKey   string    `json:"scene_key"`
	CaseName   string    `json:"case_name"`
	Built      bool      `json:"built"`
	Sent       bool      `json:"sent"`
	MessageID  string    `json:"message_id,omitempty"`
	Error      string    `json:"error,omitempty"`
	ErrorKind  ErrorKind `json:"error_kind,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type Sender interface {
	Send(ctx context.Context, target ReceiveTarget, built *BuiltCard) (messageID string, err error)
}

type Runner struct {
	registry *Registry
	sender   Sender
}

type RunSceneOptions struct {
	SceneKey string
	CaseName string
	Args     map[string]string
	Business CardBusinessContext
	Target   *ReceiveTarget
	DryRun   bool
}

type RunSuiteOptions struct {
	Suite    SuiteName
	Args     map[string]string
	Business CardBusinessContext
	Target   *ReceiveTarget
	DryRun   bool
	FailFast bool
}

func NewRunner(registry *Registry, sender Sender) *Runner {
	if registry == nil {
		registry = DefaultRegistry()
	}
	return &Runner{registry: registry, sender: sender}
}

func (r *Runner) RunScene(ctx context.Context, opts RunSceneOptions) (RegressionResult, error) {
	result := RegressionResult{SceneKey: strings.TrimSpace(opts.SceneKey), StartedAt: time.Now()}
	defer func() { result.FinishedAt = time.Now() }()
	if r == nil || r.registry == nil {
		result.ErrorKind = ErrorKindValidation
		result.Error = "runner registry is nil"
		return result, nil
	}

	scene, ok := r.registry.Get(result.SceneKey)
	if !ok {
		result.ErrorKind = ErrorKindValidation
		result.Error = fmt.Sprintf("scene %q is not registered", result.SceneKey)
		return result, nil
	}
	selectedCase, err := resolveCase(scene.TestCases(), opts.CaseName)
	if err != nil {
		result.ErrorKind = ErrorKindValidation
		result.Error = err.Error()
		return result, nil
	}
	result.CaseName = selectedCase.Name

	if err := validateRequirements(selectedCase.Requires, opts.Business); err != nil {
		result.ErrorKind = ErrorKindValidation
		result.Error = err.Error()
		return result, nil
	}

	buildReq := TestCardBuildRequest{
		Business: opts.Business,
		Case: CardRegressionCase{
			Name:        selectedCase.Name,
			Description: selectedCase.Description,
			Args:        mergeArgs(selectedCase.Args, opts.Args),
			Requires:    selectedCase.Requires,
			Tags:        append([]string(nil), selectedCase.Tags...),
		},
		Args:   mergeArgs(selectedCase.Args, opts.Args),
		DryRun: opts.DryRun,
	}
	built, err := scene.BuildTestCard(ctx, buildReq)
	if err != nil {
		result.ErrorKind = ErrorKindBuild
		result.Error = err.Error()
		return result, nil
	}
	result.Built = built != nil
	if opts.DryRun || opts.Target == nil {
		return result, nil
	}
	if err := opts.Target.Valid(); err != nil {
		result.ErrorKind = ErrorKindValidation
		result.Error = err.Error()
		return result, nil
	}
	if r.sender == nil {
		result.ErrorKind = ErrorKindValidation
		result.Error = "runner sender is nil"
		return result, nil
	}
	messageID, err := r.sender.Send(ctx, *opts.Target, built)
	if err != nil {
		result.ErrorKind = ErrorKindSend
		result.Error = err.Error()
		return result, nil
	}
	result.Sent = true
	result.MessageID = strings.TrimSpace(messageID)
	return result, nil
}

func (r *Runner) RunSuite(ctx context.Context, opts RunSuiteOptions) ([]RegressionResult, error) {
	if r == nil || r.registry == nil {
		return nil, nil
	}
	filterTag := suiteTag(opts.Suite)
	results := make([]RegressionResult, 0)
	for _, scene := range r.registry.List() {
		for _, c := range sortedCasesForTag(scene.TestCases(), filterTag) {
			result, err := r.RunScene(ctx, RunSceneOptions{
				SceneKey: scene.SceneKey(),
				CaseName: c.Name,
				Args:     opts.Args,
				Business: opts.Business,
				Target:   opts.Target,
				DryRun:   opts.DryRun,
			})
			if err != nil {
				return results, err
			}
			results = append(results, result)
			if opts.FailFast && result.Error != "" && !softFailure(opts.Suite, result) {
				return results, nil
			}
		}
	}
	return results, nil
}

func resolveCase(cases []CardRegressionCase, caseName string) (CardRegressionCase, error) {
	if strings.TrimSpace(caseName) == "" {
		caseName = "smoke-default"
	}
	for _, c := range cases {
		if strings.TrimSpace(c.Name) == strings.TrimSpace(caseName) {
			return c, nil
		}
	}
	return CardRegressionCase{}, fmt.Errorf("case %q not found; available cases: %s", caseName, strings.Join(sortedCaseNames(cases), ", "))
}

func validateRequirements(req CardRequirementSet, business CardBusinessContext) error {
	missing := make([]string, 0, 4)
	if req.NeedBusinessChatID && strings.TrimSpace(business.ChatID) == "" {
		missing = append(missing, "business chat_id")
	}
	if req.NeedActorOpenID && strings.TrimSpace(business.ActorOpenID) == "" {
		missing = append(missing, "actor_open_id")
	}
	if req.NeedTargetOpenID && strings.TrimSpace(business.TargetOpenID) == "" {
		missing = append(missing, "target_open_id")
	}
	if req.NeedObjectID && strings.TrimSpace(business.ObjectID) == "" {
		missing = append(missing, "object_id")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required context: %s", strings.Join(missing, ", "))
}

func suiteTag(suite SuiteName) string {
	switch suite {
	case SuiteLiveSmoke:
		return "live"
	case SuiteSmoke, SuiteSendSmoke, "":
		return "smoke"
	default:
		return string(suite)
	}
}

func sortedCasesForTag(cases []CardRegressionCase, tag string) []CardRegressionCase {
	filtered := make([]CardRegressionCase, 0, len(cases))
	for _, c := range cases {
		if hasTag(c.Tags, tag) {
			filtered = append(filtered, c)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return strings.TrimSpace(filtered[i].Name) < strings.TrimSpace(filtered[j].Name)
	})
	return filtered
}

func softFailure(suite SuiteName, result RegressionResult) bool {
	return suite == SuiteLiveSmoke && result.ErrorKind == ErrorKindValidation
}
