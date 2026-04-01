package replay

import (
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
)

type ReplayCaseName string

const (
	ReplayCaseBaseline  ReplayCaseName = "baseline"
	ReplayCaseAugmented ReplayCaseName = "augmented"
)

type ReplayReport struct {
	Target             ReplayTarget             `json:"target"`
	RuntimeObservation ReplayRuntimeObservation `json:"runtime_observation"`
	Cases              []ReplayCase             `json:"cases"`
	Diff               ReplayDiff               `json:"diff"`
}

type ReplayTarget struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	OpenID    string `json:"open_id"`
	ChatType  string `json:"chat_type"`
	Text      string `json:"text"`
}

type ReplayRuntimeObservation struct {
	Mentioned          bool   `json:"mentioned"`
	ReplyToBot         bool   `json:"reply_to_bot"`
	TriggerType        string `json:"trigger_type"`
	EligibleForAgentic bool   `json:"eligible_for_agentic"`
}

type ReplayCase struct {
	Name                 ReplayCaseName         `json:"name"`
	IntentContextEnabled bool                   `json:"intent_context_enabled"`
	HistoryLimit         int                    `json:"history_limit"`
	ProfileLimit         int                    `json:"profile_limit"`
	IntentInput          string                 `json:"intent_input"`
	IntentContext        ReplayIntentContext    `json:"intent_context"`
	IntentAnalysis       *intent.IntentAnalysis `json:"intent_analysis,omitempty"`
	RouteDecision        *ReplayRouteDecision   `json:"route_decision,omitempty"`
	Conversation         *ReplayConversation    `json:"conversation,omitempty"`
}

type ReplayIntentContext struct {
	HistoryLines []string `json:"history_lines"`
	ProfileLines []string `json:"profile_lines"`
}

type ReplayRouteDecision struct {
	FinalMode         string `json:"final_mode"`
	ForcedByRuntime   bool   `json:"forced_by_runtime"`
	ForcedDirectReply bool   `json:"forced_direct_reply"`
}

type ReplayConversation struct {
	Mode         string                    `json:"mode"`
	Prompt       string                    `json:"prompt,omitempty"`
	UserInput    string                    `json:"user_input,omitempty"`
	MaxToolTurns int                       `json:"max_tool_turns,omitempty"`
	ToolIntent   *ReplayToolIntent         `json:"tool_intent,omitempty"`
	Output       *ReplayConversationOutput `json:"output,omitempty"`
}

type ReplayToolIntent struct {
	WouldCallTools bool   `json:"would_call_tools"`
	CallID         string `json:"call_id,omitempty"`
	FunctionName   string `json:"function_name,omitempty"`
	Arguments      string `json:"arguments,omitempty"`
}

type ReplayConversationOutput struct {
	Decision             string `json:"decision,omitempty"`
	Thought              string `json:"thought,omitempty"`
	Reply                string `json:"reply,omitempty"`
	ReferenceFromWeb     string `json:"reference_from_web,omitempty"`
	ReferenceFromHistory string `json:"reference_from_history,omitempty"`
	RawContent           string `json:"raw_content,omitempty"`
	ReasoningContent     string `json:"reasoning_content,omitempty"`
}

type ReplayDiff struct {
	IntentInputChanged     bool     `json:"intent_input_changed"`
	InteractionModeChanged bool     `json:"interaction_mode_changed"`
	RouteChanged           bool     `json:"route_changed"`
	GenerationChanged      bool     `json:"generation_changed"`
	ToolIntentChanged      bool     `json:"tool_intent_changed"`
	ChangedFields          []string `json:"changed_fields"`
}

func (d ReplayDiff) ChangedFieldNames() []string {
	if len(d.ChangedFields) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(d.ChangedFields))
	out := make([]string, 0, len(d.ChangedFields))
	for _, field := range d.ChangedFields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func (r ReplayReport) RenderText() string {
	var builder strings.Builder

	builder.WriteString("Target\n")
	builder.WriteString(fmt.Sprintf("chat_id: %s\n", strings.TrimSpace(r.Target.ChatID)))
	builder.WriteString(fmt.Sprintf("message_id: %s\n", strings.TrimSpace(r.Target.MessageID)))
	builder.WriteString(fmt.Sprintf("open_id: %s\n", strings.TrimSpace(r.Target.OpenID)))
	builder.WriteString(fmt.Sprintf("chat_type: %s\n", strings.TrimSpace(r.Target.ChatType)))
	builder.WriteString(fmt.Sprintf("text: %s\n", strings.TrimSpace(r.Target.Text)))

	for _, item := range r.Cases {
		builder.WriteString("\n")
		builder.WriteString(strings.Title(string(item.Name)))
		builder.WriteString("\n")
		builder.WriteString(fmt.Sprintf("intent_context_enabled: %t\n", item.IntentContextEnabled))
		builder.WriteString(fmt.Sprintf("history_limit: %d\n", item.HistoryLimit))
		builder.WriteString(fmt.Sprintf("profile_limit: %d\n", item.ProfileLimit))
		builder.WriteString(fmt.Sprintf("intent_input:\n%s\n", strings.TrimSpace(item.IntentInput)))
		if len(item.IntentContext.HistoryLines) > 0 {
			builder.WriteString("history_lines:\n")
			builder.WriteString(strings.Join(item.IntentContext.HistoryLines, "\n"))
			builder.WriteString("\n")
		}
		if len(item.IntentContext.ProfileLines) > 0 {
			builder.WriteString("profile_lines:\n")
			builder.WriteString(strings.Join(item.IntentContext.ProfileLines, "\n"))
			builder.WriteString("\n")
		}
		if item.IntentAnalysis == nil {
			builder.WriteString("intent_analysis: <dry-run>\n")
		} else {
			builder.WriteString(fmt.Sprintf("intent_analysis.intent_type: %s\n", item.IntentAnalysis.IntentType))
			builder.WriteString(fmt.Sprintf("intent_analysis.need_reply: %t\n", item.IntentAnalysis.NeedReply))
			builder.WriteString(fmt.Sprintf("intent_analysis.interaction_mode: %s\n", item.IntentAnalysis.InteractionMode))
			builder.WriteString(fmt.Sprintf("intent_analysis.needs_history: %t\n", item.IntentAnalysis.NeedsHistory))
			builder.WriteString(fmt.Sprintf("intent_analysis.needs_web: %t\n", item.IntentAnalysis.NeedsWeb))
		}
		if item.RouteDecision == nil {
			builder.WriteString("route_decision: <dry-run>\n")
		} else {
			builder.WriteString(fmt.Sprintf("route_decision.final_mode: %s\n", item.RouteDecision.FinalMode))
		}
		if item.Conversation == nil {
			builder.WriteString("conversation: <dry-run>\n")
		} else {
			builder.WriteString(fmt.Sprintf("conversation.mode: %s\n", item.Conversation.Mode))
			builder.WriteString(fmt.Sprintf("conversation.prompt:\n%s\n", strings.TrimSpace(item.Conversation.Prompt)))
			builder.WriteString(fmt.Sprintf("conversation.user_input:\n%s\n", strings.TrimSpace(item.Conversation.UserInput)))
			if item.Conversation.ToolIntent != nil {
				builder.WriteString(fmt.Sprintf("conversation.tool_intent.function_name: %s\n", item.Conversation.ToolIntent.FunctionName))
			}
			if item.Conversation.Output != nil {
				builder.WriteString(fmt.Sprintf("conversation.output.decision: %s\n", item.Conversation.Output.Decision))
				builder.WriteString(fmt.Sprintf("conversation.output.reply: %s\n", item.Conversation.Output.Reply))
			}
		}
	}

	builder.WriteString("\nDiff Summary\n")
	builder.WriteString(fmt.Sprintf("intent_input_changed: %t\n", r.Diff.IntentInputChanged))
	builder.WriteString(fmt.Sprintf("interaction_mode_changed: %t\n", r.Diff.InteractionModeChanged))
	builder.WriteString(fmt.Sprintf("route_changed: %t\n", r.Diff.RouteChanged))
	builder.WriteString(fmt.Sprintf("generation_changed: %t\n", r.Diff.GenerationChanged))
	builder.WriteString(fmt.Sprintf("tool_intent_changed: %t\n", r.Diff.ToolIntentChanged))
	if changed := r.Diff.ChangedFieldNames(); len(changed) > 0 {
		builder.WriteString("changed_fields:\n")
		builder.WriteString(strings.Join(changed, "\n"))
		builder.WriteString("\n")
	}

	return builder.String()
}
