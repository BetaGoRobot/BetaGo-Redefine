package chatflow

import (
	"iter"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

// InitialChatToolOutput is the normalized tool result fed back into the next model turn.
type InitialChatToolOutput struct {
	CallID string
	Output string
}

// InitialChatTurnRequest describes one turn inside the initial chat loop.
type InitialChatTurnRequest struct {
	Plan               InitialChatExecutionPlan
	PreviousResponseID string
	ToolOutput         *InitialChatToolOutput
	AdditionalTools    *arktools.Impl[larkim.P2MessageReceiveV1]
}

// InitialChatToolCall carries chat flow state.
type InitialChatToolCall struct {
	CallID       string
	FunctionName string
	Arguments    string
}

// InitialChatTurnSnapshot carries chat flow state.
type InitialChatTurnSnapshot struct {
	ResponseID string
	ToolCall   *InitialChatToolCall
}

// InitialChatTurnResult carries chat flow state.
type InitialChatTurnResult struct {
	Stream   iter.Seq[*ark_dal.ModelStreamRespReasoning]
	Snapshot func() InitialChatTurnSnapshot
}

// InitialChatGenerationRequest is the planner input for the first model turn.
type InitialChatGenerationRequest struct {
	Event           *larkim.P2MessageReceiveV1
	ModelID         string
	ReasoningEffort responses.ReasoningEffort_Enum
	Size            int
	Files           []string
	Input           []string
	Tools           *arktools.Impl[larkim.P2MessageReceiveV1]
}

// InitialChatExecutionPlan is the fully materialized first-turn execution plan.
type InitialChatExecutionPlan struct {
	Event           *larkim.P2MessageReceiveV1
	ModelID         string
	ReasoningEffort responses.ReasoningEffort_Enum
	ChatID          string
	OpenID          string
	Prompt          string
	UserInput       string
	MaxToolTurns    int
	Files           []string
	Tools           *arktools.Impl[larkim.P2MessageReceiveV1]
	MessageList     history.OpensearchMsgLogList
}
