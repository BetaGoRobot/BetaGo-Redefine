package cardhandlers

import (
	"context"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type MusicDetailCardView struct {
	Lyrics           string
	Title            string
	Subtitle         string
	ImageKey         string
	PlayerURL        string
	AudioFileKey     string
	AudioID          string
	RefreshTime      string
	FullLyricsButton map[string]string
	RefreshID        map[string]string
	JaegerTraceInfo  string
	JaegerTraceURL   string
	WithdrawInfo     string
	WithdrawTitle    string
	WithdrawConfirm  string
	WithdrawObject   map[string]string
}

func BuildMusicDetailRawCard(ctx context.Context, view MusicDetailCardView) larkmsg.RawCard {
	view = normalizeMusicDetailCardView(ctx, view)
	elements := []any{
		musicDetailPrimaryActions(view),
	}
	if strings.TrimSpace(view.AudioFileKey) != "" {
		elements = append(elements, musicDetailAudioSection(view))
	}
	elements = append(elements,
		musicDetailContentSection(view),
		musicDetailFooterActions(view),
		map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":        "plain_text",
				"content":    "卡片更新时间：" + view.RefreshTime,
				"text_size":  "notation",
				"text_align": "right",
				"text_color": "default",
			},
			"margin": "0px 0px 0px 0px",
		},
	)
	return larkmsg.RawCard{
		"schema": "2.0",
		"config": map[string]any{
			"update_multi":   true,
			"enable_forward": false,
		},
		"body": map[string]any{
			"direction":          "vertical",
			"horizontal_spacing": "8px",
			"vertical_spacing":   "8px",
			"horizontal_align":   "left",
			"vertical_align":     "top",
			"padding":            "12px 12px 12px 12px",
			"elements":           elements,
		},
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "plain_text",
				"content": view.Title,
			},
			"subtitle": map[string]any{
				"tag":     "plain_text",
				"content": view.Subtitle,
			},
			"template": "blue",
			"padding":  "12px 12px 12px 12px",
		},
	}
}

func normalizeMusicDetailCardView(ctx context.Context, view MusicDetailCardView) MusicDetailCardView {
	if strings.TrimSpace(view.RefreshTime) == "" {
		view.RefreshTime = time.Now().In(utils.UTC8Loc()).Format(time.DateTime)
	}
	if strings.TrimSpace(view.JaegerTraceInfo) == "" {
		view.JaegerTraceInfo = "Trace"
	}
	if strings.TrimSpace(view.JaegerTraceURL) == "" {
		traceID := oteltrace.SpanFromContext(ctx).SpanContext().TraceID().String()
		view.JaegerTraceURL = utils.GenTraceURL(traceID)
	}
	if strings.TrimSpace(view.WithdrawInfo) == "" {
		view.WithdrawInfo = "撤回卡片"
	}
	if strings.TrimSpace(view.WithdrawTitle) == "" {
		view.WithdrawTitle = "撤回本条消息"
	}
	if strings.TrimSpace(view.WithdrawConfirm) == "" {
		view.WithdrawConfirm = "你确定要撤回这条消息吗？"
	}
	if len(view.WithdrawObject) == 0 {
		view.WithdrawObject = map[string]string{cardaction.ActionField: cardaction.ActionCardWithdraw}
	}
	return view
}

func musicDetailPrimaryActions(view MusicDetailCardView) map[string]any {
	return map[string]any{
		"tag":                "column_set",
		"horizontal_spacing": "8px",
		"horizontal_align":   "left",
		"columns": []any{
			musicDetailButtonColumn(map[string]any{
				"tag":   "button",
				"text":  map[string]any{"tag": "plain_text", "content": "Play"},
				"type":  "primary_filled",
				"width": "default",
				"size":  "tiny",
				"icon":  map[string]any{"tag": "standard_icon", "token": "livestream-start_outlined"},
				"behaviors": []any{map[string]any{
					"type":        "open_url",
					"default_url": view.PlayerURL,
					"pc_url":      "",
					"ios_url":     "",
					"android_url": "",
				}},
				"margin": "0px 0px 0px 0px",
			}),
			musicDetailButtonColumn(map[string]any{
				"tag":   "button",
				"text":  map[string]any{"tag": "plain_text", "content": "完整歌词"},
				"type":  "primary",
				"width": "default",
				"size":  "tiny",
				"icon":  map[string]any{"tag": "standard_icon", "token": "hash_outlined"},
				"behaviors": []any{map[string]any{
					"type":  "callback",
					"value": larkmsg.StringMapToAnyMap(view.FullLyricsButton),
				}},
				"margin": "0px 0px 0px 0px",
			}),
		},
		"margin": "0px 0px 0px 0px",
	}
}

func musicDetailAudioSection(view MusicDetailCardView) map[string]any {
	audioID := strings.TrimSpace(view.AudioID)
	if audioID == "" {
		audioID = "music_detail_audio"
	}
	return map[string]any{
		"tag":               "audio",
		"element_id":        "music_audio",
		"margin":            "0px 0px 0px 0px",
		"file_key":          view.AudioFileKey,
		"audio_id":          audioID,
		"disabled":          false,
		"show_progress_bar": true,
		"show_time":         true,
		"time_display":      "default",
		"time_position":     "end",
		"style":            "normal",
		"padding":          "12px",
		"width":            "default",
		"fallback": map[string]any{
			"tag": "fallback_text",
			"text": map[string]any{
				"tag":     "plain_text",
				"content": "请升级至最新版本客户端，以播放音频。",
			},
		},
	}
}

func musicDetailContentSection(view MusicDetailCardView) map[string]any {
	return map[string]any{
		"tag":                "column_set",
		"horizontal_spacing": "8px",
		"horizontal_align":   "left",
		"columns": []any{
			map[string]any{
				"tag":    "column",
				"width":  "weighted",
				"weight": 1,
				"elements": []any{map[string]any{
					"tag":        "markdown",
					"content":    view.Lyrics,
					"text_align": "center",
					"text_size":  "notation",
					"margin":     "0px 0px 0px 0px",
				}},
				"vertical_spacing": "8px",
				"horizontal_align": "left",
				"vertical_align":   "top",
			},
			map[string]any{
				"tag":    "column",
				"width":  "weighted",
				"weight": 1,
				"elements": []any{map[string]any{
					"tag":           "img",
					"img_key":       view.ImageKey,
					"preview":       true,
					"transparent":   true,
					"scale_type":    "fit_horizontal",
					"corner_radius": "70%",
					"margin":        "0px 0px 0px 0px",
				}},
				"vertical_spacing": "8px",
				"horizontal_align": "left",
				"vertical_align":   "top",
			},
		},
		"margin": "0px 0px 0px 0px",
	}
}

func musicDetailFooterActions(view MusicDetailCardView) map[string]any {
	return map[string]any{
		"tag":              "column_set",
		"horizontal_align": "left",
		"columns": []any{map[string]any{
			"tag":       "column",
			"width":     "auto",
			"direction": "horizontal",
			"elements": []any{
				map[string]any{
					"tag":   "button",
					"text":  map[string]any{"tag": "plain_text", "content": "刷新URL"},
					"type":  "primary_filled",
					"width": "default",
					"size":  "medium",
					"icon":  map[string]any{"tag": "standard_icon", "token": "refresh_outlined"},
					"confirm": map[string]any{
						"title": map[string]any{"tag": "plain_text", "content": "刷新音源"},
						"text":  map[string]any{"tag": "plain_text", "content": "上次刷新时间【" + view.RefreshTime + "】，建议在7天后再尝试刷新音源，确认要刷新吗?"},
					},
					"behaviors": []any{map[string]any{"type": "callback", "value": larkmsg.StringMapToAnyMap(view.RefreshID)}},
				},
				map[string]any{
					"tag":   "button",
					"text":  map[string]any{"tag": "plain_text", "content": view.JaegerTraceInfo},
					"type":  "primary_filled",
					"width": "default",
					"size":  "medium",
					"icon":  map[string]any{"tag": "standard_icon", "token": "setting-inter_outlined"},
					"behaviors": []any{map[string]any{
						"type":        "open_url",
						"default_url": view.JaegerTraceURL,
						"pc_url":      "",
						"ios_url":     "",
						"android_url": "",
					}},
				},
				map[string]any{
					"tag":   "button",
					"text":  map[string]any{"tag": "plain_text", "content": view.WithdrawInfo},
					"type":  "danger_filled",
					"width": "default",
					"size":  "medium",
					"icon":  map[string]any{"tag": "standard_icon", "token": "warning_outlined"},
					"confirm": map[string]any{
						"title": map[string]any{"tag": "plain_text", "content": view.WithdrawTitle},
						"text":  map[string]any{"tag": "plain_text", "content": view.WithdrawConfirm},
					},
					"behaviors": []any{map[string]any{"type": "callback", "value": larkmsg.StringMapToAnyMap(view.WithdrawObject)}},
					"margin":    "0px 0px 0px 0px",
				},
			},
			"vertical_spacing": "8px",
			"horizontal_align": "left",
			"vertical_align":   "top",
		}},
	}
}

func musicDetailButtonColumn(button map[string]any) map[string]any {
	return map[string]any{
		"tag":   "column",
		"width": "auto",
		"elements": []any{
			button,
		},
		"vertical_spacing": "8px",
		"horizontal_align": "left",
		"vertical_align":   "top",
	}
}
