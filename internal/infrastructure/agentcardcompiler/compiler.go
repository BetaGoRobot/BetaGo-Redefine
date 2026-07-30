package agentcardcompiler

import (
	"encoding/json"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

type Compiler struct{}

func New() *Compiler {
	return &Compiler{}
}

func (c *Compiler) Compile(bound *agentcard.BoundCardSpec) (larkmsg.RawCard, error) {
	if bound == nil {
		return nil, fmt.Errorf("bound card spec is required")
	}
	spec := bound.Spec()
	elements, err := compileBlocks(spec.Blocks)
	if err != nil {
		return nil, err
	}
	if bound.State() == agentcard.LifecycleInteractive {
		actionElements, compileErr := compileActions(bound, spec.Actions)
		if compileErr != nil {
			return nil, compileErr
		}
		elements = append(elements, actionElements...)
	} else {
		elements = append(elements, lifecycleElement(bound.State()))
	}
	return larkmsg.NewCardV2(spec.Title, elements, larkmsg.CardV2Options{
		HeaderTemplate:  themeTemplate(spec.Theme),
		VerticalSpacing: "8px",
		Padding:         "12px",
	}), nil
}

func (c *Compiler) CompileRedacted(
	bound *agentcard.BoundCardSpec,
) (larkmsg.RawCard, error) {
	card, err := c.Compile(bound)
	if err != nil {
		return nil, err
	}
	return redactCard(card).(larkmsg.RawCard), nil
}

func (c *Compiler) CompileJSON(
	bound *agentcard.BoundCardSpec,
) (json.RawMessage, error) {
	card, err := c.Compile(bound)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		return nil, fmt.Errorf("marshal compiled agent card: %w", err)
	}
	return encoded, nil
}

func (c *Compiler) CompileRedactedJSON(
	bound *agentcard.BoundCardSpec,
) (json.RawMessage, error) {
	card, err := c.CompileRedacted(bound)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		return nil, fmt.Errorf("marshal redacted agent card: %w", err)
	}
	return encoded, nil
}

func themeTemplate(theme agentcard.Theme) string {
	switch theme {
	case agentcard.ThemeGreen:
		return "green"
	case agentcard.ThemeOrange:
		return "orange"
	case agentcard.ThemeRed:
		return "red"
	case agentcard.ThemeGrey:
		return "grey"
	default:
		return "blue"
	}
}

func lifecycleElement(state agentcard.LifecycleState) any {
	text := map[agentcard.LifecycleState]string{
		agentcard.LifecycleSubmitted:  "已提交，正在进入处理队列。",
		agentcard.LifecycleProcessing: "处理中，请稍候。",
		agentcard.LifecycleResolved:   "已完成。",
		agentcard.LifecycleCancelled:  "已取消。",
		agentcard.LifecycleExpired:    "该交互已过期。",
		agentcard.LifecycleFailed:     "处理失败，请稍后重试。",
	}[state]
	return larkmsg.HintMarkdown(text)
}

func redactCard(value any) any {
	switch typed := value.(type) {
	case larkmsg.RawCard:
		result := make(larkmsg.RawCard, len(typed))
		for key, item := range typed {
			result[key] = redactCardValue(key, item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = redactCardValue(key, item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactCard(item)
		}
		return result
	default:
		return value
	}
}

func redactCardValue(key string, value any) any {
	if key == "token" {
		return "[REDACTED]"
	}
	return redactCard(value)
}
