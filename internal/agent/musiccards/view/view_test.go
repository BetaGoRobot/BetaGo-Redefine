package view

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixture[T any](t *testing.T, name string) T {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func card(t *testing.T, raw string, err error) map[string]any {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxCardBytes {
		t.Fatalf("card is %d bytes", len(raw))
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	if v["schema"] != "2.0" {
		t.Fatalf("schema = %v, want 2.0", v["schema"])
	}
	if object(t, v["config"])["update_multi"] != true {
		t.Fatal("card cannot update same message")
	}
	return v
}

func object(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want object, got %T", v)
	}
	return m
}
func array(t *testing.T, v any) []any {
	t.Helper()
	a, ok := v.([]any)
	if !ok {
		t.Fatalf("want array, got %T", v)
	}
	return a
}
func elements(t *testing.T, v map[string]any) []any {
	t.Helper()
	return array(t, object(t, v["body"])["elements"])
}

func nodes(v any, tag string) []map[string]any {
	var out []map[string]any
	switch x := v.(type) {
	case map[string]any:
		if x["tag"] == tag {
			out = append(out, x)
		}
		for _, child := range x {
			out = append(out, nodes(child, tag)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, nodes(child, tag)...)
		}
	}
	return out
}
func content(v any) string {
	var s []string
	switch x := v.(type) {
	case map[string]any:
		if text, ok := x["content"].(string); ok {
			s = append(s, text)
		}
		for k, child := range x {
			if k != "content" {
				s = append(s, content(child))
			}
		}
	case []any:
		for _, child := range x {
			s = append(s, content(child))
		}
	}
	return strings.Join(s, "\n")
}
func button(t *testing.T, v any, text string) map[string]any {
	t.Helper()
	for _, b := range nodes(v, "button") {
		if object(t, b["text"])["content"] == text {
			return b
		}
	}
	t.Fatalf("missing button %q", text)
	return nil
}
func callback(t *testing.T, b map[string]any) map[string]any {
	t.Helper()
	behaviors := array(t, b["behaviors"])
	if len(behaviors) != 1 {
		t.Fatal("expected exactly one behavior")
	}
	v := object(t, behaviors[0])
	if v["type"] != "callback" {
		t.Fatalf("behavior = %v", v)
	}
	return object(t, v["value"])
}

func TestListFixturesPreserveRichRowsAndCallerActions(t *testing.T) {
	for _, name := range []string{"song_search", "album_search", "playlist_detail", "album_detail"} {
		t.Run(name, func(t *testing.T) {
			v := fixture[List](t, name)
			raw, err := RenderList(v)
			c := card(t, raw, err)
			rows := elements(t, c)
			for i, item := range v.Items {
				row := object(t, rows[i])
				if row["tag"] != "column_set" || row["background_style"] != "grey-100" {
					t.Fatalf("row hierarchy = %v", row)
				}
				cols := array(t, row["columns"])
				if len(cols) != 2 {
					t.Fatal("row needs cover and content columns")
				}
				imgs := nodes(cols[0], "img")
				if len(imgs) != 1 || imgs[0]["img_key"] != item.ImageKey {
					t.Fatalf("cover = %v", imgs)
				}
				inner := nodes(cols[1], "column_set")
				if len(inner) != 1 {
					t.Fatalf("missing title and hot-comment columns: %v", inner)
				}
				parts := array(t, inner[0]["columns"])
				if len(parts) != 2 {
					t.Fatal("metadata columns missing")
				}
				if object(t, parts[0])["weight"] != float64(3) || object(t, parts[1])["weight"] != float64(2) {
					t.Fatal("lost metadata proportions")
				}
				expectedTitle := item.Title
				if name == "album_search" {
					expectedTitle = `\[专辑\] 魔杰座`
				}
				if !strings.Contains(content(parts[0]), expectedTitle) || !strings.Contains(content(parts[0]), item.Artist) {
					t.Fatal("title/artist missing")
				}
				for _, s := range []string{item.Comment, item.CommentTime, "ID:" + item.ID} {
					if !strings.Contains(content(parts[1]), s) {
						t.Fatalf("missing comment metadata %q", s)
					}
				}
				bs := nodes(row, "button")
				if len(bs) != 1 {
					t.Fatalf("want shipped single row action; got %d", len(bs))
				}
				if got := callback(t, bs[0]); !reflect.DeepEqual(got, item.Action.Value) {
					t.Fatalf("callback = %v, want caller value %v", got, item.Action.Value)
				}
			}
			if !strings.Contains(content(c), "第 ") || !strings.Contains(content(c), "共 ") || !strings.Contains(content(c), v.UpdatedAt) {
				t.Fatal("paging/total/update time missing")
			}
			if strings.Contains(raw, "Trace") || strings.Contains(raw, "task_id") || strings.Contains(raw, "jaeger") {
				t.Fatal("internal metadata leaked")
			}
		})
	}
}

func TestListFiveRowsBoundariesAndIndependentPlaceholders(t *testing.T) {
	v := fixture[List](t, "song_search")
	v.Items = append(v.Items, v.Items[0], v.Items[0], v.Items[0], v.Items[0])
	v.Items[1].ImageKey = ""
	v.Items[1].ImageLoading = true
	v.Items[1].Comment = ""
	v.Items[1].CommentLoading = true
	raw, err := RenderList(v)
	c := card(t, raw, err)
	rows := elements(t, c)
	if len(v.Items) != 5 || len(nodes(rows[1], "img")) != 0 {
		t.Fatal("invalid placeholder image")
	}
	if !strings.Contains(content(rows[1]), "封面加载中") || !strings.Contains(content(rows[1]), "热评加载中") {
		t.Fatal("metadata-first placeholders missing")
	}
	if button(t, c, "上一页")["disabled"] != true || button(t, c, "下一页")["disabled"] == true {
		t.Fatal("first-page controls wrong")
	}
	if got := callback(t, button(t, c, "下一页")); !reflect.DeepEqual(got, v.Next.Value) {
		t.Fatalf("page payload altered: %v", got)
	}
	v.Page = v.TotalPages
	raw, err = RenderList(v)
	c = card(t, raw, err)
	if button(t, c, "下一页")["disabled"] != true || button(t, c, "上一页")["disabled"] == true {
		t.Fatal("last-page controls wrong")
	}
	v.Items = append(v.Items, v.Items[0])
	if _, err := RenderList(v); err == nil {
		t.Fatal("sixth row silently omitted")
	}
	for _, page := range []int{-1, v.TotalPages + 1} {
		v.Items = v.Items[:5]
		v.Page = page
		if _, err := RenderList(v); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
}

func TestListEscapesTitleAndUsesLiteralCommentText(t *testing.T) {
	v := fixture[List](t, "song_search")
	v.Items[0].Title = "*evil* [link](https://evil.test) <at id=all>"
	v.Items[0].Comment = "[link](https://evil.test) <at id=all>"
	v.Items[0].ImageKey = ""
	v.Items[0].CommentLoading = false
	raw, err := RenderList(v)
	c := card(t, raw, err)
	row := elements(t, c)[0]
	if !strings.Contains(content(row), `\*evil\* \[link\]`) || !strings.Contains(content(row), "&lt;at id=all&gt;") {
		t.Fatal("title can inject markdown or Lark tags")
	}
	for _, n := range nodes(row, "plain_text") {
		if n["content"] == v.Items[0].Comment {
			return
		}
	}
	t.Fatal("comment not rendered as literal plain text")
}

func TestDetailTwoColumnsHandlesAndNativeAudioLifecycle(t *testing.T) {
	for _, name := range []string{"detail_pending", "detail_audio"} {
		t.Run(name, func(t *testing.T) {
			v := fixture[Detail](t, name)
			raw, err := RenderDetail(v)
			c := card(t, raw, err)
			if object(t, c["config"])["enable_forward"] != false {
				t.Fatal("native audio forward compatibility missing")
			}
			var pair map[string]any
			for _, n := range nodes(c, "column_set") {
				cols := array(t, n["columns"])
				if len(cols) == 2 && object(t, cols[0])["weight"] == float64(1) && object(t, cols[1])["weight"] == float64(1) {
					pair = n
					break
				}
			}
			if pair == nil {
				t.Fatal("equal two-column detail missing")
			}
			cols := array(t, pair["columns"])
			lyrics := nodes(cols[0], "markdown")
			if len(lyrics) != 1 || lyrics[0]["text_align"] != "center" || lyrics[0]["content"] != v.Lyrics {
				t.Fatalf("lyric preview = %v", lyrics)
			}
			imgs := nodes(cols[1], "img")
			if len(imgs) != 1 || imgs[0]["img_key"] != v.ImageKey || imgs[0]["corner_radius"] != "70%" {
				t.Fatalf("cover = %v", imgs)
			}
			play := object(t, array(t, button(t, c, "Play")["behaviors"])[0])
			if play["type"] != "open_url" || play["default_url"] != v.PlayerURL {
				t.Fatalf("player link = %v", play)
			}
			if !reflect.DeepEqual(callback(t, button(t, c, "完整歌词")), v.FullLyricsAction.Value) {
				t.Fatal("full lyrics callback changed")
			}
			for _, text := range []string{"刷新URL", "撤回卡片"} {
				if button(t, c, text)["confirm"] == nil {
					t.Fatalf("%s lacks confirmation", text)
				}
			}
			audios := nodes(c, "audio")
			if v.AudioFileKey == "" {
				if len(audios) != 0 {
					t.Fatal("empty native audio handle rendered")
				}
			} else {
				if len(audios) != 1 {
					t.Fatal("native audio missing")
				}
				a := audios[0]
				for k, want := range map[string]any{"file_key": v.AudioFileKey, "audio_id": v.AudioID, "show_progress_bar": true, "show_time": true, "time_display": "both"} {
					if a[k] != want {
						t.Fatalf("audio %s = %v", k, a[k])
					}
				}
				if len(nodes(a, "fallback_text")) != 1 || !strings.Contains(content(a), "升级") {
					t.Fatal("native audio client fallback missing")
				}
				if !strings.Contains(content(c), "03:40") {
					t.Fatal("duration missing")
				}
			}
		})
	}
}

func TestDetailMissingMediaKeepsUsableSourceFallback(t *testing.T) {
	v := fixture[Detail](t, "detail_pending")
	v.ImageKey = ""
	v.PlayerURL = ""
	raw, err := RenderDetail(v)
	c := card(t, raw, err)
	if len(nodes(c, "img")) != 0 || !strings.Contains(content(c), "封面暂不可用") {
		t.Fatal("missing cover is not readable")
	}
	if object(t, array(t, button(t, c, "Play")["behaviors"])[0])["default_url"] != v.SourceURL {
		t.Fatal("source fallback missing")
	}
	v.AudioFileKey = "file_real"
	v.AudioID = ""
	raw, err = RenderDetail(v)
	c = card(t, raw, err)
	if nodes(c, "audio")[0]["audio_id"] == "" {
		t.Fatal("default audio ID missing")
	}
}

func TestWhitespaceAudioHandleIsUnavailable(t *testing.T) {
	v := fixture[Detail](t, "detail_pending")
	v.AudioFileKey = " \t\n"
	raw, err := RenderDetail(v)
	c := card(t, raw, err)
	if len(nodes(c, "audio")) != 0 {
		t.Fatal("blank uploaded file handle became an invalid audio element")
	}
}

func TestDetailOptionalAlbumActionPreservesSignedCallback(t *testing.T) {
	v := fixture[Detail](t, "detail_pending")
	raw, err := RenderDetail(v)
	c := card(t, raw, err)
	before := len(nodes(c, "button"))
	v.AlbumAction = Action{Value: map[string]any{"kind": "music", "token": "signed-album-99"}}
	raw, err = RenderDetail(v)
	c = card(t, raw, err)
	if len(nodes(c, "button")) != before+1 {
		t.Fatal("album entry absent from detail")
	}
	if !reflect.DeepEqual(callback(t, button(t, c, "查看专辑")), v.AlbumAction.Value) {
		t.Fatal("album callback changed")
	}
}

func TestLyricsAndDetailEscapeUntrustedContent(t *testing.T) {
	text := "<at id=all> [open](https://evil.test) *bold*"
	for name, render := range map[string]func() (string, error){
		"detail": func() (string, error) {
			v := fixture[Detail](t, "detail_pending")
			v.Lyrics = text
			return RenderDetail(v)
		},
		"lyrics": func() (string, error) { v := fixture[Lyrics](t, "lyrics"); v.Text = text; return RenderLyrics(v) },
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := render()
			c := card(t, raw, err)
			got := content(c)
			if strings.Contains(got, "<at id=all>") || strings.Contains(got, "[open](") || strings.Contains(got, " *bold*") {
				t.Fatal("untrusted lyric markup can execute")
			}
			if !strings.Contains(got, `&lt;at id=all&gt; \[open\]`) {
				t.Fatal("escaped lyrics missing")
			}
		})
	}
}

func TestEmptyListAndUnavailableCommentRemainReadable(t *testing.T) {
	raw, err := RenderList(List{Title: "空结果"})
	c := card(t, raw, err)
	if !strings.Contains(content(c), "暂无音乐结果") || !strings.Contains(content(c), "第 1 / 1 页") {
		t.Fatal("empty state missing")
	}
	if button(t, c, "上一页")["disabled"] != true || button(t, c, "下一页")["disabled"] != true {
		t.Fatal("empty result navigation enabled")
	}
	v := fixture[List](t, "song_search")
	v.Items[0].Comment = ""
	v.Items[0].ImageKey = ""
	raw, err = RenderList(v)
	c = card(t, raw, err)
	if !strings.Contains(content(c), "暂无热评") || !strings.Contains(content(c), "封面暂不可用") {
		t.Fatal("failed media state not readable")
	}
}

func TestLyricsTwoColumnsPreserveEveryLine(t *testing.T) {
	v := fixture[Lyrics](t, "lyrics")
	raw, err := RenderLyrics(v)
	c := card(t, raw, err)
	var columns []any
	for _, n := range nodes(c, "column_set") {
		a := array(t, n["columns"])
		if len(a) == 2 && object(t, a[0])["weight"] == float64(1) && object(t, a[1])["weight"] == float64(1) {
			columns = a
			break
		}
	}
	if len(columns) != 2 {
		t.Fatal("two lyric columns missing")
	}
	left, right := nodes(columns[0], "markdown"), nodes(columns[1], "markdown")
	if len(left) != 1 || len(right) != 1 || left[0]["content"].(string)+"\n"+right[0]["content"].(string) != v.Text {
		t.Fatal("full lyrics lost or reordered")
	}
	if nodes(c, "img")[0]["img_key"] != v.ImageKey {
		t.Fatal("lyrics cover handle missing")
	}
}

func TestOversizedLyricsProvideExplicitDownloadOrError(t *testing.T) {
	v := fixture[Lyrics](t, "lyrics")
	v.Text = strings.Repeat("很长的歌词 <at id=all>\n", 5000)
	v.DownloadURL = ""
	if raw, err := RenderLyrics(v); !errors.Is(err, ErrCardTooLarge) || raw != "" {
		t.Fatal("oversized claimed-full lyrics silently truncated")
	}
	v.DownloadURL = "https://assets.example.test/full.lrc?signature=opaque"
	raw, err := RenderLyrics(v)
	c := card(t, raw, err)
	if !strings.Contains(content(c), "完整歌词超出卡片容量") {
		t.Fatal("long lyric fallback not explained")
	}
	if object(t, array(t, button(t, c, "下载完整歌词")["behaviors"])[0])["default_url"] != v.DownloadURL {
		t.Fatal("full download missing")
	}
}

func TestUnsafeURLsAndOversizedCardsFailClosed(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,evil", "//evil.test/path", "https://user:pass@evil.test/path", "https://", "https://example.test/\npath"} {
		t.Run(bad, func(t *testing.T) {
			d := fixture[Detail](t, "detail_pending")
			d.PlayerURL = bad
			if _, err := RenderDetail(d); err == nil {
				t.Fatal("unsafe player URL accepted")
			}
			l := fixture[Lyrics](t, "lyrics")
			l.DownloadURL = bad
			if _, err := RenderLyrics(l); err == nil {
				t.Fatal("unsafe download URL accepted")
			}
			v := fixture[List](t, "song_search")
			v.Items[0].Action = Action{Text: "Open", URL: bad}
			if _, err := RenderList(v); err == nil {
				t.Fatal("unsafe action URL accepted")
			}
		})
	}
	v := fixture[List](t, "song_search")
	v.Items[0].Title = strings.Repeat("大", MaxCardBytes)
	if raw, err := RenderList(v); !errors.Is(err, ErrCardTooLarge) || raw != "" {
		t.Fatal("oversized list silently truncated")
	}
	d := fixture[Detail](t, "detail_pending")
	d.Lyrics = strings.Repeat("大", MaxCardBytes)
	if raw, err := RenderDetail(d); !errors.Is(err, ErrCardTooLarge) || raw != "" {
		t.Fatal("oversized detail silently truncated")
	}
	v = fixture[List](t, "song_search")
	v.Items[0].Action.URL = "https://example.test"
	if _, err := RenderList(v); err == nil {
		t.Fatal("ambiguous callback and URL accepted")
	}
}
