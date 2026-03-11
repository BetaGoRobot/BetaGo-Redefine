package xmodel

import (
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	ark_model "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

type WordWithTag struct {
	Word string `json:"word"`
	Tag  string `json:"tag"`
}
type MessageIndex struct {
	*MessageLog
	ChatName             string          `json:"chat_name"`
	CreateTime           string          `json:"create_time"`
	CreateTimeV2         string          `json:"create_time_v2"`
	Message              []float32       `json:"message"`
	OpenID               string          `json:"user_id"`
	UserName             string          `json:"user_name"`
	RawMessage           string          `json:"raw_message"`
	RawMessageJieba      string          `json:"raw_message_jieba"`
	RawMessageJiebaArray []string        `json:"raw_message_jieba_array"`
	RawMessageJiebaTag   []*WordWithTag  `json:"raw_message_jieba_tag"`
	TokenUsage           ark_model.Usage `json:"token_usage"`
	IsCommand            bool            `json:"is_command"`
	MainCommand          string          `json:"main_command"`
}

type CardActionIndex struct {
	*callback.CardActionTriggerEvent
	ChatName       string         `json:"chat_name"`
	CreateTime     string         `json:"create_time"`
	CreateTimeUnix int64          `json:"create_time_unix,omitempty"`
	OpenID         string         `json:"user_id"`
	UserName       string         `json:"user_name"`
	OpenMessageID  string         `json:"open_message_id"`
	OpenChatID     string         `json:"open_chat_id"`
	ActionName     string         `json:"action_name"`
	ActionTag      string         `json:"action_tag"`
	SelectedOption string         `json:"selected_option"`
	ActionValue    map[string]any `json:"action_value"`
}

type MessageChunkLogV3 struct {
	ID                  string               `json:"id"`
	Summary             string               `json:"summary"`
	Intent              string               `json:"intent"`
	SentimentAndTone    *SentimentAndTone    `json:"sentiment_and_tone"`
	Entities            *Entities            `json:"entities"`
	InteractionAnalysis *InteractionAnalysis `json:"interaction_analysis"`
	Outcomes            *Outcome             `json:"outcomes"`

	ConversationEmbedding []float32 `json:"conversation_embedding"`

	MsgList     []string `json:"msg_list"`
	OpenIDs     []string `json:"user_ids,omitempty"`
	GroupID     string   `json:"group_id"`
	Timestamp   string   `json:"timestamp"`
	TimestampV2 *string  `json:"timestamp_v2"`
	MsgIDs      []string `json:"msg_ids"`
}

type SentimentAndTone struct {
	Sentiment string   `json:"sentiment"`
	Tones     []string `json:"tones"`
}
type PlansAndSuggestion struct {
	ActivityOrSuggestion string  `json:"activity_or_suggestion"`
	Proposer             *User   `json:"proposer"`
	ParticipantsInvolved []*User `json:"participants_involved"`
	Timing               *Timing `json:"timing"`
}

type Participant struct {
	*User
	MessageCount int `json:"message_count"`
}

type User struct {
	OpenID string `json:"user_id"`
	Name   string `json:"name"`
}

type Outcome struct {
	ConclusionsOrAgreements    []string              `json:"conclusions_or_agreements"`
	PlansAndSuggestions        []*PlansAndSuggestion `json:"plans_and_suggestions"`
	OpenThreadsOrPendingPoints []string              `json:"open_threads_or_pending_points"`
}

type Timing struct {
	RawText        string `json:"raw_text,omitempty"`
	NormalizedDate string `json:"normalized_date,omitempty"`
}

type Entities struct {
	MainTopicsOrActivities         []string        `json:"main_topics_or_activities"`
	KeyConceptsAndNouns            []string        `json:"key_concepts_and_nouns"`
	MentionedGroupsOrOrganizations []string        `json:"mentioned_groups_or_organizations"`
	MentionedPeople                []string        `json:"mentioned_people"`
	LocationsAndVenues             []string        `json:"locations_and_venues"`
	MediaAndWorks                  []*MediaAndWork `json:"media_and_works"`
	Resources                      []any           `json:"resources"`
}

type MediaAndWork struct {
	Title string `json:"title"`
	Type  string `json:"type"`
}

type InteractionAnalysis struct {
	Participants        []*Participant `json:"participants"`
	ConversationFlow    string         `json:"conversation_flow"`
	SocialDynamics      []string       `json:"social_dynamics"`
	IsQuestionPresent   bool           `json:"is_question_present"`
	UnresolvedQuestions []string       `json:"unresolved_questions"`
}

type MessageLog struct {
	MessageID   string `json:"message_id,omitempty" `  // 消息的open_message_id，说明参见：[消息ID说明](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/intro#ac79c1c2)
	RootID      string `json:"root_id,omitempty"`      // 根消息id，用于回复消息场景，说明参见：[消息ID说明](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/intro#ac79c1c2)
	ParentID    string `json:"parent_id,omitempty"`    // 父消息的id，用于回复消息场景，说明参见：[消息ID说明](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/intro#ac79c1c2)
	ChatID      string `json:"chat_id,omitempty"`      // 消息所在的群组 ID
	ThreadID    string `json:"thread_id,omitempty"`    // 消息所属的话题 ID
	ChatType    string `json:"chat_type,omitempty"`    // 消息所在的群组类型;;**可选值有**：;- `p2p`：单聊;- `group`： 群组;- `topic_group`：话题群
	MessageType string `json:"message_type,omitempty"` // 消息类型

	UserAgent string `json:"user_agent,omitempty"` // 用户代理
	Mentions  string `json:"mentions"`
	RawBody   string `json:"raw_body"`
	Content   string `json:"message_str"`
	FileKey   string `json:"file_key"`
	TraceID   string `json:"trace_id"`
	CreatedAt time.Time
}

type PromptTemplateArg struct {
	*model.PromptTemplateArg

	HistoryRecords   []string `json:"history_records" gorm:"-"`
	Context          []string `json:"context" gorm:"-"`
	Topics           []string `json:"topics" gorm:"-"`
	UserInput        []string `json:"user_input" gorm:"-"`
	CurrentTimeStamp string   `json:"current_timestamp" gorm:"-"`
}
