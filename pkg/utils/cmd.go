package utils

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

func RemoveArgFromStr(s string, args ...string) string {
	re := fmt.Sprintf(`\s+--(%s)=(?:\"[^\"]*\"|[^\s]+)`, strings.Join(args, "|"))
	re = regexp.MustCompile(re).ReplaceAllString(s, "")
	return strings.Join(strings.Fields(re), " ")
}

func BuildURL(jsonURL string) string {
	u := &url.URL{}
	u.Host = config.Get().NeteaseMusicConfig.PlayerURL
	u.Scheme = "https"
	q := u.Query()
	q.Add("target", jsonURL)
	u.RawQuery = q.Encode()
	return u.String()
}

// BuildMusicURL preserves old descriptor links while giving the player a stable identity.
func BuildMusicURL(descriptorURL, trackID string) string {
	u, err := url.Parse(BuildURL(descriptorURL))
	if err != nil {
		return BuildURL(descriptorURL)
	}
	q := u.Query()
	q.Set("trackID", trackID)
	q.Set("source", "netease")
	u.RawQuery = q.Encode()
	u.Path = "/play"
	return u.String()
}
