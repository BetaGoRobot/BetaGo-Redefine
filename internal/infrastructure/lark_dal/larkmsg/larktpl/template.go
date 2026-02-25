package larktpl

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/consts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/gg/gptr"
	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type TemplateStru struct {
	TemplateID      string
	TemplateVersion string
}

var (
	FourColSheetTemplate         = "AAq0LWXpn9FbS"
	ThreeColSheetTemplate        = "AAq0LIyUeFhNX"
	TwoColSheetTemplate          = "AAq0LPliGGphg"
	TwoColPicTemplate            = "AAq0LPJqOoh3s"
	AlbumListTemplate            = "AAqdqaEBaxJaf"
	SingleSongDetailTemplate     = "AAqdrtjg8g1s8"
	FullLyricsTemplate           = "AAq3mcb9ivduh"
	StreamingReasonTemplate      = "ONLY_SRC_STERAMING_CARD"
	NormalCardReplyTemplate      = "AAqRQtNPSJbsZ"
	NormalCardGraphReplyTemplate = "AAqdmx3wt8mit"
	ChunkMetaTemplate            = "AAqxfVYYV3Zcr"
	WordCountTemplate            = "AAqx4lG1w3cug"
)

type TemplateVersionV2[T any] struct {
	model.TemplateVersion
	Variables *T
}

func (t *TemplateVersionV2[T]) WithData(data *T) TemplateVersionV2[T] {
	t.Variables = data
	return *t
}

func GetTemplate(ctx context.Context, templateID string) *model.TemplateVersion {
	ins := query.Q.TemplateVersion
	template, err := ins.WithContext(ctx).Where(ins.TemplateID.Eq(templateID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &model.TemplateVersion{TemplateID: templateID}
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logs.L().Ctx(ctx).Error("get templates from db error", zap.String("templateID", template.TemplateID), zap.Error(err))
	}
	return template
}

func GetTemplateV2[T any](ctx context.Context, templateID string) TemplateVersionV2[T] {
	ins := query.Q.TemplateVersion
	template, err := ins.WithContext(ctx).Where(ins.TemplateID.Eq(templateID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TemplateVersionV2[T]{TemplateVersion: model.TemplateVersion{TemplateID: templateID}}
	}
	if err != nil {
		logs.L().Ctx(ctx).Error("get templates from db error", zap.String("templateID", template.TemplateID), zap.Error(err))
	}
	if template != nil {
		return TemplateVersionV2[T]{TemplateVersion: *template}
	}
	return TemplateVersionV2[T]{}
}

type (
	TemplateCardContent struct {
		Type string   `json:"type"` // must be template
		Data CardData `json:"data"`
	}
	CardData struct {
		TemplateID          string                 `json:"template_id"`
		TemplateVersionName string                 `json:"template_version_name"`
		TemplateVariable    map[string]interface{} `json:"template_variable"`
		TemplateSrc         string                 `json:"template_src"`
	}
)

func NewCardContent(ctx context.Context, templateID string) *TemplateCardContent {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	traceID := span.SpanContext().TraceID().String()
	templateVersion := GetTemplate(ctx, templateID)
	var t *TemplateCardContent
	// 纯template
	t = &TemplateCardContent{
		Type: "template",
		Data: CardData{
			TemplateID:          templateVersion.TemplateID,
			TemplateVersionName: templateVersion.TemplateVersion,
			TemplateVariable:    make(map[string]interface{}),
		},
	}
	if templateVersion.TemplateSrc != "" {
		t.Data.TemplateSrc = templateVersion.TemplateSrc
	}

	// default参数
	t.AddJaegerTraceInfo(traceID)
	t.AddVariable("withdraw_info", "撤回卡片")
	t.AddVariable("withdraw_title", "撤回本条消息")
	t.AddVariable("withdraw_confirm", "你确定要撤回这条消息吗？")
	t.AddVariable("withdraw_object", map[string]string{"type": "withdraw"})
	if srcCmd := ctx.Value(consts.ContextVarSrcCmd); srcCmd != nil {
		t.AddVariable("raw_cmd", srcCmd.(string))
		t.AddVariable("refresh_obj", map[string]string{"type": "refresh_obj", "command": srcCmd.(string)})
	}
	t.AddVariable("refresh_time", time.Now().In(utils.UTC8Loc()).Format(time.DateTime))
	return t
}

func NewCardContentV2[T any](ctx context.Context, templateID string) *TemplateCardContent {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	traceID := span.SpanContext().TraceID().String()
	templateVersion := GetTemplateV2[T](ctx, templateID)
	var t *TemplateCardContent
	// 纯template
	t = &TemplateCardContent{
		Type: "template",
		Data: CardData{
			TemplateID:          templateVersion.TemplateID,
			TemplateVersionName: templateVersion.TemplateVersion.TemplateVersion,
			TemplateVariable:    make(map[string]interface{}),
		},
	}
	if templateVersion.TemplateSrc != "" {
		t.Data.TemplateSrc = templateVersion.TemplateSrc
	}

	// default参数
	v := CardBaseVars{
		JaegerTraceInfo: "Trace",
		JaegerTraceURL:  utils.GenTraceURL(traceID),
		WithdrawInfo:    "撤回卡片",
		WithdrawTitle:   "撤回本条消息",
		WithdrawConfirm: "你确定要撤回这条消息吗？",
		WithdrawObject:  WithDrawObj{Type: "withdraw"},
		RefreshTime:     time.Now().In(utils.UTC8Loc()).Format(time.DateTime),
	}
	if srcCmd := ctx.Value(consts.ContextVarSrcCmd); srcCmd != nil {
		v.RawCmd = gptr.Of(srcCmd.(string))
		v.RefreshObj = &RefreshObj{Type: "refresh_obj", Command: srcCmd.(string)}
	}
	t.AddVariableStruct(v)

	// 合并
	variables, _ := sonic.Marshal(templateVersion.Variables)
	var sourceMap map[string]any
	sonic.Unmarshal(variables, &sourceMap)
	maps.Copy(t.Data.TemplateVariable, sourceMap)
	return t
}

func (c *TemplateCardContent) AddJaegerTraceInfo(traceID string) *TemplateCardContent {
	return c.AddVariable("jaeger_trace_info", "Trace").
		AddVariable("jaeger_trace_url", utils.GenTraceURL(traceID))
}

func (c *TemplateCardContent) AddVariable(key string, value interface{}) *TemplateCardContent {
	c.Data.TemplateVariable[key] = value
	return c
}

func (c *TemplateCardContent) AddVariableStruct(value any) *TemplateCardContent {
	// 结构体转换为map
	variables, _ := sonic.Marshal(value)
	var sourceMap map[string]any
	sonic.Unmarshal(variables, &sourceMap)
	maps.Copy(c.Data.TemplateVariable, sourceMap)
	return c
}

func (c *TemplateCardContent) UpdateVariables(m map[string]interface{}) *TemplateCardContent {
	for k, v := range m {
		c.Data.TemplateVariable[k] = v
	}
	return c
}

func (c *TemplateCardContent) GetVariables() []string {
	s := make([]string, 0, len(c.Data.TemplateVariable))
	for _, v := range c.Data.TemplateVariable {
		s = append(s, fmt.Sprint(v))
	}
	return s
}

func (c *TemplateCardContent) String() string {
	if c == nil {
		return ""
	}
	if c.Data.TemplateSrc == "" {
		res, err := sonic.MarshalString(c)
		if err != nil {
			return ""
		}
		return res
	}
	replacedSrc := c.Data.TemplateSrc
	for k, v := range c.Data.TemplateVariable {
		s, _ := sonic.MarshalString(v)
		s = strings.Trim(s, "\"")
		switch v.(type) {
		case string:
			replacedSrc = strings.ReplaceAll(replacedSrc, "${"+k+"}", s)
		default:
			replacedSrc = strings.ReplaceAll(replacedSrc, "\"${"+k+"}\"", s)
		}
	}
	return replacedSrc
}

func (c *TemplateCardContent) DataString() string {
	if c == nil {
		return ""
	}
	if c.Data.TemplateSrc == "" {
		res, err := sonic.MarshalString(c.Data)
		if err != nil {
			return ""
		}
		return res
	}
	replacedSrc := c.Data.TemplateSrc
	for k, v := range c.Data.TemplateVariable {
		s, _ := sonic.MarshalString(v)
		s = strings.Trim(s, "\"")
		switch v.(type) {
		case string:
			replacedSrc = strings.ReplaceAll(replacedSrc, "${"+k+"}", s)
		default:
			replacedSrc = strings.ReplaceAll(replacedSrc, "\"${"+k+"}\"", s)
		}
	}
	return replacedSrc
}
