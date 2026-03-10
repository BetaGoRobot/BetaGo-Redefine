package xcopywriting

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ImgAdd               = "image_add"
	ImgAddRespAlreadyAdd = "image_add_resp_already_add"
	ImgAddRespAddSuccess = "image_add_resp_add_success"

	ImgNotStickerOrIMG = "image_not_sticker_or_img"
	ImgNotAnyValidArgs = "image_not_any_valid_args"
	ImgQuoteNoParent   = "image_quote_no_parent"
)

func GetCopyWritings(ctx context.Context, chatID, endPoint string) []string {
	ctx, span := otel.Start(ctx)
	defer span.End()

	// custom copy writing
	ins := query.Q.CopyWritingCustom
	customRes, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(chatID), ins.Endpoint.Eq(endPoint)).Find()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		otel.RecordError(span, err)
		return []string{}
	}
	if len(customRes) != 0 && len(customRes[0].Content) != 0 {
		return customRes[0].Content
	} else {
		ins.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CopyWritingCustom{
			GuildID: chatID, Endpoint: endPoint, Content: []string{},
		})
	}

	// default copy writing
	ins2 := query.Q.CopyWritingGeneral
	generalRes, err := ins2.WithContext(ctx).Where(ins2.Endpoint.Eq(endPoint)).Find()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		otel.RecordError(span, err)
		return []string{}
	}
	if len(generalRes) != 0 && len(generalRes[0].Content) != 0 {
		return generalRes[0].Content
	} else {
		ins2.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CopyWritingGeneral{
			Endpoint: endPoint, Content: []string{},
		})
	}

	return []string{endPoint}
}

func GetSampleCopyWritings(ctx context.Context, chatID, endPoint string) string {
	ctx, span := otel.Start(ctx)
	defer span.End()

	return utils.SampleSlice(GetCopyWritings(ctx, chatID, endPoint))
}
