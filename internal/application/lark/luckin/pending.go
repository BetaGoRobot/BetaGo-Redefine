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
	RequesterOpenID    string
	CredentialScope    CredentialScope
	MCPServerName      string
	CreateOrderPayload json.RawMessage
	PayloadHash        string
	PreviewResult      json.RawMessage
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
	RequesterOpenID    string
	Credential         Credential
	CreateOrderPayload json.RawMessage
	PreviewResult      json.RawMessage
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
		RequesterOpenID:    req.RequesterOpenID,
		CredentialScope:    req.Credential.Scope,
		MCPServerName:      ServerName,
		CreateOrderPayload: req.CreateOrderPayload,
		PayloadHash:        hex.EncodeToString(hash[:]),
		PreviewResult:      req.PreviewResult,
		Status:             PendingStatusPending,
		ResultJSON:         json.RawMessage(`{}`),
		ExpiresAt:          now.Add(10 * time.Minute),
	}
}
