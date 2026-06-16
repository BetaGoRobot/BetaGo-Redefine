package luckin

import (
	"context"
	"strings"
)

type GeoPoint struct {
	Longitude float64
	Latitude  float64
}

// Geocoder 把用户描述的地点文本转为经纬度（GCJ-02，与瑞幸坐标系一致）。
type Geocoder interface {
	Geocode(context.Context, string) (GeoPoint, error)
}

type ShopSelection struct {
	DeptID   int64
	DeptName string
	// Longitude/Latitude 来自门店搜索结果本身，仅内部用于下单，不向用户/模型暴露。
	Longitude float64
	Latitude  float64
}

type SessionKey struct {
	Provider  string
	AppID     string
	BotOpenID string
	ChatID    string
	OpenID    string
}

type SessionStore interface {
	GetShop(context.Context, SessionKey) (ShopSelection, bool)
	SetShop(context.Context, SessionKey, ShopSelection)
	ClearShop(context.Context, SessionKey)
}

func (k SessionKey) String() string {
	return strings.Join([]string{
		"luckin",
		k.Provider,
		k.AppID,
		k.BotOpenID,
		k.ChatID,
		k.OpenID,
	}, "|")
}

func NewSessionKey(req CredentialRequest) SessionKey {
	return SessionKey{
		Provider:  ProviderLuckin,
		AppID:     req.AppID,
		BotOpenID: req.BotOpenID,
		ChatID:    req.ChatID,
		OpenID:    req.OpenID,
	}
}
