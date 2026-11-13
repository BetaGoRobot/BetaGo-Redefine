package music

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AnonymousNetease never sends user cookies, never logs in and never requests unblock matching.
type AnonymousNetease struct {
	BaseURL        string
	OwnedMediaHost string // Explicitly configured owned proxy/QA host; never infer a third-party source.
	Client         *http.Client
}

func (p AnonymousNetease) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 12 * time.Second}
}
func (p AnonymousNetease) get(ctx context.Context, path string, q url.Values, out any) error {
	if p.BaseURL == "" {
		return unavailable
	}
	q.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(p.BaseURL, "/")+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return unavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &apiError{503, "source_access_denied"}
	}
	if resp.StatusCode != 200 {
		return unavailable
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
		return unavailable
	}
	return nil
}

type netSong struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	DT   int64  `json:"dt"`
	AR   []struct {
		Name string `json:"name"`
	} `json:"ar"`
	AL struct {
		Name   string `json:"name"`
		PicURL string `json:"picUrl"`
	} `json:"al"`
}

func (n netSong) track() Track {
	artists := []string{}
	for _, a := range n.AR {
		artists = append(artists, a.Name)
	}
	id := strconv.FormatInt(n.ID, 10)
	return Track{ID: "netease:" + id, Source: "netease", ExternalID: id, Title: n.Name, Artist: strings.Join(artists, ", "), Album: n.AL.Name, Picture: n.AL.PicURL, DurationMs: n.DT}
}

var numeric = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func (p AnonymousNetease) Search(ctx context.Context, q string) ([]Track, error) {
	var v struct {
		Code   int
		Result struct{ Songs []netSong }
	}
	if err := p.get(ctx, "/cloudsearch", url.Values{"keywords": {q}, "type": {"1"}, "limit": {"30"}}, &v); err != nil {
		return nil, err
	}
	if v.Code != 200 {
		return nil, unavailable
	}
	out := []Track{}
	for _, n := range v.Result.Songs {
		if n.ID > 0 {
			out = append(out, n.track())
		}
	}
	return out, nil
}
func (p AnonymousNetease) Detail(ctx context.Context, id string) (Track, error) {
	ext := strings.TrimPrefix(id, "netease:")
	if !strings.HasPrefix(id, "netease:") || !numeric.MatchString(ext) {
		return Track{}, missing
	}
	var v struct {
		Code  int
		Songs []netSong
	}
	if err := p.get(ctx, "/song/detail", url.Values{"ids": {ext}}, &v); err != nil {
		return Track{}, err
	}
	if v.Code != 200 || len(v.Songs) != 1 {
		return Track{}, missing
	}
	return v.Songs[0].track(), nil
}
func (p AnonymousNetease) Resolve(ctx context.Context, t Track) (Resolution, error) {
	out := Resolution{TrackID: t.ID, Status: "unavailable", Reason: "source_not_supported"}
	if t.Source != "netease" || !numeric.MatchString(t.ExternalID) {
		return out, nil
	}
	var v struct {
		Code int
		Data []struct {
			ID            int64
			URL           string
			Expi          int64
			FreeTrialInfo json.RawMessage
			Code          int
		}
	}
	if err := p.get(ctx, "/song/url/v1", url.Values{"id": {t.ExternalID}, "level": {"standard"}, "unblock": {"false"}}, &v); err != nil {
		if ae, ok := err.(*apiError); ok && ae.Message == "source_access_denied" {
			out.Reason = "provider_access_denied"
			return out, nil
		}
		// Some deployed anonymous adapters cannot decode the v1 upstream response.
		// Compatibility endpoint, same source and recording; never retry a valid restriction response.
		if err = p.get(ctx, "/song/url", url.Values{"id": {t.ExternalID}, "br": {"128000"}, "unblock": {"false"}}, &v); err != nil {
			return out, err
		}
	}
	out.Reason = "anonymous_source_unavailable_or_restricted"
	if v.Code != 200 || len(v.Data) != 1 || strconv.FormatInt(v.Data[0].ID, 10) != t.ExternalID || v.Data[0].URL == "" {
		return out, nil
	}
	d := v.Data[0]
	u, err := url.Parse(d.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return out, nil
	}
	host := strings.ToLower(u.Hostname())
	owned := p.OwnedMediaHost != "" && host == strings.ToLower(p.OwnedMediaHost)
	if !owned && host != "music.126.net" && !strings.HasSuffix(host, ".music.126.net") && host != "music.163.com" && !strings.HasSuffix(host, ".music.163.com") {
		out.Reason = "source_mismatch: anonymous adapter returned an unapproved media host"
		return out, nil
	}
	// Provider URL only, without account fallback or substituting another recording.
	out.Status = "available"
	out.Reason = ""
	out.URL = d.URL
	if len(d.FreeTrialInfo) > 0 && string(d.FreeTrialInfo) != "null" {
		out.Status = "preview"
		out.Reason = "anonymous_preview_only"
	}
	secs := d.Expi
	if secs <= 0 || secs > 300 {
		secs = 300
	}
	exp := time.Now().Add(time.Duration(secs) * time.Second)
	out.ExpiresAt = &exp
	return out, nil
}
func (p AnonymousNetease) Lyrics(ctx context.Context, t Track) (Lyrics, error) {
	out := Lyrics{TrackID: t.ID}
	if t.Source != "netease" || !numeric.MatchString(t.ExternalID) {
		return out, nil
	}
	var v struct {
		Code   int
		LRC    struct{ Lyric string }
		Tlyric struct{ Lyric string }
	}
	if err := p.get(ctx, "/lyric", url.Values{"id": {t.ExternalID}}, &v); err != nil {
		return out, err
	}
	if v.Code != 200 {
		return out, unavailable
	}
	out.LRC = v.LRC.Lyric
	out.TranslatedLRC = v.Tlyric.Lyric
	return out, nil
}

// FeishuVerifier exchanges only a short-lived code and trusts identity exclusively from user_info.
// No provider token is returned to clients or persisted.
type FeishuVerifier struct {
	AppID, AppSecret, RedirectURI string
	Client                        *http.Client
	BaseURL                       string
}

func (v FeishuVerifier) Verify(ctx context.Context, code string) (Identity, error) {
	out := Identity{}
	if code == "" || len(code) > 2048 || v.AppID == "" || v.AppSecret == "" {
		return out, fmt.Errorf("feishu not configured")
	}
	c := v.Client
	if c == nil {
		c = &http.Client{Timeout: 12 * time.Second}
	}
	base := v.BaseURL
	if base == "" {
		base = "https://open.feishu.cn"
	}
	b, _ := json.Marshal(map[string]string{"grant_type": "authorization_code", "code": code, "client_id": v.AppID, "client_secret": v.AppSecret, "redirect_uri": v.RedirectURI})
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/open-apis/authen/v2/oauth/token", bytes.NewReader(b))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	var token struct {
		Code        int
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if resp.StatusCode != 200 {
		return out, fmt.Errorf("feishu exchange rejected")
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil {
		return out, err
	}
	if token.Code != 0 || token.AccessToken == "" || token.Error != "" {
		return out, fmt.Errorf("feishu exchange rejected")
	}
	req, err = http.NewRequestWithContext(ctx, "GET", base+"/open-apis/authen/v1/user_info", nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	info, err := c.Do(req)
	if err != nil {
		return out, err
	}
	defer info.Body.Close()
	var user struct {
		Code int
		Data struct {
			OpenID    string `json:"open_id"`
			TenantKey string `json:"tenant_key"`
		}
	}
	if info.StatusCode != 200 {
		return out, fmt.Errorf("feishu identity rejected")
	}
	if err = json.NewDecoder(io.LimitReader(info.Body, 1<<20)).Decode(&user); err != nil {
		return out, err
	}
	if user.Code != 0 || user.Data.OpenID == "" || user.Data.TenantKey == "" {
		return out, fmt.Errorf("feishu identity missing tenant or subject")
	}
	return Identity{AppID: v.AppID, TenantKey: user.Data.TenantKey, Subject: user.Data.OpenID}, nil
}
