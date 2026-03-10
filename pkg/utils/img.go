package utils

import (
	"context"
	"io"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

func ResizeIMGFromReader(ctx context.Context, r io.ReadCloser) (output []byte) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	imgBody, err := io.ReadAll(r)
	if err != nil {
		logs.L().Ctx(ctx).Error("read image error", zap.Error(err))
		return
	}
	// newImage, err := bimg.NewImage(imgBody).Resize(512, 512)
	// if err != nil {
	// 	fmt.Fprintln(os.Stderr, err)
	// }
	return imgBody
}
