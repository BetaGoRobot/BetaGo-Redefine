package luckin

import (
	"context"
	"time"
)

// OrderRecordStatus 本地订单跟踪状态。
type OrderRecordStatus string

const (
	OrderRecordActive    OrderRecordStatus = "active"
	OrderRecordCompleted OrderRecordStatus = "completed"
	OrderRecordCancelled OrderRecordStatus = "cancelled"
	OrderRecordExpired   OrderRecordStatus = "expired"
	OrderRecordFailed    OrderRecordStatus = "failed"
)

// OrderRecord 本地订单跟踪记录，用于轮询生命周期。
type OrderRecord struct {
	OrderID          string
	AppID            string
	BotOpenID        string
	ChatID           string
	RequesterOpenID  string
	CredentialScope  CredentialScope
	MessageID        string
	Status           OrderRecordStatus
	LastRemoteStatus int
	NeedPay          bool
	PayURL           string
	QRCodeURL        string
	DiscountPrice    float64
	UnpaidReminded   bool
	NextPollAt       time.Time
	PollDeadline     time.Time
	FailCount        int
	StoppedReason    string
	CreatedAt        time.Time
}

// OrderTracker 持久化订单跟踪记录，供轮询 worker 使用。
type OrderTracker interface {
	CreateOrder(ctx context.Context, record OrderRecord) error
}
