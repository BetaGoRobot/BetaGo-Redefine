package luckin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type PendingStatus string

const (
	PendingStatusPending   PendingStatus = "pending"
	PendingStatusConfirmed PendingStatus = "confirmed"
	PendingStatusExpired   PendingStatus = "expired"
	PendingStatusCancelled PendingStatus = "cancelled"
	PendingStatusFailed    PendingStatus = "failed"
)

var (
	ErrPendingOrderNotFound       = errors.New("luckin pending order not found")
	ErrPendingOrderNotConfirmable = errors.New("luckin pending order cannot be confirmed")
)

type PendingOrder struct {
	ID                 string
	AppID              string
	BotOpenID          string
	ChatID             string
	// RequesterOpenID 始终是发起人 OpenID。即便结算按钮被发起人之外的人点到（虽然会被
	// 越权拦截），凭证、订单归属仍以发起人为准。
	RequesterOpenID    string
	// InitiatorOpenID 与 RequesterOpenID 在 luckin 场景下相同，单独保留是为了
	// 在落库时显式表达"这单的发起人"，方便后续按发起人查询/分账。
	InitiatorOpenID    string
	CheckoutMode       CheckoutMode
	CredentialScope    CredentialScope
	MCPServerName      string
	CreateOrderPayload json.RawMessage
	PayloadHash        string
	PreviewResult      json.RawMessage
	// CartSnapshot 是 Draft 时的购物车原貌（含 LineID/AddedByOpenID/UnitPrice），
	// 取餐通知卡按这份快照分账，避免后续 cart 被清空导致拿不到分账依据。
	CartSnapshot       []CartItem
	Status             PendingStatus
	ResultJSON         json.RawMessage
	ErrorText          string
	ExpiresAt          time.Time
	ConfirmedByOpenID  string
	ConfirmedAt        *time.Time
}

type NewPendingOrderRequest struct {
	AppID              string
	BotOpenID          string
	ChatID             string
	InitiatorOpenID    string
	RequesterOpenID    string
	CheckoutMode       CheckoutMode
	Credential         Credential
	CreateOrderPayload json.RawMessage
	PreviewResult      json.RawMessage
	CartSnapshot       []CartItem
	Now                time.Time
}

func NewPendingOrder(req NewPendingOrderRequest) PendingOrder {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	hash := sha256.Sum256(req.CreateOrderPayload)
	return PendingOrder{
		ID:                 uuid.NewString(),
		AppID:              req.AppID,
		BotOpenID:          req.BotOpenID,
		ChatID:             req.ChatID,
		RequesterOpenID:    firstNonEmpty(req.RequesterOpenID, req.InitiatorOpenID),
		InitiatorOpenID:    req.InitiatorOpenID,
		CheckoutMode:       NormalizeCheckoutMode(string(req.CheckoutMode)),
		CredentialScope:    req.Credential.Scope,
		MCPServerName:      ServerName,
		CreateOrderPayload: req.CreateOrderPayload,
		PayloadHash:        hex.EncodeToString(hash[:]),
		PreviewResult:      req.PreviewResult,
		CartSnapshot:       req.CartSnapshot,
		Status:             PendingStatusPending,
		ResultJSON:         json.RawMessage(`{}`),
		ExpiresAt:          now.Add(10 * time.Minute),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
