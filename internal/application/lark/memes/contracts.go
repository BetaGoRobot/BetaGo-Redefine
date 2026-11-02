// Package memes retrieves evidence-grounded meme references independently of model and search SDKs.
package memes

import (
	"context"
	"encoding/json"
	"time"
)

const (
	MaxMessages     = 32
	MaxInputRunes   = 8192
	MaxInputBytes   = 32 * 1024
	MaxFragments    = 128
	DefaultMaxChars = 64
	HardMaxChars    = 120
	CandidateBudget = 5
	// One source can have eight use cards sharing the same description vector.
	// Read a bounded wider source pool before choosing five diverse references.
	DescriptionHitBudget = CandidateBudget * 8
	MaxSearchQueries     = 7
)

type Request struct {
	RequestID string `json:"request_id"`
	Dialogue  []Turn `json:"dialogue"`
	MaxChars  int    `json:"max_chars"`
}
type Turn struct {
	TurnID  string `json:"turn_id"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}
type SourceFragment struct {
	SourceID  int    `json:"source_id"`
	TurnID    string `json:"turn_id"`
	Quote     string `json:"quote"`
	StartRune int    `json:"start_rune"`
	EndRune   int    `json:"end_rune"`
}
type FactState string

const (
	Asserted     FactState = "asserted"
	Proposed     FactState = "proposed"
	Hypothetical FactState = "hypothetical"
	Quoted       FactState = "quoted"
	Unknown      FactState = "unknown"
)

type Evidence struct {
	SourceID int       `json:"source_id"`
	State    FactState `json:"state"`
}
type Requirement struct {
	ID    string   `json:"id"`
	AnyOf []string `json:"any_of"`
}
type Role struct {
	Kind    string `json:"kind,omitempty"`
	Meaning string `json:"meaning,omitempty"`
}
type Stance struct {
	Voice            string `json:"voice,omitempty"`
	ReplyTarget      string `json:"reply_target,omitempty"`
	EvaluationTarget string `json:"evaluation_target,omitempty"`
}
type Slot struct {
	Meaning     string `json:"meaning,omitempty"`
	ValueSource string `json:"value_source,omitempty"`
	BindTo      string `json:"bind_to,omitempty"`
	MaxChars    int    `json:"max_chars,omitempty"`
	Constraints string `json:"constraints,omitempty"`
	Form        string `json:"form,omitempty"`
}
type Example struct {
	Dialogue    []Turn `json:"dialogue,omitempty"`
	Explanation string `json:"explanation,omitempty"`
	SourceKind  string `json:"source_kind,omitempty"`
}
type Examples struct {
	Positive []Example `json:"positive,omitempty"`
	NearMiss []Example `json:"near_miss,omitempty"`
}
type SourceMetadata struct {
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}
type UseCard struct {
	Description        string           `json:"description,omitempty"`
	UseID              string           `json:"use_id"`
	FamilyID           string           `json:"family_id"`
	Name               string           `json:"name"`
	ReviewStatus       string           `json:"review_status"`
	InputRelation      string           `json:"input_relation"`
	Requirements       []Requirement    `json:"requirements"`
	ApplicabilityNotes string           `json:"applicability_notes"`
	ResponseAct        string           `json:"response_act"`
	Roles              map[string]Role  `json:"roles,omitempty"`
	Stance             Stance           `json:"stance,omitzero"`
	Template           string           `json:"template,omitempty"`
	Anchors            []string         `json:"anchors,omitempty"`
	Slots              map[string]Slot  `json:"slots,omitempty"`
	Examples           Examples         `json:"examples,omitzero"`
	Sources            []SourceMetadata `json:"sources"`
}
type Relation struct {
	Kind     string     `json:"kind"`
	Pattern  string     `json:"pattern"`
	Evidence []Evidence `json:"evidence,omitempty"`
}
type EmotionChain struct {
	Subject  string     `json:"subject"`
	States   []string   `json:"states"`
	Evidence []Evidence `json:"evidence,omitempty"`
}
type Key struct {
	Text     string     `json:"text"`
	Evidence []Evidence `json:"evidence"`
}
type ProjectionKeys struct {
	RequiredAliasGroups [][]string `json:"required_alias_groups"`
	Related             []string   `json:"related"`
}
type Projection struct {
	CardFingerprint string         `json:"card_fingerprint"`
	Relations       []Relation     `json:"relations"`
	EmotionChain    []EmotionChain `json:"emotion_chain"`
	Keys            ProjectionKeys `json:"keys"`
}
type IndexRecord struct {
	UseID        string     `json:"use_id"`
	Relation     string     `json:"relation"`
	EmotionChain string     `json:"emotion_chain"`
	Keys         string     `json:"keys"`
	Full         UseCard    `json:"full"`
	Projection   Projection `json:"projection"`
	Fingerprint  string     `json:"fingerprint"`
}
type QueryAnalysis struct {
	Relations    []Relation     `json:"relations"`
	EmotionChain []EmotionChain `json:"emotion_chain"`
	Keys         []Key          `json:"keys"`
}
type Field string

const (
	FieldRelation    Field = "relation"
	FieldEmotion     Field = "emotion_chain"
	FieldKeys        Field = "keys"
	FieldDescription Field = "description"
)

// FieldQueryBudget keeps the three semantic dimensions bounded separately from
// the single original-dialogue query used for explanation-vector retrieval.
func FieldQueryBudget(field Field) int {
	switch field {
	case FieldRelation, FieldEmotion, FieldKeys:
		return 2
	case FieldDescription:
		return 1
	default:
		return 0
	}
}

type SearchQuery struct {
	ID             string   `json:"id"`
	Field          Field    `json:"field"`
	Text           string   `json:"text"`
	RelationKinds  []string `json:"relation_kinds,omitempty"`
	ElementIndices []int    `json:"element_indices"`
}
type Warning struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	UseID  string `json:"use_id,omitempty"`
}
type QueryPlan struct {
	Grounded  *GroundedPlanTrace `json:"grounded,omitempty"`
	Analysis  QueryAnalysis      `json:"analysis"`
	Queries   []SearchQuery      `json:"queries"`
	Warnings  []Warning          `json:"warnings"`
	Discarded []SearchQuery      `json:"discarded"`
}
type RawHit struct {
	DocumentID      string     `json:"document_id"`
	UseID           string     `json:"use_id"`
	Score           float64    `json:"score"`
	Rank            int        `json:"rank"`
	Full            UseCard    `json:"full"`
	RelationKinds   []string   `json:"relation_kinds,omitempty"`
	Projection      Projection `json:"projection"`
	Fingerprint     string     `json:"fingerprint,omitempty"`
	CardFingerprint string     `json:"card_fingerprint,omitempty"`
}
type ChannelResult struct {
	Warnings []Warning `json:"warnings,omitempty"`
	Channel  string    `json:"channel"`
	Status   string    `json:"status"`
	Hits     []RawHit  `json:"hits"`
	Error    string    `json:"error,omitempty"`
}
type RankedHit struct {
	UseID string  `json:"use_id"`
	Score float64 `json:"score"`
	Rank  int     `json:"rank"`
}
type QueryResult struct {
	Query    SearchQuery     `json:"query"`
	Status   string          `json:"status"`
	Channels []ChannelResult `json:"channels"`
	Hits     []RankedHit     `json:"hits"`
	Error    string          `json:"error,omitempty"`
}
type FieldResult struct {
	Field             Field       `json:"field"`
	SuccessfulQueries int         `json:"successful_queries"`
	Hits              []RankedHit `json:"hits"`
}
type RetrievalSnapshot struct {
	Fingerprint       string        `json:"fingerprint"`
	Plan              QueryPlan     `json:"plan"`
	Queries           []QueryResult `json:"queries"`
	RawQueries        []QueryResult `json:"raw_queries"`
	Fields            []FieldResult `json:"fields"`
	Fused             []RankedHit   `json:"fused"`
	Candidates        []UseCard     `json:"candidates"`
	TruncatedUseIDs   []string      `json:"truncated_use_ids"`
	Warnings          []Warning     `json:"warnings"`
	Degraded          bool          `json:"degraded"`
	SuccessfulQueries int           `json:"successful_queries"`
}
type Retriever interface {
	Search(context.Context, QueryPlan) (RetrievalSnapshot, error)
}
type Stage string

const (
	StageInput     Stage = "input"
	StageQuery     Stage = "query"
	StageRetrieval Stage = "retrieval"
	StageEncode    Stage = "encode"
)

type CompletionRequest struct {
	Stage        Stage
	SystemPrompt string
	InputJSON    json.RawMessage
	Schema       json.RawMessage
	Repair       bool
}
type CallMetadata struct {
	Model        string `json:"model"`
	PromptHash   string `json:"prompt_hash"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	SDKRetries   int    `json:"sdk_retries"`
	DurationMS   int64  `json:"duration_ms"`
}
type CompletionResponse struct {
	JSON     json.RawMessage
	Metadata CallMetadata
}
type Completion interface {
	Complete(context.Context, CompletionRequest) (CompletionResponse, error)
}
type RunMetadata struct {
	RequestTimeoutMS int64           `json:"request_timeout_ms"`
	StageTimeoutMS   int64           `json:"stage_timeout_ms"`
	Model            string          `json:"model,omitempty"`
	PromptHash       string          `json:"prompt_hash,omitempty"`
	CorpusHash       string          `json:"corpus_hash,omitempty"`
	ProjectionHash   string          `json:"projection_hash,omitempty"`
	RetrievalConfig  json.RawMessage `json:"retrieval_config,omitempty"`
	CandidateBudget  int             `json:"candidate_budget"`
	SemanticCalls    int             `json:"semantic_calls"`
	RepairCalls      int             `json:"repair_calls"`
	ElapsedMS        int64           `json:"elapsed_ms"`
	Calls            []CallMetadata  `json:"calls,omitempty"`
}
type ReferenceStatus string

const (
	ReferenceStatusReady      ReferenceStatus = "ready"
	ReferenceStatusEmpty      ReferenceStatus = "empty"
	ReferenceStatusUnresolved ReferenceStatus = "unresolved"
	ReferenceStatusError      ReferenceStatus = "error"
)
const (
	ReasonNoCandidates    = "NO_CANDIDATES"
	ReasonPlanEmpty       = "PLAN_EMPTY"
	ReasonRetrievalFailed = "RETRIEVAL_FAILED"
	ReasonContractInvalid = "CONTRACT_INVALID"
	ReasonTimeout         = "TIMEOUT"
	ReasonModelFailed     = "MODEL_FAILED"
)

type ReferenceError struct {
	Stage  Stage  `json:"stage"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}
type ReferenceResult struct {
	ContextV2 *GroundedContext  `json:"context_v2,omitempty"`
	Status    ReferenceStatus   `json:"status"`
	Cards     []UseCard         `json:"cards"`
	Plan      QueryPlan         `json:"plan"`
	Retrieval RetrievalSnapshot `json:"retrieval"`
	Sources   []SourceFragment  `json:"sources"`
	Warnings  []Warning         `json:"warnings"`
	Degraded  bool              `json:"degraded"`
	Reason    string            `json:"reason,omitempty"`
	Errors    []ReferenceError  `json:"errors"`
	Metadata  RunMetadata       `json:"metadata"`
}
type ReferenceDependencies struct {
	Model     Completion
	Retriever Retriever
}
type ReferenceOptions struct {
	RequestTimeout time.Duration
	StageTimeout   time.Duration
	Metadata       RunMetadata
}
type WikiSource struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}
type EncodedUse struct {
	Full       UseCard    `json:"full"`
	Projection Projection `json:"projection"`
}
type EncodedWiki struct {
	Cards []EncodedUse `json:"cards"`
}
