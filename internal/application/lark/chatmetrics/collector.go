package chatmetrics

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const DefaultRecentWindow = 24 * time.Hour

type Chat struct {
	ID     string
	Name   string
	Status string
}

type Snapshot struct {
	Chat               Chat
	MemberCount        int
	RecentMessageCount int
	RecentWindow       time.Duration
	CollectedAt        time.Time
}

type Collector struct {
	ListChats           func(context.Context) ([]Chat, error)
	CountMembers        func(context.Context, string) (int, error)
	CountRecentMessages func(context.Context, string, time.Time) (int, error)
	Record              func(Snapshot)
	Now                 func() time.Time
	RecentWindow        time.Duration
}

func (c Collector) Collect(ctx context.Context) error {
	if c.ListChats == nil {
		return errors.New("chat metrics list chats func is nil")
	}
	if c.CountMembers == nil {
		return errors.New("chat metrics count members func is nil")
	}
	if c.CountRecentMessages == nil {
		return errors.New("chat metrics count recent messages func is nil")
	}
	if c.Record == nil {
		return errors.New("chat metrics record func is nil")
	}

	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	window := c.RecentWindow
	if window <= 0 {
		window = DefaultRecentWindow
	}
	since := now.Add(-window)

	chats, err := c.ListChats(ctx)
	if err != nil {
		return err
	}

	var joined error
	for _, chat := range chats {
		if chat.ID == "" {
			continue
		}
		memberCount, memberErr := c.CountMembers(ctx, chat.ID)
		if memberErr != nil {
			joined = errors.Join(joined, fmt.Errorf("count members for %s: %w", chat.ID, memberErr))
			continue
		}
		recentMessages, messageErr := c.CountRecentMessages(ctx, chat.ID, since)
		if messageErr != nil {
			joined = errors.Join(joined, fmt.Errorf("count recent messages for %s: %w", chat.ID, messageErr))
			continue
		}
		c.Record(Snapshot{
			Chat:               chat,
			MemberCount:        memberCount,
			RecentMessageCount: recentMessages,
			RecentWindow:       window,
			CollectedAt:        now,
		})
	}
	return joined
}
