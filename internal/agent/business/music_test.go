package business

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestMusicSearchIsReadOnlyAndPreservesStableIDs(t *testing.T) {
	calls := []string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("keywords") != "夜曲" || r.Form.Get("type") != "1" || r.Header.Get("Cookie") != "server-cookie" {
			t.Error("search wire contract lost")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":200,"result":{"songs":[{"id":123,"name":"夜曲","ar":[{"name":"artist"}],"al":{"picUrl":"https://img.example/1"}}],"songCount":1}}`))
	}))
	defer s.Close()
	c, err := NewMusicClient(MusicConfig{BaseURL: s.URL, Cookie: "server-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Search(context.Background(), MusicSearch{Kind: "song", Keywords: "夜曲", Limit: 5})
	if err != nil || len(out.Items) != 1 || out.Items[0].ID != 123 || out.Items[0].Name != "夜曲" {
		t.Fatalf("search: %+v %v", out, err)
	}
	if !reflect.DeepEqual(calls, []string{"/cloudsearch"}) {
		t.Fatalf("search caused extra effects: %v", calls)
	}
}

func TestPlaylistReadsOnlySelectedPageAndReturnsFullTrackCount(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.URL.Path == "/playlist/detail" {
			w.Write([]byte(`{"code":200,"playlist":{"id":9,"name":"list","trackIds":[{"id":1},{"id":2},{"id":3}]}}`))
			return
		}
		if r.URL.Path != "/song/detail" || r.Form.Get("ids") != "2" {
			t.Errorf("unbounded playlist hydration: %s %v", r.URL.Path, r.Form)
		}
		w.Write([]byte(`{"code":200,"songs":[{"id":2,"name":"selected"}]}`))
	}))
	defer s.Close()
	c, _ := NewMusicClient(MusicConfig{BaseURL: s.URL})
	out, err := c.Detail(context.Background(), "playlist", 9, 1, 1)
	if err != nil || out.Total != 3 || len(out.Tracks) != 1 || out.Tracks[0].ID != 2 {
		t.Fatalf("page: %+v %v", out, err)
	}
}

func TestMusicRejectsProviderErrorsAndCredentialRedirects(t *testing.T) {
	leaked := false
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.Write([]byte(`{}`)) }))
	defer dst.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dst.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	c, _ := NewMusicClient(MusicConfig{BaseURL: s.URL, Cookie: "private-cookie", HTTPClient: s.Client()})
	_, err := c.Search(context.Background(), MusicSearch{Kind: "song", Keywords: "q", Limit: 1})
	if err == nil || leaked {
		t.Fatal("redirect or credential forwarding accepted")
	}
	if b, _ := json.Marshal(err.Error()); string(b) == `"private-cookie"` {
		t.Fatal("credential leaked")
	}
}

func TestMusicParitySelectedMetadataLyricsCommentAndUnblock(t *testing.T) {
	var stamps []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.URL.Path {
		case "/song/detail":
			if r.Form.Get("ids") != "42" {
				t.Errorf("wrong selected id: %v", r.Form)
			}
			w.Write([]byte(`{"code":200,"songs":[{"id":7,"name":"wrong"},{"id":42,"name":"chosen","dt":1000,"ar":[{"name":"artist"}],"al":{"id":8,"name":"album","picUrl":"https://img.example/42"}}]}`))
		case "/lyric":
			if r.Form.Get("id") != "42" {
				t.Error("lyrics lost selected ID")
			}
			w.Write([]byte(`{"code":200,"lrc":{"lyric":"[00:01.00]first\n[00:02.00]second"},"tlyric":{"lyric":"[00:01.00]第一\n[00:02.00]第二"}}`))
		case "/comment/new/":
			if r.Form.Get("id") != "8" || r.Form.Get("type") != "3" || r.Form.Get("pageSize") != "1" {
				t.Errorf("comment contract %v", r.Form)
			}
			w.Write([]byte(`{"code":200,"data":{"comments":[{"content":"a real comment","time":1001,"user":{"nickname":"reader"}}]}}`))
		case "/song/url/v1":
			if r.Form.Get("id") != "42" || r.Form.Get("unblock") != "true" || r.Form.Get("timestamp") == "" {
				t.Errorf("playback parity %v", r.Form)
			}
			stamps = append(stamps, r.Form.Get("timestamp"))
			w.Write([]byte(`{"code":200,"data":[{"id":7,"url":"https://wrong.example/7"},{"id":42,"url":"https://cdn.kuwo.cn/42.flac"}]}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer s.Close()
	c, _ := NewMusicClient(MusicConfig{BaseURL: s.URL})
	d, e := c.Detail(context.Background(), "song", 42, 1, 0)
	if e != nil || len(d.Tracks) != 1 || d.Tracks[0].AlbumID != 8 || d.Tracks[0].Album != "album" || d.Tracks[0].DurationMS != 1000 {
		t.Fatalf("metadata %+v %v", d, e)
	}
	lyrics, e := c.Lyrics(context.Background(), 42)
	if e != nil || !strings.Contains(lyrics.Full, "first\n第一") || !strings.Contains(lyrics.Original, "[00:02.00]second") {
		t.Fatalf("lyrics %+v %v", lyrics, e)
	}
	comment, e := c.Comment(context.Background(), "album", 8)
	if e != nil || comment.Content != "a real comment" || comment.TimeMS != 1001 || comment.Author != "reader" {
		t.Fatalf("comment %+v %v", comment, e)
	}
	for _, refresh := range []bool{false, true} {
		u, e := c.PlaybackURL(context.Background(), 42, PlaybackOptions{Refresh: refresh})
		if e != nil || u != "https://cdn.kuwo.cn/42.flac" {
			t.Fatalf("playback %s %v", u, e)
		}
	}
	if len(stamps) != 2 || stamps[0] == stamps[1] {
		t.Fatalf("refresh reused source timestamp %v", stamps)
	}
}

func TestMusicProviderErrorsAreTypedAndDoNotExposeSecrets(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":403,"message":"secret-cookie"}`))
	}))
	defer s.Close()
	c, _ := NewMusicClient(MusicConfig{BaseURL: s.URL, Cookie: "secret-cookie"})
	_, e := c.Lyrics(context.Background(), 42)
	var provider *MusicProviderError
	if !errors.As(e, &provider) || !errors.Is(e, ErrProvider) || provider.Code != 403 || strings.Contains(e.Error(), "secret-cookie") {
		t.Fatalf("untyped or leaking provider failure %T %v", e, e)
	}
}
