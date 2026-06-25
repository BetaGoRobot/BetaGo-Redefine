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
	Address  string
	// Longitude/Latitude 来自门店搜索结果本身，仅内部用于下单，不向用户/模型暴露。
	Longitude float64
	Latitude  float64
}

type CheckoutMode string

const (
	CheckoutModeInitiatorUnified CheckoutMode = "initiator_unified"
	CheckoutModeSelfService      CheckoutMode = "self_service"
)

func NormalizeCheckoutMode(mode string) CheckoutMode {
	switch CheckoutMode(strings.TrimSpace(mode)) {
	case CheckoutModeSelfService:
		return CheckoutModeSelfService
	default:
		return CheckoutModeInitiatorUnified
	}
}

// ProductSelection 记录用户已选商品及当前规格，用于规格切换与下单。
type ProductSelection struct {
	ProductID   int64
	SkuCode     string
	ProductName string
}

// SessionKey 一次点单流程的范围。同一群里多人同时发起，每张卡片各自独立。
// 卡片消息 ID 贯穿"开始点单 → 选店 → 加购 → 结算 → 确认/取消"全过程（PatchCardJSON 同卡），
// 因此用 MessageID 作为隔离键最稳。
type SessionKey struct {
	Provider  string
	AppID     string
	BotOpenID string
	MessageID string
}

func (k SessionKey) String() string {
	return strings.Join([]string{
		"luckin",
		k.Provider,
		k.AppID,
		k.BotOpenID,
		k.MessageID,
	}, "|")
}

// Valid 报告 key 是否携带 messageID；缺失时 store/lock 必须直接拒绝，避免误用群级 key。
func (k SessionKey) Valid() bool {
	return strings.TrimSpace(k.MessageID) != ""
}

func NewSessionKey(req CredentialRequest, messageID string) SessionKey {
	return SessionKey{
		Provider:  ProviderLuckin,
		AppID:     req.AppID,
		BotOpenID: req.BotOpenID,
		MessageID: strings.TrimSpace(messageID),
	}
}

// UserHistoryKey 用户级偏好（最近门店、是否曾开始过点单）的隔离键。
// 与 SessionKey 解耦：换一张新卡片不应让"最近门店"消失，因此用 (chat, user) 维度。
type UserHistoryKey struct {
	Provider  string
	AppID     string
	BotOpenID string
	ChatID    string
	OpenID    string
}

func (k UserHistoryKey) String() string {
	return strings.Join([]string{
		"luckin-user",
		k.Provider,
		k.AppID,
		k.BotOpenID,
		k.ChatID,
		k.OpenID,
	}, "|")
}

func NewUserHistoryKey(req CredentialRequest) UserHistoryKey {
	return UserHistoryKey{
		Provider:  ProviderLuckin,
		AppID:     req.AppID,
		BotOpenID: req.BotOpenID,
		ChatID:    req.ChatID,
		OpenID:    req.OpenID,
	}
}

// OrderSession 一次点单流程的完整状态：发起人 + 门店 + 购物车。
// 发起人在卡片首发时即锁定，贯穿全流程；非发起人只能加购自己的商品行。
type OrderSession struct {
	InitiatorOpenID string
	ChatID          string
	Shop            ShopSelection
	Cart            Cart
	CheckoutMode    CheckoutMode
}

// SessionStore 由 mcpstore 实现：进程内 ttlcache + Redis 持久化。
// 接口拆两组：Order* 按 SessionKey（卡片维度），History* 按 UserHistoryKey（用户维度）。
type SessionStore interface {
	GetSession(context.Context, SessionKey) (OrderSession, bool)
	SetSession(context.Context, SessionKey, OrderSession)
	DeleteSession(context.Context, SessionKey)

	// GetRecentShops 返回该用户最近选过的门店（跨卡片）。
	GetRecentShops(context.Context, UserHistoryKey, int) []ShopSelection
	// AddRecentShop 把一次成功选店记录到用户偏好。
	AddRecentShop(context.Context, UserHistoryKey, ShopSelection)
	// Seen 报告该用户此前是否在该机器人下点过单（决定"会话过期"还是"从未开始"文案）。
	Seen(context.Context, UserHistoryKey) bool
	// MarkSeen 标记该用户已经开始过点单。
	MarkSeen(context.Context, UserHistoryKey)
}
