package luckin

import "time"

// PollDecision 描述一次轮询后的处理结果，供 worker 执行。
type PollDecision struct {
	// Status 非空表示订单进入终止状态需写库并停止轮询。
	Status OrderRecordStatus
	// LastRemoteStatus 远端状态变化时设置。
	LastRemoteStatus *int
	// UnpaidReminded 触发未支付提醒后置 true。
	UnpaidReminded *bool
	// NextPollAt 非 nil 表示继续轮询并安排下次时间。
	NextPollAt *time.Time
	// StoppedReason 终止原因（终态/超时/未支付超时/token 失效）。
	StoppedReason string
	// StatusTimestamps 需要写入的节点时间戳列名->时间。
	StatusTimestamps map[string]time.Time
	// NoticeText 非空表示需要发节点通知卡。
	NoticeText string
	// PatchStatusCard 是否需要把订单卡更新为最新状态。
	PatchStatusCard bool
	// SendUnpaidReminder 是否需要发未支付提醒卡。
	SendUnpaidReminder bool
}

// EvaluatePoll 根据当前记录与远端订单详情计算轮询决策（纯函数，便于测试）。
func EvaluatePoll(record OrderRecord, detail OrderDetail, cfg OrderPollConfig, now time.Time) PollDecision {
	decision := PollDecision{StatusTimestamps: map[string]time.Time{}}

	// 全局最大轮询时长兜底。
	if !record.PollDeadline.IsZero() && now.After(record.PollDeadline) {
		decision.Status = OrderRecordExpired
		decision.StoppedReason = "poll deadline exceeded"
		return decision
	}

	remoteStatus := detail.Status
	if remoteStatus == 0 {
		remoteStatus = inferOrderStatus(detail.StatusName)
	}
	statusChanged := remoteStatus != 0 && remoteStatus != record.LastRemoteStatus
	if statusChanged {
		s := remoteStatus
		decision.LastRemoteStatus = &s
		if col := statusTimestampColumn(remoteStatus); col != "" {
			decision.StatusTimestamps[col] = now
		}
		if notice := OrderStatusNotice(remoteStatus); notice != "" {
			decision.NoticeText = notice
		} else {
			decision.PatchStatusCard = true
		}
	}

	// 终止状态：已完成/已取消。
	if IsTerminalOrderStatus(remoteStatus) {
		if remoteStatus == OrderStatusCompleted {
			decision.Status = OrderRecordCompleted
		} else {
			decision.Status = OrderRecordCancelled
		}
		decision.StoppedReason = "terminal status " + orderStatusLabel(remoteStatus)
		return decision
	}

	// 未支付相关处理。
	if remoteStatus == OrderStatusUnpaid || (remoteStatus == 0 && record.LastRemoteStatus == OrderStatusUnpaid) {
		elapsed := now.Sub(record.CreatedAt)
		if cfg.UnpaidTimeout > 0 && elapsed >= cfg.UnpaidTimeout {
			decision.Status = OrderRecordExpired
			decision.StoppedReason = "unpaid timeout"
			return decision
		}
		if !record.UnpaidReminded && cfg.UnpaidRemindAt > 0 && elapsed >= cfg.UnpaidRemindAt {
			reminded := true
			decision.UnpaidReminded = &reminded
			decision.SendUnpaidReminder = true
		}
	}

	// 继续轮询。
	interval := cfg.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	// 待支付阶段需要更快感知支付完成；后续制作/取餐阶段保持 3-5s 的实时性。
	if remoteStatus == OrderStatusUnpaid || (remoteStatus == 0 && record.LastRemoteStatus == OrderStatusUnpaid) {
		interval = minDuration(interval, 1*time.Second)
	} else {
		interval = minDuration(interval, 5*time.Second)
	}
	next := now.Add(interval)
	decision.NextPollAt = &next
	return decision
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}

func statusTimestampColumn(status int) string {
	switch status {
	case OrderStatusPlaced:
		return "placed_at"
	case OrderStatusMaking:
		return "making_at"
	case OrderStatusReady:
		return "ready_at"
	case OrderStatusCompleted:
		return "completed_at"
	case OrderStatusCancelled:
		return "cancelled_at"
	default:
		return ""
	}
}
