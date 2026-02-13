package ops

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/BetaGo/consts"
	"github.com/BetaGoRobot/BetaGo/utility"
	"github.com/BetaGoRobot/BetaGo/utility/database"
	"github.com/BetaGoRobot/BetaGo/utility/larkutils"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

type WordMatchType string

const (
	MatchTypeSubStr WordMatchType = "substr"
	MatchTypeRegex  WordMatchType = "regex"
	MatchTypeFull   WordMatchType = "full"
)

type ReplyType string

type ReplyNType struct {
	Reply     string    `json:"reply" gorm:"primaryKey;index"`
	ReplyType ReplyType `json:"reply_type" gorm:"primaryKey;index;default:text"`
}

const (
	ReplyTypeText ReplyType = "text"
	ReplyTypeImg  ReplyType = "img"
)

var _ Op = &WordReplyMsgOperator{}

// WordReplyMsgOperator  Repeat
//
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:11
type WordReplyMsgOperator struct {
	OpBase
}

func (r *WordReplyMsgOperator) Name() string {
	return "WordReplyMsgOperator"
}

// PreRun Repeat
//
//	@receiver r *WordReplyMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:17
func (r *WordReplyMsgOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	defer span.RecordError(err)

	if command.LarkRootCommand.IsCommand(ctx, larkutils.PreGetTextMsg(ctx, event)) {
		return errors.Wrap(consts.ErrStageSkip, r.Name()+" Not Mentioned")
	}
	return
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *WordReplyMsgOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	defer span.RecordError(err)

	msg := larkutils.PreGetTextMsg(ctx, event)
	var replyItem *ReplyNType
	// 检查定制化逻辑, Key为GuildID, 拿到GUI了dID下的所有SubStr配置
	ins := query.Q.QuoteReplyMsgCustom
	customConfig, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(*event.Event.Message.ChatId)).Find()
	replyList := make([]*ReplyNType, 0)
	for _, data := range customConfig {
		if CheckQuoteKeywordMatch(msg, data.Keyword, WordMatchType(data.MatchType)) {
			replyList = append(replyList, &ReplyNType{Reply: data.Reply, ReplyType: ReplyType(data.ReplyType)})
		}
	}

	if len(replyList) == 0 {
		// 无定制化逻辑，走通用判断
		data, hitCache := database.FindByCacheFunc(
			database.QuoteReplyMsg{},
			func(d database.QuoteReplyMsg) string {
				return d.Keyword
			},
		)
		span.SetAttributes(attribute.Bool("QuoteReplyMsg hitCache", hitCache))
		for _, d := range data {
			if CheckQuoteKeywordMatch(msg, d.Keyword, WordMatchType(d.MatchType)) {
				replyList = append(replyList, &ReplyNType{Reply: d.Reply, ReplyType: ReplyType(d.ReplyType)})
			}
		}
	}
	if len(replyList) > 0 {
		replyItem = utility.SampleSlice(replyList)
		_, subSpan := otel.T().Start(ctx, reflecting.GetCurrentFunc())
		defer subSpan.End()
		if replyItem.ReplyType == ReplyTypeText {
			_, err := larkutils.ReplyMsgText(ctx, replyItem.Reply, *event.Event.Message.MessageId, "_wordReply", false)
			if err != nil {
				logs.L().Ctx(ctx).Error("ReplyMessage error", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
				return err
			}
		} else if replyItem.ReplyType == ReplyTypeImg {
			var msgType, content string
			if strings.HasPrefix(replyItem.Reply, "img") {
				msgType = larkim.MsgTypeImage
				content, _ = sonic.MarshalString(map[string]string{
					"image_key": replyItem.Reply,
				})
			} else {
				msgType = larkim.MsgTypeSticker
				content, _ = sonic.MarshalString(map[string]string{
					"file_key": replyItem.Reply,
				})
			}
			_, err := larkutils.ReplyMsgRawContentType(ctx, *event.Event.Message.MessageId, msgType, content, "_wordReply", false)
			if err != nil {
				logs.L().Ctx(ctx).Error("ReplyMessage error", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
				return err
			}
		} else {
			return errors.New("unknown reply type")
		}

	}
	return
}

func CheckQuoteKeywordMatch(msg string, keyword string, matchType WordMatchType) bool {
	switch matchType {
	case MatchTypeFull:
		return msg == keyword
	case MatchTypeSubStr:
		return strings.Contains(msg, keyword)
	case MatchTypeRegex:
		return utility.RegexpMatch(msg, keyword)
	default:
		panic("unknown match type" + matchType)
	}
}
