package llmusage

import (
	"strings"
	"time"
	"unicode/utf8"
)

const labelMaxLen = 64

type SourceType string

const (
	SourceTypeUser       SourceType = "user"
	SourceTypeBackground SourceType = "background"
	SourceTypeSystem     SourceType = "system"
	SourceTypeDebug      SourceType = "debug"
)

type BusinessScene string

const (
	SceneConversation BusinessScene = "conversation"
	SceneCommand      BusinessScene = "command"
	SceneRouting      BusinessScene = "routing"
	SceneRetrieval    BusinessScene = "retrieval"
	SceneAgentRuntime BusinessScene = "agent_runtime"
	SceneEvaluation   BusinessScene = "evaluation"
	SceneBackground   BusinessScene = "background"
	SceneDebug        BusinessScene = "debug"
	SceneUnknown      BusinessScene = "unknown"
)

func (s BusinessScene) Valid() bool {
	switch s {
	case SceneConversation, SceneCommand, SceneRouting, SceneRetrieval,
		SceneAgentRuntime, SceneEvaluation, SceneBackground, SceneDebug:
		return true
	default:
		return false
	}
}

type BusinessOperation string

const (
	OperationChatReply            BusinessOperation = "chat_reply"
	OperationMentionReply         BusinessOperation = "mention_reply"
	OperationP2PReply             BusinessOperation = "p2p_reply"
	OperationCommandChat          BusinessOperation = "command_chat"
	OperationCommandHandler       BusinessOperation = "command_handler"
	OperationIntentRecognition    BusinessOperation = "intent_recognition"
	OperationToolPlanning         BusinessOperation = "tool_planning"
	OperationActivation           BusinessOperation = "activation"
	OperationRelevance            BusinessOperation = "relevance"
	OperationHistorySearch        BusinessOperation = "history_search"
	OperationTopicRecall          BusinessOperation = "topic_recall"
	OperationRetrieverEmbedding   BusinessOperation = "retriever_embedding"
	OperationRetrieverRecall      BusinessOperation = "retriever_recall"
	OperationRetrieverAnswer      BusinessOperation = "retriever_answer"
	OperationCallbackContinuation BusinessOperation = "callback_continuation"
	OperationCandidateGeneration  BusinessOperation = "candidate_generation"
	OperationJudge                BusinessOperation = "judge"
	OperationMessageEmbedding     BusinessOperation = "message_embedding"
	OperationOutboundEmbedding    BusinessOperation = "outbound_embedding"
	OperationChunkMerge           BusinessOperation = "chunk_merge"
	OperationChunkEmbedding       BusinessOperation = "chunk_embedding"
	OperationReindexEmbedding     BusinessOperation = "reindex_embedding"
	OperationDebugImage           BusinessOperation = "debug_image"
	OperationDebugConversation    BusinessOperation = "debug_conversation"
	OperationUnknown              BusinessOperation = "unknown"
)

func (o BusinessOperation) Valid() bool {
	switch o {
	case OperationChatReply, OperationMentionReply, OperationP2PReply,
		OperationCommandChat, OperationCommandHandler, OperationIntentRecognition,
		OperationToolPlanning, OperationActivation, OperationRelevance,
		OperationHistorySearch, OperationTopicRecall, OperationRetrieverEmbedding,
		OperationRetrieverRecall, OperationRetrieverAnswer,
		OperationCallbackContinuation, OperationCandidateGeneration, OperationJudge,
		OperationMessageEmbedding, OperationOutboundEmbedding, OperationChunkMerge,
		OperationChunkEmbedding, OperationReindexEmbedding, OperationDebugImage,
		OperationDebugConversation:
		return true
	default:
		return false
	}
}

type AttributionMode string

const (
	AttributionExplicit      AttributionMode = "explicit"
	AttributionLegacyMapping AttributionMode = "legacy_mapping"
	AttributionUnknown       AttributionMode = "unknown"
)

type Status string

const (
	StatusSuccess      Status = "success"
	StatusError        Status = "error"
	StatusUsageMissing Status = "usage_missing"
)

type ToolStatus string

const (
	ToolStatusSuccess ToolStatus = "success"
	ToolStatusError   ToolStatus = "error"
)

type ToolCall struct {
	Name      string
	Status    ToolStatus
	Duration  time.Duration
	ErrorKind string
	Error     string
	CalledAt  time.Time
}

type Kind string

const (
	KindResponses       Kind = "responses"
	KindResponsesStream Kind = "responses_stream"
	KindEmbedding       Kind = "embedding"
)

type Scope struct {
	ChatID            string
	ChatName          string
	OpenID            string
	UserName          string
	SourceType        SourceType
	Source            string
	BusinessScene     BusinessScene
	BusinessOperation BusinessOperation
	AttributionMode   AttributionMode
}

type Record struct {
	Scope            Scope
	Provider         string
	Model            string
	Kind             Kind
	Status           Status
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	ResponseID       string
	TraceID          string
	Error            string
	CreatedAt        time.Time
	ToolCalls        []ToolCall
}

type Buckets struct {
	Minute time.Time
	Hour   time.Time
	Day    time.Time
}

func NormalizeScope(scope Scope) Scope {
	scope.ChatID = strings.TrimSpace(scope.ChatID)
	scope.ChatName = strings.TrimSpace(scope.ChatName)
	scope.OpenID = strings.TrimSpace(scope.OpenID)
	scope.UserName = strings.TrimSpace(scope.UserName)
	scope.Source = strings.TrimSpace(scope.Source)
	scope.SourceType = SourceType(strings.TrimSpace(string(scope.SourceType)))
	scope.BusinessScene = BusinessScene(strings.TrimSpace(string(scope.BusinessScene)))
	scope.BusinessOperation = BusinessOperation(strings.TrimSpace(string(scope.BusinessOperation)))
	if scope.Source == "" {
		scope.Source = "unknown"
	}
	switch scope.SourceType {
	case SourceTypeUser, SourceTypeBackground, SourceTypeSystem, SourceTypeDebug:
	default:
		scope.SourceType = SourceTypeSystem
	}
	if scope.BusinessScene.Valid() && scope.BusinessOperation.Valid() {
		scope.AttributionMode = AttributionExplicit
	} else if scene, operation, ok := legacyAttribution(scope.Source); ok {
		scope.BusinessScene = scene
		scope.BusinessOperation = operation
		scope.AttributionMode = AttributionLegacyMapping
	} else {
		scope.BusinessScene = SceneUnknown
		scope.BusinessOperation = OperationUnknown
		scope.AttributionMode = AttributionUnknown
	}
	if scope.ChatName == "" {
		scope.ChatName = fallbackChatName(scope)
	}
	return scope
}

func legacyAttribution(source string) (BusinessScene, BusinessOperation, bool) {
	switch strings.TrimSpace(source) {
	case "chat":
		return SceneConversation, OperationChatReply, true
	case "intent":
		return SceneRouting, OperationIntentRecognition, true
	case "history_search":
		return SceneRetrieval, OperationHistorySearch, true
	case "topic_recall":
		return SceneRetrieval, OperationTopicRecall, true
	case "retriever_embedding":
		return SceneRetrieval, OperationRetrieverEmbedding, true
	case "retriever_recall":
		return SceneRetrieval, OperationRetrieverRecall, true
	case "retriever_answer":
		return SceneRetrieval, OperationRetrieverAnswer, true
	case "message_recording":
		return SceneBackground, OperationMessageEmbedding, true
	case "outbound_message_recording":
		return SceneBackground, OperationOutboundEmbedding, true
	case "chunking":
		return SceneBackground, OperationChunkMerge, true
	case "chunking_embedding":
		return SceneBackground, OperationChunkEmbedding, true
	case "reindex_embeddings":
		return SceneBackground, OperationReindexEmbedding, true
	case "conversation_evaluation_candidate":
		return SceneEvaluation, OperationCandidateGeneration, true
	case "agent_callback_continuation":
		return SceneAgentRuntime, OperationCallbackContinuation, true
	case "debug_image":
		return SceneDebug, OperationDebugImage, true
	default:
		return SceneUnknown, OperationUnknown, false
	}
}

func fallbackChatName(scope Scope) string {
	if scope.ChatID != "" {
		return scope.ChatID
	}
	if scope.Source != "" && scope.SourceType != "" {
		return string(scope.SourceType) + ":" + scope.Source
	}
	if scope.Source != "" {
		return scope.Source
	}
	return "unknown"
}

func BucketTimes(createdAt time.Time) Buckets {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return Buckets{
		Minute: createdAt.Truncate(time.Minute),
		Hour:   createdAt.Truncate(time.Hour),
		Day:    time.Date(createdAt.Year(), createdAt.Month(), createdAt.Day(), 0, 0, 0, 0, createdAt.Location()),
	}
}

func sanitizeLabel(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "�")
	if utf8.RuneCountInString(value) <= labelMaxLen {
		return value
	}
	runes := []rune(value)
	return string(runes[:labelMaxLen]) + "..."
}
