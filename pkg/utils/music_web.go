package utils

import (
	"net/url"
	"os"
	"regexp"
	"strings"
)

// MusicWebURLs keeps the browser entry usable independently of Feishu AppLink.
type MusicWebURLs struct{ BrowserURL, AppLink string }

var musicWebTrackID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}:[A-Za-z0-9._-]{1,180}$`)
var musicWebAppID = regexp.MustCompile(`^cli_[A-Za-z0-9_]{1,100}$`)

// BuildMusicWebLinks only accepts operator configuration and catalogue/search
// identifiers. Identity, sessions and authorization codes never enter these URLs.
func BuildMusicWebLinks(query, trackID string) MusicWebURLs {
	origin, err := url.Parse(strings.TrimSpace(os.Getenv("MUSIC_WEB_ORIGIN")))
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") || origin.Opaque != "" {
		return MusicWebURLs{}
	}
	if trackID != "" && !musicWebTrackID.MatchString(trackID) {
		return MusicWebURLs{}
	}
	origin.Path = "/library"
	q := url.Values{}
	if trackID != "" {
		origin.Path = "/play"
		q.Set("trackID", trackID)
	} else if query = strings.TrimSpace(query); query != "" {
		origin.Path = "/search"
		q.Set("q", query)
	}
	origin.RawQuery = q.Encode()
	links := MusicWebURLs{BrowserURL: origin.String()}
	if appID := strings.TrimSpace(os.Getenv("MUSIC_WEB_APP_ID")); musicWebAppID.MatchString(appID) {
		app := url.URL{Scheme: "https", Host: "applink.feishu.cn", Path: "/client/web_app/open"}
		// Feishu's path parameter cannot carry a query or fragment. The browser
		// URL already contains encoded query values; encode the complete URL once
		// more as lk_target_url, per the H5 AppLink protocol. Request a bounded
		// sidebar beside the conversation; actual placement is client-controlled.
		app.RawQuery = url.Values{"appId": {appID}, "mode": {"sidebar"}, "min_width": {"360"}, "max_width": {"480"}, "lk_target_url": {links.BrowserURL}}.Encode()
		links.AppLink = app.String()
	}
	return links
}
