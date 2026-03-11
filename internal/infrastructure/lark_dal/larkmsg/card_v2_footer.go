package larkmsg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type StandardCardFooterOptions struct {
	RefreshPayload map[string]any
	RefreshText    string
}

func AppendStandardCardFooter(ctx context.Context, elements []any, opts ...StandardCardFooterOptions) []any {
	result := append([]any{}, elements...)
	result = append(result, Divider(), StandardCardFooter(ctx, opts...))
	return result
}

func StandardCardFooter(ctx context.Context, opts ...StandardCardFooterOptions) map[string]any {
	options := standardCardFooterOptions(opts)
	traceID := cardTraceID(ctx)
	traceURL := safeTraceURL(traceID)
	buttons := make([]map[string]any, 0, 3)
	if len(options.RefreshPayload) > 0 {
		buttons = append(buttons, Button(options.refreshText(), ButtonOptions{
			Type:    "default",
			Size:    "small",
			Payload: options.RefreshPayload,
		}))
	}
	buttons = append(buttons,
		Button("撤回", ButtonOptions{
			Type: "danger_filled",
			Size: "small",
			Payload: map[string]any{
				cardaction.ActionField: cardaction.ActionCardWithdraw,
			},
		}),
	)
	if traceURL != "" {
		buttons = append(buttons, Button("Trace", ButtonOptions{
			Size: "small",
			URL:  traceURL,
		}))
	}

	columns := make([]any, 0, len(buttons)+1)
	columns = append(columns, Column([]any{TextDiv(cardUpdatedAtText(), CardTextOptions{
		Size:  "notation",
		Color: "grey",
		Align: "left",
	})}, ColumnOptions{
		Width:         "weighted",
		Weight:        1,
		VerticalAlign: "top",
	}))
	for _, button := range buttons {
		columns = append(columns, Column([]any{button}, ColumnOptions{
			Width:         "auto",
			VerticalAlign: "top",
		}))
	}

	return ColumnSet(columns, ColumnSetOptions{
		HorizontalSpacing: "8px",
	})
}

func standardCardFooterOptions(opts []StandardCardFooterOptions) StandardCardFooterOptions {
	if len(opts) == 0 {
		return StandardCardFooterOptions{}
	}
	return opts[0]
}

func (opts StandardCardFooterOptions) refreshText() string {
	if opts.RefreshText != "" {
		return opts.RefreshText
	}
	return "刷新"
}

func cardUpdatedAtText() string {
	return "更新于 " + time.Now().In(utils.UTC8Loc()).Format("01-02 15:04:05")
}

func cardTraceID(ctx context.Context) string {
	spanCtx := oteltrace.SpanContextFromContext(ctx)
	if spanCtx.HasTraceID() {
		return spanCtx.TraceID().String()
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := otel.Start(ctx)
	traceID := ""
	if spanCtx := span.SpanContext(); spanCtx.HasTraceID() {
		traceID = spanCtx.TraceID().String()
	} else {
		traceID = fallbackTraceID()
	}
	span.End()
	return traceID
}

func safeTraceURL(traceID string) (traceURL string) {
	if traceID == "" {
		return ""
	}
	defer func() {
		if recover() != nil {
			traceURL = ""
		}
	}()
	return utils.GenTraceURL(traceID)
}

func fallbackTraceID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
