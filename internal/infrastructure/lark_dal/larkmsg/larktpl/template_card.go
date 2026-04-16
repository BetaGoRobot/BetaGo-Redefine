package larktpl

import (
	"maps"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/bytedance/sonic"
)

type CardBaseVars struct {
	RefreshTime     string      `json:"refresh_time"`
	JaegerTraceInfo string      `json:"jaeger_trace_info"`
	JaegerTraceURL  string      `json:"jaeger_trace_url"`
	WithdrawInfo    string      `json:"withdraw_info"`
	WithdrawTitle   string      `json:"withdraw_title"`
	WithdrawConfirm string      `json:"withdraw_confirm"`
	WithdrawObject  WithDrawObj `json:"withdraw_object"`

	RawCmd     *string     `json:"raw_cmd,omitempty"`
	RefreshObj *RefreshObj `json:"refresh_obj,omitempty"`
}

type CardBaseVarsSetter interface {
	SetCardBaseVars(CardBaseVars)
}

type RefreshObj struct {
	Action  string `json:"action"`
	Command string `json:"command"`
}

type WithDrawObj struct {
	Action string `json:"action"`
}

type ImageKeyRef struct {
	ImgKey string `json:"img_key,omitempty"`
}

type MusicListCardVars struct {
	CardBaseVars
	ObjectList1  []*MusicListCardItem `json:"object_list_1"`
	Query        string               `json:"query"`
	PageInfoText string               `json:"page_info_text,omitempty"`
	CurrentPage  int                  `json:"current_page,omitempty"`
	TotalPages   int                  `json:"total_pages,omitempty"`
	HasPrev      bool                 `json:"has_prev"`
	HasNext      bool                 `json:"has_next"`
	PrevPageVal  map[string]string    `json:"prev_page_val,omitempty"`
	NextPageVal  map[string]string    `json:"next_page_val,omitempty"`
}

func (v *MusicListCardVars) SetCardBaseVars(base CardBaseVars) {
	if v == nil {
		return
	}
	v.CardBaseVars = base
}

type MusicListCardItem struct {
	Field1       string            `json:"field_1,omitempty"`
	Field2       ImageKeyRef       `json:"field_2,omitempty"`
	Field3       string            `json:"field_3,omitempty"`
	CommentTime  string            `json:"comment_time,omitempty"`
	ButtonInfo  string            `json:"button_info,omitempty"`
	ElementID   string            `json:"element_id,omitempty"`
	ButtonVal   map[string]string `json:"button_val,omitempty"`
	Button2Info string            `json:"button2_info,omitempty"`
	Button2Val  map[string]string `json:"button2_val,omitempty"`

	Filled bool `json:"-"`
}

type SingleSongDetailCardVars struct {
	CardBaseVars
	Lyrics           string            `json:"lyrics"`
	Title            string            `json:"title"`
	SubTitle         string            `json:"sub_title"`
	ImgKey           ImageKeyRef       `json:"imgkey"`
	PlayerURL        string            `json:"player_url"`
	FullLyricsButton map[string]string `json:"full_lyrics_button"`
	RefreshID        map[string]string `json:"refresh_id"`
}

func (v *SingleSongDetailCardVars) SetCardBaseVars(base CardBaseVars) {
	if v == nil {
		return
	}
	v.CardBaseVars = base
}

type FullLyricsCardVars struct {
	CardBaseVars
	LeftLyrics  string      `json:"left_lyrics"`
	RightLyrics string      `json:"right_lyrics"`
	Title       string      `json:"title"`
	SubTitle    string      `json:"sub_title"`
	ImgKey      ImageKeyRef `json:"imgkey"`
}

func (v *FullLyricsCardVars) SetCardBaseVars(base CardBaseVars) {
	if v == nil {
		return
	}
	v.CardBaseVars = base
}

type NormalCardReplyVars struct {
	CardBaseVars
	Content string `json:"content"`
}

func (v *NormalCardReplyVars) SetCardBaseVars(base CardBaseVars) {
	if v == nil {
		return
	}
	v.CardBaseVars = base
}

type ToneData struct {
	Tone string `json:"tone"`
}

type Questions struct {
	Question string `json:"question"`
}

type MsgLine struct {
	Time    string `json:"time"`
	User    *User  `json:"user,omitempty"`
	Content string `json:"content"`
}
type MainTopicOrActivity struct {
	MainTopicOrActivity string `json:"main_topic_or_activity,omitempty"`
}

type KeyConceptAndNoun struct {
	KeyConceptAndNoun string `json:"key_concept_and_noun,omitempty"`
}
type MentionedGroupOrOrganization struct {
	MentionedGroupOrOrganization string `json:"mentioned_group_or_organization,omitempty"`
}

type MentionedPeopleUnit struct {
	MentionedPeople string `json:"mentioned_people,omitempty"`
}

type LocationAndVenue struct {
	LocationAndVenue string `json:"locations_and_venue,omitempty"`
}

type MediaAndWork struct {
	Title string `json:"title"`
	Type  string `json:"type"`
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
	UserID string `json:"user_id"`
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

type ChunkMetaData struct {
	Summary string `json:"summary"`

	Intent       string  `json:"intent"`
	Participants []*User `json:"participants,omitempty"`

	Sentiment string       `json:"sentiment"`
	Tones     []*ToneData  `json:"tones,omitempty"`
	Questions []*Questions `json:"questions,omitempty"`

	MsgList            []*MsgLine            `json:"msg_list,omitempty"`
	PlansAndSuggestion []*PlansAndSuggestion `json:"plans_and_suggestions,omitempty"`

	MainTopicsOrActivities         []*ObjTextArray `json:"main_topics_or_activities,omitempty"`
	KeyConceptsAndNouns            []*ObjTextArray `json:"key_concepts_and_nouns,omitempty"`
	MentionedGroupsOrOrganizations []*ObjTextArray `json:"mentioned_groups_or_organizations,omitempty"`
	MentionedPeople                []*ObjTextArray `json:"mentioned_people,omitempty"`
	LocationsAndVenues             []*ObjTextArray `json:"locations_and_venues,omitempty"`
	MediaAndWorks                  []*MediaAndWork `json:"media_and_works,omitempty"`

	Timestamp string `json:"timestamp"`
	MsgID     string `json:"msg_id"`

	*CardBaseVars
}

type ObjTextArray struct {
	Text string `json:"text,omitempty"`
}

func ToObjTextArray(s string) *ObjTextArray {
	return &ObjTextArray{s}
}

type (
	// 对于wc的卡片，主要涉及几个信息
	WordCountCardVars[T any] struct {
		// 1. 用户排行榜、消息/互动频率
		UserList []*UserListItem `json:"user_list"`
		// 2. 词云
		WordCloud any             `json:"word_cloud"`
		Chunks    []*ChunkData[T] `json:"chunks"`
		TimeStamp string          `json:"time_stamp"`
		StartTime string          `json:"start_time"`
		EndTime   string          `json:"end_time"`
	}

	UserListItem struct {
		Number    int         `json:"number"`
		User      []*UserUnit `json:"user"`
		MsgCnt    int         `json:"msg_cnt"`
		ActionCnt int         `json:"action_cnt"`
	}
	UserUnit struct {
		ID string `json:"id"` // OpenID
	}

	ChunkData[T any] struct {
		ChunkLog *T `json:"-"`

		Sentiment           string      `json:"sentiment"`
		Tones               string      `json:"tones"`
		UserIDs4Lark        []*UserUnit `json:"user_ids_4_lark"`
		UnresolvedQuestions string      `json:"unresolved_questions"`
	}
)

func (d *ChunkData[T]) MarshalJSON() ([]byte, error) {
	// 1. 核心破局点：定义类型别名，剥离原有的 MarshalJSON 方法
	type Alias ChunkData[T]

	// 2. 将指针强转为别名类型，再去转 Map。此时序列化库就不会再触发死循环了
	m, err := utils.Struct2Map((*Alias)(d))
	if err != nil {
		return nil, err // 顺便纠正一下：原代码这里出错时 return sonic.Marshal(m) 是有风险的，应该直接返回 err
	}

	// 3. 将需要打平的内部结构也转为 Map
	chunkMap, err := utils.Struct2Map(d.ChunkLog)
	if err != nil {
		return nil, err
	}

	// 4. 清理残留的嵌套字段 (非常重要)
	// 因为 d 被当做普通 struct 序列化了，m 里面此刻还包含着嵌套的 ChunkLog 字段。
	// 如果你在 struct 定义时没有给 ChunkLog 加上 `json:"-"`，这里必须手动把它的 key 删掉，否则结果会重复。
	delete(m, "chunkLog") // 注意：这里的 "chunkLog" 要替换成你 ChunkLog 实际的 json tag 名字

	// 5. 合并 Map
	maps.Copy(m, chunkMap)

	// 6. 最终输出
	return sonic.Marshal(m)
}
