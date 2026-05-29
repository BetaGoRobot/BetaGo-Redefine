package neteaseapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/consts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func timeNowString() string {
	return time.Now().In(utils.UTC8Loc()).Format(time.DateTime)
}

func BuildMusicListRawCard(ctx context.Context, vars *larktpl.MusicListCardVars) larkmsg.RawCard {
	if vars == nil {
		vars = &larktpl.MusicListCardVars{}
	}
	vars = withMusicListBaseVars(ctx, vars)
	return larkmsg.RawCard{
		"schema": "2.0",
		"config": map[string]any{
			"update_multi": true,
			"style": map[string]any{
				"text_size": map[string]any{
					"normal_v2": map[string]any{
						"default": "normal",
						"pc":      "normal",
						"mobile":  "heading",
					},
				},
			},
			"streaming_mode": true,
		},
		"body": map[string]any{
			"direction":          "vertical",
			"horizontal_spacing": "8px",
			"vertical_spacing":   "8px",
			"horizontal_align":   "left",
			"vertical_align":     "top",
			"padding":            "12px 12px 12px 12px",
			"elements":           musicListRawCardElements(vars),
		},
		"header": map[string]any{
			"title":    larkmsg.PlainText(fmt.Sprintf("%s的检索结果", vars.Query)),
			"subtitle": larkmsg.PlainText(""),
			"template": "blue",
			"icon": map[string]any{
				"tag":   "standard_icon",
				"token": "search_outlined",
			},
			"padding": "12px 12px 12px 12px",
		},
	}
}

func withMusicListBaseVars(ctx context.Context, vars *larktpl.MusicListCardVars) *larktpl.MusicListCardVars {
	next := *vars
	if next.JaegerTraceInfo == "" {
		traceID := oteltrace.SpanFromContext(ctx).SpanContext().TraceID().String()
		next.JaegerTraceInfo = "Trace"
		next.JaegerTraceURL = utils.GenTraceURL(traceID)
	}
	if next.WithdrawInfo == "" {
		next.WithdrawInfo = "撤回卡片"
		next.WithdrawTitle = "撤回本条消息"
		next.WithdrawConfirm = "你确定要撤回这条消息吗？"
		next.WithdrawObject = larktpl.WithDrawObj{Action: cardaction.ActionCardWithdraw}
	}
	if next.RefreshTime == "" {
		next.RefreshTime = timeNowString()
	}
	if next.FirstReplyCost == "" {
		next.FirstReplyCost = xhandler.PipelineElapsedString(ctx)
	}
	if srcCmd := ctx.Value(consts.ContextVarSrcCmd); srcCmd != nil && next.RawCmd == nil {
		if raw, ok := srcCmd.(string); ok {
			next.RawCmd = new(raw)
			next.RefreshObj = &larktpl.RefreshObj{Action: cardaction.ActionCommandRefresh, Command: raw}
		}
	}
	return &next
}

func musicListRawCardElements(vars *larktpl.MusicListCardVars) []any {
	elements := make([]any, 0, len(vars.ObjectList1)+3)
	for _, item := range vars.ObjectList1 {
		if item == nil {
			continue
		}
		elements = append(elements, musicListRawCardItem(item))
	}
	elements = append(elements,
		musicListFooterActions(vars),
		map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":        "plain_text",
				"content":    musicListTimingText(vars),
				"text_size":  "notation",
				"text_align": "right",
				"text_color": "default",
			},
			"margin": "0px 0px 0px 0px",
		},
	)
	return elements
}

func musicListTimingText(vars *larktpl.MusicListCardVars) string {
	if vars == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if strings.TrimSpace(vars.FirstReplyCost) != "" {
		parts = append(parts, "首次回复耗时："+vars.FirstReplyCost)
		finalCost := strings.TrimSpace(vars.FinalPatchCost)
		if finalCost == "" {
			finalCost = "计算中"
		}
		parts = append(parts, "最终Patch耗时："+finalCost)
	}
	parts = append(parts, "卡片更新时间："+vars.RefreshTime)
	return strings.Join(parts, " · ")
}

func musicListRawCardItem(item *larktpl.MusicListCardItem) map[string]any {
	return map[string]any{
		"tag":                "column_set",
		"flex_mode":          "stretch",
		"background_style":   "grey-100",
		"horizontal_spacing": "8px",
		"horizontal_align":   "left",
		"columns": []any{
			map[string]any{
				"tag":   "column",
				"width": "auto",
				"elements": []any{
					map[string]any{
						"tag":         "img",
						"img_key":     item.Field2.ImgKey,
						"preview":     true,
						"transparent": true,
						"scale_type":  "crop_center",
						"size":        "medium",
						"margin":      "0px 0px 0px 0px",
					},
				},
				"padding":            "0px 0px 0px 0px",
				"direction":          "vertical",
				"horizontal_spacing": "8px",
				"vertical_spacing":   "8px",
				"horizontal_align":   "left",
				"vertical_align":     "top",
				"margin":             "0px 0px 0px 0px",
			},
			map[string]any{
				"tag":              "column",
				"width":            "weighted",
				"vertical_spacing": "8px",
				"horizontal_align": "left",
				"vertical_align":   "top",
				"weight":           1,
				"elements":         []any{musicListContentColumns(item)},
			},
		},
		"margin": "0px 0px 0px 0px",
	}
}

func musicListContentColumns(item *larktpl.MusicListCardItem) map[string]any {
	return map[string]any{
		"tag":                "column_set",
		"horizontal_spacing": "8px",
		"horizontal_align":   "left",
		"columns": []any{
			map[string]any{
				"tag":              "column",
				"width":            "weighted",
				"vertical_spacing": "8px",
				"horizontal_align": "left",
				"vertical_align":   "top",
				"weight":           3,
				"elements": []any{
					map[string]any{
						"tag":        "markdown",
						"content":    item.Field1,
						"text_align": "left",
						"text_size":  "normal_v2",
						"margin":     "0px 0px 0px 0px",
					},
					musicListButton(item.ButtonInfo, "primary_filled", "small", "link-copy_outlined", false, item.ButtonVal),
				},
				"padding":            "0px 0px 0px 0px",
				"direction":          "vertical",
				"horizontal_spacing": "8px",
				"margin":             "0px 0px 0px 0px",
			},
			map[string]any{
				"tag":              "column",
				"width":            "weighted",
				"vertical_spacing": "8px",
				"horizontal_align": "left",
				"vertical_align":   "top",
				"weight":           2,
				"elements": []any{
					map[string]any{
						"tag": "div",
						"text": map[string]any{
							"tag":        "plain_text",
							"content":    item.Field3,
							"text_size":  "notation",
							"text_align": "left",
							"text_color": "grey",
							"lines":      3,
						},
						"icon": map[string]any{
							"tag":   "standard_icon",
							"token": "pen_outlined",
							"color": "light_grey",
						},
					},
					map[string]any{
						"tag": "div",
						"text": map[string]any{
							"tag":        "plain_text",
							"content":    "ID:" + item.ElementID,
							"text_size":  "notation",
							"text_align": "left",
							"text_color": "default",
						},
						"margin": "0px 0px 0px 0px",
					},
					map[string]any{
						"tag":        "markdown",
						"content":    item.CommentTime,
						"text_align": "right",
						"text_size":  "notation",
						"margin":     "0px 0px 0px 0px",
					},
				},
			},
		},
		"margin": "0px 0px 0px 0px",
	}
}

func musicListFooterActions(vars *larktpl.MusicListCardVars) map[string]any {
	return map[string]any{
		"tag":                "column_set",
		"horizontal_spacing": "8px",
		"horizontal_align":   "left",
		"columns": []any{
			map[string]any{
				"tag":                "column",
				"width":              "weighted",
				"direction":          "horizontal",
				"horizontal_spacing": "8px",
				"vertical_spacing":   "8px",
				"horizontal_align":   "left",
				"vertical_align":     "top",
				"weight":             1,
				"elements": []any{
					openURLButton(vars.JaegerTraceInfo, "primary_filled", "medium", "setting-inter_outlined", vars.JaegerTraceURL),
					withdrawButton(vars),
				},
			},
			map[string]any{
				"tag":                "column",
				"width":              "weighted",
				"padding":            "0px 0px 0px 0px",
				"direction":          "horizontal",
				"horizontal_spacing": "8px",
				"vertical_spacing":   "0px",
				"horizontal_align":   "center",
				"vertical_align":     "bottom",
				"margin":             "0px 0px 0px 0px",
				"weight":             1,
				"elements": []any{
					map[string]any{
						"tag":        "markdown",
						"content":    vars.PageInfoText,
						"text_align": "center",
						"text_size":  "notation",
						"margin":     "0px 0px 0px 0px",
					},
				},
			},
			map[string]any{
				"tag":                "column",
				"width":              "weighted",
				"padding":            "0px 0px 0px 0px",
				"direction":          "horizontal",
				"horizontal_spacing": "8px",
				"vertical_spacing":   "8px",
				"horizontal_align":   "right",
				"vertical_align":     "top",
				"margin":             "0px 0px 0px 0px",
				"weight":             1,
				"elements": []any{
					musicListButton("上一页", "default", "medium", "left_outlined", vars.HasPrev, vars.PrevPageVal),
					musicListButton("下一页", "default", "medium", "right-small-ccm_outlined", vars.HasNext, vars.NextPageVal),
				},
			},
		},
	}
}

func musicListButton(text, buttonType, size, icon string, disabled bool, value map[string]string) map[string]any {
	button := map[string]any{
		"tag":   "button",
		"text":  larkmsg.PlainText(text),
		"type":  buttonType,
		"width": "default",
		"size":  size,
		"icon": map[string]any{
			"tag":   "standard_icon",
			"token": icon,
		},
		"margin": "0px 0px 0px 0px",
	}
	if len(value) > 0 {
		button["behaviors"] = []any{
			map[string]any{
				"type":  "callback",
				"value": value,
			},
		}
	}
	if disabled {
		button["disabled"] = true
	}
	return button
}

func openURLButton(text, buttonType, size, icon, url string) map[string]any {
	return map[string]any{
		"tag":   "button",
		"text":  larkmsg.PlainText(text),
		"type":  buttonType,
		"width": "default",
		"size":  size,
		"icon": map[string]any{
			"tag":   "standard_icon",
			"token": icon,
		},
		"behaviors": []any{
			map[string]any{
				"type":        "open_url",
				"default_url": url,
				"pc_url":      "",
				"ios_url":     "",
				"android_url": "",
			},
		},
	}
}

func withdrawButton(vars *larktpl.MusicListCardVars) map[string]any {
	button := musicListButton(vars.WithdrawInfo, "danger_filled", "medium", "warning_outlined", false, map[string]string{
		"action": vars.WithdrawObject.Action,
	})
	button["confirm"] = map[string]any{
		"title": larkmsg.PlainText(vars.WithdrawTitle),
		"text":  larkmsg.PlainText(vars.WithdrawConfirm),
	}
	return button
}
