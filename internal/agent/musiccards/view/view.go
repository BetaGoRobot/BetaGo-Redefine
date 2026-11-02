// Package view renders music cards from caller-supplied presentation data.
// It does not fetch media, decide authority, or create callback credentials.
package view

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

const MaxCardBytes = 28 * 1024
const PageSize = 5

var ErrCardTooLarge = errors.New("music card exceeds 28 KiB")

type Action struct {
	Text         string         `json:"text"`
	Value        map[string]any `json:"value,omitempty"`
	URL          string         `json:"url,omitempty"`
	Disabled     bool           `json:"disabled,omitempty"`
	ConfirmTitle string         `json:"confirm_title,omitempty"`
	ConfirmText  string         `json:"confirm_text,omitempty"`
}

type Item struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	ImageKey       string `json:"image_key,omitempty"`
	Comment        string `json:"comment,omitempty"`
	CommentTime    string `json:"comment_time,omitempty"`
	ImageLoading   bool   `json:"image_loading,omitempty"`
	CommentLoading bool   `json:"comment_loading,omitempty"`
	Action         Action `json:"action"`
}

// List.Items is the already selected visible page, containing at most five rows.
// Page is one-based. Zero page/count values use a single-page empty-state default.
type List struct {
	Title      string `json:"title"`
	Items      []Item `json:"items"`
	Page       int    `json:"page"`
	TotalPages int    `json:"total_pages"`
	Total      int    `json:"total"`
	Prev       Action `json:"prev"`
	Next       Action `json:"next"`
	Refresh    Action `json:"refresh"`
	Withdraw   Action `json:"withdraw"`
	UpdatedAt  string `json:"updated_at"`
	Notice     string `json:"notice,omitempty"`
}

type Detail struct {
	Title            string `json:"title"`
	Subtitle         string `json:"subtitle"`
	ImageKey         string `json:"image_key"`
	Lyrics           string `json:"lyrics"`
	PlayerURL        string `json:"player_url"`
	SourceURL        string `json:"source_url"`
	AudioFileKey     string `json:"audio_file_key"`
	AudioID          string `json:"audio_id"`
	DurationMS       int64  `json:"duration_ms"`
	FullLyricsAction Action `json:"full_lyrics_action"`
	AlbumAction      Action `json:"album_action"`
	Refresh          Action `json:"refresh"`
	Withdraw         Action `json:"withdraw"`
	UpdatedAt        string `json:"updated_at"`
	Notice           string `json:"notice,omitempty"`
}

type Lyrics struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Text        string `json:"text"`
	ImageKey    string `json:"image_key"`
	DownloadURL string `json:"download_url"`
	Withdraw    Action `json:"withdraw"`
	UpdatedAt   string `json:"updated_at"`
}

// RenderList preserves the rich, single-action row layout. Enrichment states are
// independent: a resolved cover does not imply that a comment has resolved.
func RenderList(v List) (string, error) {
	if len(v.Items) > PageSize {
		return "", fmt.Errorf("music list page exceeds %d rows", PageSize)
	}
	if v.Page < 0 || v.TotalPages < 0 || v.Total < 0 {
		return "", errors.New("invalid music list pagination")
	}
	if v.Total == 0 {
		v.Total = len(v.Items)
	}
	if v.TotalPages == 0 {
		v.TotalPages = max(1, (v.Total+PageSize-1)/PageSize)
	}
	if v.Page == 0 {
		v.Page = 1
	}
	if v.Page > v.TotalPages {
		return "", errors.New("music list page outside available range")
	}
	r := &renderer{}
	elements := make([]any, 0, len(v.Items)+3)
	for i, item := range v.Items {
		elements = append(elements, r.listItem(item, i))
	}
	if len(v.Items) == 0 {
		elements = append(elements, note("暂无音乐结果", "left"))
	}
	if v.Notice != "" {
		elements = append(elements, note(v.Notice, "left"))
	}
	prev, next := v.Prev, v.Next
	prev.Text, next.Text = "上一页", "下一页"
	prev.Disabled = prev.Disabled || v.Page <= 1
	next.Disabled = next.Disabled || v.Page >= v.TotalPages
	elements = append(elements, columns(
		column(r.management(v.Refresh, v.Withdraw), 1, "horizontal", "left"),
		column([]any{markdown(fmt.Sprintf("第 %d / %d 页 · 共 %d 项", v.Page, v.TotalPages, v.Total), "center", "notation")}, 1, "horizontal", "center"),
		column([]any{r.button(prev, "default", "medium", "left_outlined"), r.button(next, "default", "medium", "right-small-ccm_outlined")}, 1, "horizontal", "right"),
	), updated(v.UpdatedAt))
	c := baseCard(v.Title, "", elements)
	c["config"].(map[string]any)["style"] = map[string]any{"text_size": map[string]any{"normal_v2": map[string]any{"default": "normal", "pc": "normal", "mobile": "heading"}}}
	return r.marshal(c)
}

// RenderDetail includes native audio only when an uploaded handle is available.
// The caller can render again without AudioFileKey for clients rejecting audio.
func RenderDetail(v Detail) (string, error) {
	r := &renderer{}
	if !validOptionalURL(v.PlayerURL) || !validOptionalURL(v.SourceURL) {
		return "", errors.New("invalid music playback URL")
	}
	if v.DurationMS < 0 {
		return "", errors.New("invalid music duration")
	}
	playerURL := v.PlayerURL
	if playerURL == "" {
		playerURL = v.SourceURL
	}
	primary := make([]any, 0, 3)
	if playerURL != "" {
		primary = append(primary, autoColumn([]any{r.button(Action{Text: "Play", URL: playerURL}, "primary_filled", "tiny", "livestream-start_outlined")}, "vertical"))
	} else {
		primary = append(primary, autoColumn([]any{note("播放器暂不可用", "left")}, "vertical"))
	}
	if actionPresent(v.FullLyricsAction) {
		a := v.FullLyricsAction
		if a.Text == "" {
			a.Text = "完整歌词"
		}
		primary = append(primary, autoColumn([]any{r.button(a, "primary", "tiny", "hash_outlined")}, "vertical"))
	}
	if actionPresent(v.AlbumAction) {
		a := v.AlbumAction
		if a.Text == "" {
			a.Text = "查看专辑"
		}
		primary = append(primary, autoColumn([]any{r.button(a, "default", "tiny", "music_outlined")}, "vertical"))
	}
	elements := []any{columns(primary...)}
	if strings.TrimSpace(v.AudioFileKey) != "" {
		id := strings.TrimSpace(v.AudioID)
		if id == "" {
			id = "music_detail_audio"
		}
		elements = append(elements, map[string]any{
			"tag": "audio", "element_id": "music_audio", "margin": "0px 0px 0px 0px",
			"file_key": v.AudioFileKey, "audio_id": id, "disabled": false,
			"show_progress_bar": true, "show_time": true, "time_display": "both", "time_position": "end",
			"style": "normal", "padding": "12px", "width": "fill",
			"fallback": map[string]any{"tag": "fallback_text", "text": plain("请升级至最新版本客户端，以播放音频；也可使用 Play 打开播放器。")},
		})
	}
	if v.DurationMS > 0 {
		seconds := v.DurationMS / 1000
		elements = append(elements, note(fmt.Sprintf("时长 %02d:%02d", seconds/60, seconds%60), "left"))
	}
	lyrics := v.Lyrics
	if lyrics == "" {
		lyrics = "暂无歌词"
	}
	elements = append(elements, columns(
		column([]any{markdown(escapeMarkdown(lyrics), "center", "notation")}, 1, "vertical", "left"),
		column([]any{cover(v.ImageKey, false, true)}, 1, "vertical", "left"),
	))
	if v.Notice != "" {
		elements = append(elements, note(v.Notice, "left"))
	}
	elements = append(elements, columns(autoColumn(r.management(v.Refresh, v.Withdraw), "horizontal")), updated(v.UpdatedAt))
	c := baseCard(v.Title, v.Subtitle, elements)
	c["config"].(map[string]any)["enable_forward"] = false
	return r.marshal(c)
}

// RenderLyrics never presents a clipped excerpt as the full lyrics. Oversized
// text needs an explicit download URL; otherwise ErrCardTooLarge is returned.
func RenderLyrics(v Lyrics) (string, error) {
	if !validOptionalURL(v.DownloadURL) {
		return "", errors.New("invalid lyrics download URL")
	}
	raw, err := renderLyrics(v, false)
	if !errors.Is(err, ErrCardTooLarge) || v.DownloadURL == "" {
		return raw, err
	}
	return renderLyrics(v, true)
}

func renderLyrics(v Lyrics, downloadOnly bool) (string, error) {
	r := &renderer{}
	elements := make([]any, 0, 6)
	if v.ImageKey != "" {
		elements = append(elements, cover(v.ImageKey, false, false))
	}
	if downloadOnly {
		elements = append(elements, note("完整歌词超出卡片容量，请下载完整歌词查看全部内容。", "left"))
	} else {
		text := v.Text
		if text == "" {
			text = "暂无歌词"
		}
		lines := strings.Split(text, "\n")
		mid := (len(lines) + 1) / 2
		elements = append(elements, columns(
			column([]any{markdown(escapeMarkdown(strings.Join(lines[:mid], "\n")), "center", "notation")}, 1, "vertical", "left"),
			column([]any{markdown(escapeMarkdown(strings.Join(lines[mid:], "\n")), "center", "notation")}, 1, "vertical", "left"),
		))
	}
	if v.DownloadURL != "" {
		elements = append(elements, columns(autoColumn([]any{r.button(Action{Text: "下载完整歌词", URL: v.DownloadURL}, "primary", "medium", "download_outlined")}, "vertical")))
	}
	elements = append(elements, columns(autoColumn(r.management(Action{}, v.Withdraw), "horizontal")), updated(v.UpdatedAt))
	return r.marshal(baseCard(v.Title, v.Subtitle, elements))
}

type renderer struct{ err error }

func (r *renderer) listItem(v Item, index int) map[string]any {
	comment, date := v.Comment, v.CommentTime
	if comment == "" {
		comment = "暂无热评"
		if v.CommentLoading {
			comment = "热评加载中"
		}
	}
	if date == "" && v.CommentLoading {
		date = "加载中"
	}
	commentNode := note(comment, "left")
	commentNode["text"].(map[string]any)["lines"] = 3
	commentNode["icon"] = map[string]any{"tag": "standard_icon", "token": "pen_outlined", "color": "light_grey"}
	a := v.Action
	if a.Text == "" {
		a.Text = "点击播放"
	}
	metadata := columns(
		column([]any{markdown("**"+escapeMarkdown(v.Title)+"**\n**"+escapeMarkdown(v.Artist)+"**", "left", "normal_v2"), r.button(a, "primary_filled", "small", "link-copy_outlined")}, 3, "vertical", "left"),
		column([]any{commentNode, note("ID:"+v.ID, "left"), note(date, "right")}, 2, "vertical", "left"),
	)
	row := columns(autoColumn([]any{cover(v.ImageKey, v.ImageLoading, false)}, "vertical"), column([]any{metadata}, 1, "vertical", "left"))
	row["background_style"], row["flex_mode"], row["element_id"] = "grey-100", "stretch", fmt.Sprintf("music_row_%d", index+1)
	return row
}

func (r *renderer) management(refresh, withdraw Action) []any {
	var out []any
	if actionPresent(refresh) {
		if refresh.Text == "" {
			refresh.Text = "刷新URL"
		}
		if refresh.ConfirmTitle == "" {
			refresh.ConfirmTitle = "刷新音源"
		}
		if refresh.ConfirmText == "" {
			refresh.ConfirmText = "确认要重新获取音源和媒体资源吗？"
		}
		out = append(out, r.button(refresh, "primary_filled", "medium", "refresh_outlined"))
	}
	if actionPresent(withdraw) {
		if withdraw.Text == "" {
			withdraw.Text = "撤回卡片"
		}
		if withdraw.ConfirmTitle == "" {
			withdraw.ConfirmTitle = "撤回本条消息"
		}
		if withdraw.ConfirmText == "" {
			withdraw.ConfirmText = "你确定要撤回这条消息吗？"
		}
		out = append(out, r.button(withdraw, "danger_filled", "medium", "warning_outlined"))
	}
	return out
}

func (r *renderer) button(a Action, style, size, icon string) map[string]any {
	b := map[string]any{"tag": "button", "text": plain(a.Text), "type": style, "width": "default", "size": size, "icon": map[string]any{"tag": "standard_icon", "token": icon}, "margin": "0px 0px 0px 0px"}
	if !validOptionalURL(a.URL) || (a.URL != "" && len(a.Value) > 0) {
		r.err = errors.New("invalid music card action")
	}
	if a.Disabled || (a.URL == "" && len(a.Value) == 0) {
		b["disabled"] = true
	} else if a.URL != "" {
		b["behaviors"] = []any{map[string]any{"type": "open_url", "default_url": a.URL, "pc_url": "", "ios_url": "", "android_url": ""}}
	} else {
		b["behaviors"] = []any{map[string]any{"type": "callback", "value": a.Value}}
	}
	if a.ConfirmTitle != "" || a.ConfirmText != "" {
		b["confirm"] = map[string]any{"title": plain(a.ConfirmTitle), "text": plain(a.ConfirmText)}
	}
	return b
}

func (r *renderer) marshal(v any) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode music card: %w", err)
	}
	if len(b) > MaxCardBytes {
		return "", fmt.Errorf("%w: %d bytes", ErrCardTooLarge, len(b))
	}
	return string(b), nil
}

func actionPresent(a Action) bool {
	return a.Text != "" || a.URL != "" || len(a.Value) > 0 || a.Disabled
}

func validOptionalURL(raw string) bool {
	if raw == "" {
		return true
	}
	if strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Opaque == ""
}

func plain(s string) map[string]any { return map[string]any{"tag": "plain_text", "content": s} }

func markdown(s, align, size string) map[string]any {
	return map[string]any{"tag": "markdown", "content": s, "text_align": align, "text_size": size, "margin": "0px 0px 0px 0px"}
}

func note(s, align string) map[string]any {
	t := plain(s)
	t["text_size"], t["text_align"], t["text_color"] = "notation", align, "grey"
	return map[string]any{"tag": "div", "text": t, "margin": "0px 0px 0px 0px"}
}

func updated(at string) map[string]any {
	if at == "" {
		return note("卡片更新时间：未提供", "right")
	}
	return note("卡片更新时间："+at, "right")
}

func cover(key string, loading, rounded bool) map[string]any {
	if strings.TrimSpace(key) == "" {
		if loading {
			return note("封面加载中", "center")
		}
		return note("封面暂不可用", "center")
	}
	v := map[string]any{"tag": "img", "img_key": key, "alt": plain("专辑封面"), "preview": true, "transparent": true, "scale_type": "crop_center", "size": "medium", "margin": "0px 0px 0px 0px"}
	if rounded {
		v["scale_type"], v["corner_radius"] = "fit_horizontal", "70%"
		delete(v, "size")
	}
	return v
}

func columns(cols ...any) map[string]any {
	return map[string]any{"tag": "column_set", "horizontal_spacing": "8px", "horizontal_align": "left", "columns": cols, "margin": "0px 0px 0px 0px"}
}

func column(elements []any, weight int, direction, align string) map[string]any {
	if elements == nil {
		elements = []any{}
	}
	return map[string]any{"tag": "column", "width": "weighted", "weight": weight, "direction": direction, "elements": elements, "vertical_spacing": "8px", "horizontal_spacing": "8px", "horizontal_align": align, "vertical_align": "top", "padding": "0px 0px 0px 0px"}
}

func autoColumn(elements []any, direction string) map[string]any {
	v := column(elements, 1, direction, "left")
	v["width"] = "auto"
	delete(v, "weight")
	return v
}

func baseCard(title, subtitle string, elements []any) map[string]any {
	return map[string]any{
		"schema": "2.0", "config": map[string]any{"update_multi": true},
		"header": map[string]any{"title": plain(title), "subtitle": plain(subtitle), "template": "blue", "padding": "12px 12px 12px 12px"},
		"body":   map[string]any{"direction": "vertical", "horizontal_spacing": "8px", "vertical_spacing": "8px", "horizontal_align": "left", "vertical_align": "top", "padding": "12px 12px 12px 12px", "elements": elements},
	}
}

var markdownEscaper = strings.NewReplacer("\\", "\\\\", "&", "&amp;", "<", "&lt;", ">", "&gt;", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "`", "\\`", "~", "\\~")

func escapeMarkdown(text string) string { return markdownEscaper.Replace(text) }
