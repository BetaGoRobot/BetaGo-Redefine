package config

import "strings"

const (
	configEnumGroupArkModels       = "ark_models"
	configEnumGroupOpenSearchIndex = "opensearch_indexes"
)

type ConfigEnumOption struct {
	Text  string
	Value string
}

type ConfigDefinition struct {
	Key             ConfigKey
	Description     string
	ValueType       string
	IntMin          int
	IntMax          int
	EnumGroup       string
	EnumOptionsFunc func() []ConfigEnumOption
	AllowCustom     bool
}

var configDefinitions = []ConfigDefinition{
	{
		Key:         KeyReactionDefaultRate,
		Description: "默认回应表情概率 (0-100)",
		ValueType:   "int",
		IntMin:      0,
		IntMax:      100,
	},
	{
		Key:         KeyReactionFollowDefaultRate,
		Description: "跟随回应表情概率 (0-100)",
		ValueType:   "int",
		IntMin:      0,
		IntMax:      100,
	},
	{
		Key:         KeyRepeatDefaultRate,
		Description: "默认复读消息概率 (0-100)",
		ValueType:   "int",
		IntMin:      0,
		IntMax:      100,
	},
	{
		Key:         KeyImitateDefaultRate,
		Description: "默认模仿发言概率 (0-100)",
		ValueType:   "int",
		IntMin:      0,
		IntMax:      100,
	},
	{
		Key:         KeyIntentFallbackRate,
		Description: "意图识别失败回退概率 (0-100)",
		ValueType:   "int",
		IntMin:      0,
		IntMax:      100,
	},
	{
		Key:         KeyIntentReplyThreshold,
		Description: "意图识别回复阈值 (0-100)",
		ValueType:   "int",
		IntMin:      0,
		IntMax:      100,
	},
	{
		Key:         KeyIntentRecognitionEnabled,
		Description: "是否启用意图识别",
		ValueType:   "bool",
	},
	{
		Key:         KeyAgentRuntimeEnabled,
		Description: "是否启用 Agent Runtime 入口",
		ValueType:   "bool",
	},
	{
		Key:         KeyAgentRuntimeShadowOnly,
		Description: "Agent Runtime 是否仅 shadow 观察，不接管用户可见回复",
		ValueType:   "bool",
	},
	{
		Key:         KeyAgentRuntimeChatCutover,
		Description: "Agent Runtime 是否接管聊天主链路",
		ValueType:   "bool",
	},
	{
		Key:         KeyMusicCardInThread,
		Description: "音乐卡片是否默认在话题内回复",
		ValueType:   "bool",
	},
	{
		Key:         KeyWithDrawReplace,
		Description: "是否使用伪撤回替代真实撤回",
		ValueType:   "bool",
	},
	{
		Key:         KeyChatMode,
		Description: "聊天模式，standard 为标准聊天，agentic 为 agentic 卡片聊天",
		ValueType:   "string",
		EnumOptionsFunc: func() []ConfigEnumOption {
			return []ConfigEnumOption{
				{Text: "standard | 标准聊天", Value: string(ChatModeStandard)},
				{Text: "agentic | Agentic 卡片聊天", Value: string(ChatModeAgentic)},
			}
		},
	},
	{
		Key:             KeyChatReasoningModel,
		Description:     "推理模型 ID",
		ValueType:       "string",
		EnumGroup:       configEnumGroupArkModels,
		EnumOptionsFunc: arkModelEnumOptions,
		AllowCustom:     true,
	},
	{
		Key:             KeyChatNormalModel,
		Description:     "普通聊天模型 ID",
		ValueType:       "string",
		EnumGroup:       configEnumGroupArkModels,
		EnumOptionsFunc: arkModelEnumOptions,
		AllowCustom:     true,
	},
	{
		Key:             KeyIntentLiteModel,
		Description:     "意图识别模型 ID",
		ValueType:       "string",
		EnumGroup:       configEnumGroupArkModels,
		EnumOptionsFunc: arkModelEnumOptions,
		AllowCustom:     true,
	},
	{
		Key:             KeyLarkMsgIndex,
		Description:     "Lark 消息索引名",
		ValueType:       "string",
		EnumGroup:       configEnumGroupOpenSearchIndex,
		EnumOptionsFunc: openSearchIndexEnumOptions,
		AllowCustom:     true,
	},
	{
		Key:             KeyLarkChunkIndex,
		Description:     "Lark Chunk 索引名",
		ValueType:       "string",
		EnumGroup:       configEnumGroupOpenSearchIndex,
		EnumOptionsFunc: openSearchIndexEnumOptions,
		AllowCustom:     true,
	},
}

func GetConfigDefinition(key ConfigKey) (ConfigDefinition, bool) {
	for _, def := range configDefinitions {
		if def.Key == key {
			return def, true
		}
	}
	return ConfigDefinition{}, false
}

func GetConfigEnumOptions(key ConfigKey, currentValue string) []ConfigEnumOption {
	def, ok := GetConfigDefinition(key)
	if !ok {
		return nil
	}
	return def.EnumOptions(currentValue)
}

func (d ConfigDefinition) EnumOptions(currentValue string) []ConfigEnumOption {
	if d.EnumOptionsFunc == nil {
		return nil
	}
	options := d.EnumOptionsFunc()
	return ensureCurrentConfigEnumOption(options, currentValue)
}

func (d ConfigDefinition) HasEnumOptions() bool {
	return d.EnumOptionsFunc != nil
}

func ensureCurrentConfigEnumOption(options []ConfigEnumOption, currentValue string) []ConfigEnumOption {
	currentValue = strings.TrimSpace(currentValue)
	if currentValue == "" {
		return options
	}
	for _, option := range options {
		if strings.TrimSpace(option.Value) == currentValue {
			return options
		}
	}

	withCurrent := make([]ConfigEnumOption, 0, len(options)+1)
	withCurrent = append(withCurrent, ConfigEnumOption{
		Text:  currentValue + " | 当前值",
		Value: currentValue,
	})
	withCurrent = append(withCurrent, options...)
	return withCurrent
}

func arkModelEnumOptions() []ConfigEnumOption {
	cfg := currentBaseConfig()
	if cfg == nil || cfg.ArkConfig == nil {
		return nil
	}
	return buildConfigEnumOptions([]configEnumCandidate{
		{Value: cfg.ArkConfig.ReasoningModel, Source: "reasoning_model"},
		{Value: cfg.ArkConfig.NormalModel, Source: "normal_model"},
		{Value: cfg.ArkConfig.LiteModel, Source: "lite_model"},
	})
}

func openSearchIndexEnumOptions() []ConfigEnumOption {
	cfg := currentBaseConfig()
	if cfg == nil || cfg.OpensearchConfig == nil {
		return nil
	}
	return buildConfigEnumOptions([]configEnumCandidate{
		{Value: cfg.OpensearchConfig.LarkMsgIndex, Source: "lark_msg_index"},
		{Value: cfg.OpensearchConfig.LarkChunkIndex, Source: "lark_chunk_index"},
	})
}

type configEnumCandidate struct {
	Value  string
	Source string
}

func buildConfigEnumOptions(candidates []configEnumCandidate) []ConfigEnumOption {
	orderedValues := make([]string, 0, len(candidates))
	sourcesByValue := make(map[string][]string, len(candidates))
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate.Value)
		if value == "" {
			continue
		}
		if _, exists := sourcesByValue[value]; !exists {
			orderedValues = append(orderedValues, value)
		}
		source := strings.TrimSpace(candidate.Source)
		if source == "" || containsString(sourcesByValue[value], source) {
			continue
		}
		sourcesByValue[value] = append(sourcesByValue[value], source)
	}

	options := make([]ConfigEnumOption, 0, len(orderedValues))
	for _, value := range orderedValues {
		text := value
		if sources := sourcesByValue[value]; len(sources) > 0 {
			text = value + " | " + strings.Join(sources, "/")
		}
		options = append(options, ConfigEnumOption{
			Text:  text,
			Value: value,
		})
	}
	return options
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
