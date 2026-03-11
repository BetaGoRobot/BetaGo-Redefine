package larkmsg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type StandardCardFooterOptions struct {
	RefreshPayload     map[string]any
	RefreshText        string
	LastModifierOpenID string
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
	columns = append(columns, Column(cardFooterMeta(options), ColumnOptions{
		Width:           "weighted",
		Weight:          1,
		VerticalAlign:   "top",
		VerticalSpacing: "4px",
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

func cardFooterMeta(opts StandardCardFooterOptions) []any {
	meta := []any{
		TextDiv(cardUpdatedAtText(), CardTextOptions{
			Size:  "notation",
			Color: "grey",
			Align: "left",
		}),
	}
	if modifier := cardLastModifierElement(opts.LastModifierOpenID); modifier != nil {
		meta = append(meta, modifier)
	}
	return meta
}

func cardLastModifierElement(openID string) map[string]any {
	openID = strings.TrimSpace(openID)
	if openID == "" {
		return nil
	}
	showAvatar := true
	showName := true
	return ColumnSet([]any{
		Column([]any{TextDiv("最后修改", CardTextOptions{
			Size:  "notation",
			Color: "grey",
			Align: "left",
		})}, ColumnOptions{
			Width:         "auto",
			VerticalAlign: "center",
		}),
		Column([]any{Person(openID, PersonOptions{
			Size:       "extra_small",
			ShowAvatar: &showAvatar,
			ShowName:   &showName,
			Style:      "capsule",
			Margin:     "0",
		})}, ColumnOptions{
			Width:         "auto",
			VerticalAlign: "center",
		}),
	}, ColumnSetOptions{
		HorizontalSpacing: "4px",
	})
}

func cardUpdatedAtText() string {
	return "更新于 " + time.Now().In(utils.UTC8Loc()).Format(time.DateTime)
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
