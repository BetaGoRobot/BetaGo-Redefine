package handlers

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/carddebug"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
)

type ConfigScope string

const (
	ConfigScopeChat   ConfigScope = "chat"
	ConfigScopeUser   ConfigScope = "user"
	ConfigScopeGlobal ConfigScope = "global"
)

func (ConfigScope) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(ConfigScopeChat), Label: "当前群"},
			{Value: string(ConfigScopeUser), Label: "当前用户"},
			{Value: string(ConfigScopeGlobal), Label: "全局"},
		},
		DefaultValue: string(ConfigScopeChat),
	}
}

type FeatureScope string

const (
	FeatureScopeChat     FeatureScope = "chat"
	FeatureScopeUser     FeatureScope = "user"
	FeatureScopeChatUser FeatureScope = "chat_user"
)

func (FeatureScope) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(FeatureScopeChat), Label: "当前群"},
			{Value: string(FeatureScopeUser), Label: "当前用户"},
			{Value: string(FeatureScopeChatUser), Label: "群内用户"},
		},
		DefaultValue: string(FeatureScopeChat),
	}
}

type MusicSearchType string

const (
	MusicSearchTypeSong     MusicSearchType = "song"
	MusicSearchTypeAlbum    MusicSearchType = "album"
	MusicSearchTypePlaylist MusicSearchType = "playlist"
)

func (MusicSearchType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(MusicSearchTypeSong), Label: "单曲"},
			{Value: string(MusicSearchTypeAlbum), Label: "专辑"},
			{Value: string(MusicSearchTypePlaylist), Label: "歌单"},
		},
		DefaultValue: string(MusicSearchTypeSong),
	}
}

type ScheduleStatus string

const (
	ScheduleStatusEnabled   ScheduleStatus = model.ScheduleTaskStatusEnabled
	ScheduleStatusPaused    ScheduleStatus = model.ScheduleTaskStatusPaused
	ScheduleStatusCompleted ScheduleStatus = model.ScheduleTaskStatusCompleted
	ScheduleStatusDisabled  ScheduleStatus = model.ScheduleTaskStatusDisabled
)

func (ScheduleStatus) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(ScheduleStatusEnabled), Label: "启用"},
			{Value: string(ScheduleStatusPaused), Label: "暂停"},
			{Value: string(ScheduleStatusCompleted), Label: "完成"},
			{Value: string(ScheduleStatusDisabled), Label: "禁用"},
		},
	}
}

type ScheduleType string

const (
	ScheduleTypeOnce ScheduleType = model.ScheduleTaskTypeOnce
	ScheduleTypeCron ScheduleType = model.ScheduleTaskTypeCron
)

func (ScheduleType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(ScheduleTypeOnce), Label: "单次"},
			{Value: string(ScheduleTypeCron), Label: "周期"},
		},
	}
}

type ScheduleChatScope string

const (
	ScheduleChatScopeCurrent ScheduleChatScope = ScheduleChatScope(scheduleapp.TaskChatScopeCurrent)
	ScheduleChatScopeAll     ScheduleChatScope = ScheduleChatScope(scheduleapp.TaskChatScopeAll)
)

func (ScheduleChatScope) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(ScheduleChatScopeCurrent), Label: "当前群"},
		},
		DefaultValue: string(ScheduleChatScopeCurrent),
	}
}

type DebugCardSpec string

func (DebugCardSpec) CommandEnum() xcommand.EnumDescriptor {
	specs := carddebug.ListSpecs()
	options := make([]xcommand.CommandArgOption, 0, len(specs))
	for _, spec := range specs {
		options = append(options, xcommand.CommandArgOption{
			Value: spec.Name,
			Label: spec.Name,
		})
	}
	return xcommand.EnumDescriptor{Options: options}
}

type ReplyMatchType string

const (
	ReplyMatchTypeSubstr ReplyMatchType = ReplyMatchType(xmodel.MatchTypeSubStr)
	ReplyMatchTypeFull   ReplyMatchType = ReplyMatchType(xmodel.MatchTypeFull)
	ReplyMatchTypeRegex  ReplyMatchType = ReplyMatchType(xmodel.MatchTypeRegex)
)

func (ReplyMatchType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(ReplyMatchTypeSubstr), Label: "包含匹配"},
			{Value: string(ReplyMatchTypeFull), Label: "完全匹配"},
			{Value: string(ReplyMatchTypeRegex), Label: "正则匹配"},
		},
	}
}

type ReplyContentType string

const (
	ReplyContentTypeText  ReplyContentType = "text"
	ReplyContentTypeImage ReplyContentType = "image"
)

func (ReplyContentType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(ReplyContentTypeText), Label: "文本"},
			{Value: string(ReplyContentTypeImage), Label: "图片"},
		},
		DefaultValue: string(ReplyContentTypeText),
	}
}

type TrendChartType string

const (
	TrendChartTypeLine TrendChartType = "line"
	TrendChartTypePie  TrendChartType = "pie"
	TrendChartTypeBar  TrendChartType = "bar"
)

func (TrendChartType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(TrendChartTypeLine), Label: "折线图"},
			{Value: string(TrendChartTypePie), Label: "饼图"},
			{Value: string(TrendChartTypeBar), Label: "柱状图"},
		},
		DefaultValue: string(TrendChartTypeLine),
	}
}

type WordCloudSortType string

const (
	WordCloudSortTypeRelevance WordCloudSortType = "relevance"
	WordCloudSortTypeTime      WordCloudSortType = "time"
)

func (WordCloudSortType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(WordCloudSortTypeRelevance), Label: "相关度"},
			{Value: string(WordCloudSortTypeTime), Label: "时间"},
		},
		DefaultValue: string(WordCloudSortTypeRelevance),
	}
}

type WordChunkIntentType string

const (
	WordChunkIntentTypeAll                   WordChunkIntentType = "all"
	WordChunkIntentTypeSocialCoordination    WordChunkIntentType = "SOCIAL_COORDINATION"
	WordChunkIntentTypeInformationSharing    WordChunkIntentType = "INFORMATION_SHARING"
	WordChunkIntentTypeSeekingHelpOrAdvice   WordChunkIntentType = "SEEKING_HELP_OR_ADVICE"
	WordChunkIntentTypeDebateOrDiscussion    WordChunkIntentType = "DEBATE_OR_DISCUSSION"
	WordChunkIntentTypeEmotionalSharing      WordChunkIntentType = "EMOTIONAL_SHARING_OR_SUPPORT"
	WordChunkIntentTypeRequestRecommendation WordChunkIntentType = "REQUESTING_RECOMMENDATION"
	WordChunkIntentTypeCasualChitchat        WordChunkIntentType = "CASUAL_CHITCHAT"
)

func (WordChunkIntentType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(WordChunkIntentTypeAll), Label: "全部意图"},
			{Value: string(WordChunkIntentTypeSocialCoordination), Label: "共商议事"},
			{Value: string(WordChunkIntentTypeInformationSharing), Label: "见闻共飨"},
			{Value: string(WordChunkIntentTypeSeekingHelpOrAdvice), Label: "求教问策"},
			{Value: string(WordChunkIntentTypeDebateOrDiscussion), Label: "明辨事理"},
			{Value: string(WordChunkIntentTypeEmotionalSharing), Label: "悲欢与共"},
			{Value: string(WordChunkIntentTypeRequestRecommendation), Label: "求珍问宝"},
			{Value: string(WordChunkIntentTypeCasualChitchat), Label: "谈天说地"},
		},
		DefaultValue: string(WordChunkIntentTypeAll),
	}
}

type WordChunkSentimentType string

const (
	WordChunkSentimentTypeAll      WordChunkSentimentType = "all"
	WordChunkSentimentTypePositive WordChunkSentimentType = "POSITIVE"
	WordChunkSentimentTypeNegative WordChunkSentimentType = "NEGATIVE"
	WordChunkSentimentTypeNeutral  WordChunkSentimentType = "NEUTRAL"
	WordChunkSentimentTypeMixed    WordChunkSentimentType = "MIXED"
)

func (WordChunkSentimentType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(WordChunkSentimentTypeAll), Label: "全部情绪"},
			{Value: string(WordChunkSentimentTypePositive), Label: "正向"},
			{Value: string(WordChunkSentimentTypeNegative), Label: "负向"},
			{Value: string(WordChunkSentimentTypeNeutral), Label: "中性"},
			{Value: string(WordChunkSentimentTypeMixed), Label: "混合"},
		},
		DefaultValue: string(WordChunkSentimentTypeAll),
	}
}

type WordChunkQuestionMode string

const (
	WordChunkQuestionModeAll        WordChunkQuestionMode = "all"
	WordChunkQuestionModeQuestion   WordChunkQuestionMode = "question"
	WordChunkQuestionModeNoQuestion WordChunkQuestionMode = "no_question"
)

func (WordChunkQuestionMode) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(WordChunkQuestionModeAll), Label: "全部"},
			{Value: string(WordChunkQuestionModeQuestion), Label: "仅有问题"},
			{Value: string(WordChunkQuestionModeNoQuestion), Label: "仅无问题"},
		},
		DefaultValue: string(WordChunkQuestionModeAll),
	}
}

type ImageAssetType string

const (
	ImageAssetTypeImage   ImageAssetType = "image"
	ImageAssetTypeSticker ImageAssetType = "sticker"
)

func (ImageAssetType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(ImageAssetTypeImage), Label: "图片"},
			{Value: string(ImageAssetTypeSticker), Label: "贴纸"},
		},
	}
}

type OneWordType string

const (
	OneWordTypeAnime    OneWordType = "二次元"
	OneWordTypeGame     OneWordType = "游戏"
	OneWordTypeLiterary OneWordType = "文学"
	OneWordTypeOriginal OneWordType = "原创"
	OneWordTypeNetwork  OneWordType = "网络"
	OneWordTypeOther    OneWordType = "其他"
	OneWordTypeFilm     OneWordType = "影视"
	OneWordTypePoetry   OneWordType = "诗词"
	OneWordTypeNetease  OneWordType = "网易云"
	OneWordTypePhilo    OneWordType = "哲学"
	OneWordTypeJoke     OneWordType = "抖机灵"
)

func (OneWordType) CommandEnum() xcommand.EnumDescriptor {
	return xcommand.EnumDescriptor{
		Options: []xcommand.CommandArgOption{
			{Value: string(OneWordTypeAnime), Label: string(OneWordTypeAnime)},
			{Value: string(OneWordTypeGame), Label: string(OneWordTypeGame)},
			{Value: string(OneWordTypeLiterary), Label: string(OneWordTypeLiterary)},
			{Value: string(OneWordTypeOriginal), Label: string(OneWordTypeOriginal)},
			{Value: string(OneWordTypeNetwork), Label: string(OneWordTypeNetwork)},
			{Value: string(OneWordTypeOther), Label: string(OneWordTypeOther)},
			{Value: string(OneWordTypeFilm), Label: string(OneWordTypeFilm)},
			{Value: string(OneWordTypePoetry), Label: string(OneWordTypePoetry)},
			{Value: string(OneWordTypeNetease), Label: string(OneWordTypeNetease)},
			{Value: string(OneWordTypePhilo), Label: string(OneWordTypePhilo)},
			{Value: string(OneWordTypeJoke), Label: string(OneWordTypeJoke)},
		},
	}
}
