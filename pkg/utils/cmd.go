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
	u.Host = config.Get().NeteaseMusicConfig.BaseURL
	u.Scheme = "https"
	q := u.Query()
	q.Add("target", strings.TrimPrefix(jsonURL, "https://kutt.kmhomelab.cn/"))
	u.RawQuery = q.Encode()
	return u.String()
}
