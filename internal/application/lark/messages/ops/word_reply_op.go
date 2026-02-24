package ops

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"gorm.io/gorm"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"go.uber.org/zap"
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

	if command.LarkRootCommand.IsCommand(ctx, larkmsg.PreGetTextMsg(ctx, event)) {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" Not Mentioned")
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

	msg := larkmsg.PreGetTextMsg(ctx, event)
	var replyItem *xmodel.ReplyNType
	// 检查定制化逻辑, Key为GuildID, 拿到GUI了dID下的所有SubStr配置
	ins := query.Q.QuoteReplyMsgCustom
	customConfig, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(*event.Event.Message.ChatId)).Find()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logs.L().Ctx(ctx).Error("get quote reply config from db failed", zap.Error(err))
		return
	}
	replyList := make([]*xmodel.ReplyNType, 0)
	for _, data := range customConfig {
		if CheckQuoteKeywordMatch(msg, data.Keyword, xmodel.WordMatchType(data.MatchType)) {
			replyList = append(replyList, &xmodel.ReplyNType{Reply: data.Reply, ReplyType: xmodel.ReplyType(data.ReplyType)})
		}
	}

	if len(replyList) == 0 {
		// 无定制化逻辑，走通用判断
		ins := query.Q.QuoteReplyMsg
		data, err := ins.WithContext(ctx).Find()
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			logs.L().Ctx(ctx).Error("FindByCacheFunc error", zap.Error(err))
			return err
		}
		for _, d := range data {
			if CheckQuoteKeywordMatch(msg, d.Keyword, xmodel.WordMatchType(d.MatchType)) {
				replyList = append(replyList, &xmodel.ReplyNType{Reply: d.Reply, ReplyType: xmodel.ReplyType(d.ReplyType)})
			}
		}
	}
	if len(replyList) > 0 {
		replyItem = utils.SampleSlice(replyList)
		_, subSpan := otel.T().Start(ctx, reflecting.GetCurrentFunc())
		defer subSpan.End()
		if replyItem.ReplyType == xmodel.ReplyTypeText {
			_, err := larkmsg.ReplyMsgText(ctx, replyItem.Reply, *event.Event.Message.MessageId, "_wordReply", false)
			if err != nil {
				logs.L().Ctx(ctx).Error("ReplyMessage error", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
				return err
			}
		} else if replyItem.ReplyType == xmodel.ReplyTypeImg {
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
			_, err := larkmsg.ReplyMsgRawContentType(ctx, *event.Event.Message.MessageId, msgType, content, "_wordReply", false)
			if err != nil {
				logs.L().Ctx(ctx).Error("ReplyMessage error", zap.Error(err), zap.String("TraceID", span.SpanContext().TraceID().String()))
				return err
			}
		} else {
			return errors.New("unknown reply type")
		}

	}
	return nil
}

func CheckQuoteKeywordMatch(msg string, keyword string, matchType xmodel.WordMatchType) bool {
	switch matchType {
	case xmodel.MatchTypeFull:
		return msg == keyword
	case xmodel.MatchTypeSubStr:
		return strings.Contains(msg, keyword)
	case xmodel.MatchTypeRegex:
		return utils.RegexpMatch(msg, keyword)
	default:
		panic("unknown match type" + matchType)
	}
}
