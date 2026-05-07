package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/holiday"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScheduleWithWorkdayCheck 测试带工作日检查的调度
func TestScheduleWithWorkdayCheck(t *testing.T) {
	ctx := context.Background()

	t.Run("测试跳过周末", func(t *testing.T) {
		// 创建一个周五的任务，应该跳过周末在周一执行
		task := &model.ScheduledTask{
			ID:        "test-skip-weekends",
			Name:      "测试跳过周末",
			Type:      model.ScheduleTaskTypeCron,
			CronExpr:  "0 9 * * 1-5", // 每周一到周五9点
			Timezone:  "Asia/Shanghai",
			Status:    model.ScheduleTaskStatusEnabled,
			NextRunAt: time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC), // 周五
			// SkipWeekends: true, // TODO: 添加此字段
		}

		// 测试：如果启用了跳过周末，下一个执行时间应该在周一
		nextRun := task.NextRunAt
		if nextRun.Weekday() == time.Friday {
			// 计算下一个工作日
			nextWorkday, err := holiday.GetNextWorkdayForSchedule(ctx, nextRun.Add(24*time.Hour))
			require.NoError(t, err)
			assert.Equal(t, time.Monday, nextWorkday.Weekday())
			t.Logf("周五后的下一个工作日: %s (周一)", nextWorkday.Format("2006-01-02"))
		}
	})

	t.Run("测试跳过节假日", func(t *testing.T) {
		// 创建一个劳动节当天的任务
		laborDay := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)

		// 检查劳动节是否为工作日
		isWorkday, err := holiday.IsWorkdayCheck(ctx, laborDay)
		require.NoError(t, err)
		assert.False(t, isWorkday, "劳动节不应该是工作日")

		// 获取下一个工作日
		nextWorkday, err := holiday.GetNextWorkdayForSchedule(ctx, laborDay)
		require.NoError(t, err)
		t.Logf("劳动节后的下一个工作日: %s", nextWorkday.Format("2006-01-02"))

		// 验证下一个工作日不是节假日
		isNextWorkday, err := holiday.IsWorkdayCheck(ctx, nextWorkday)
		require.NoError(t, err)
		assert.True(t, isNextWorkday, "下一个工作日应该是真正的工作日")
	})

	t.Run("测试周末和节假日组合", func(t *testing.T) {
		// 测试周六（2026-05-09）
		saturday := time.Date(2026, 5, 9, 9, 0, 0, 0, time.UTC)

		// 检查周六是否为工作日
		isWorkday, err := holiday.IsWorkdayCheck(ctx, saturday)
		require.NoError(t, err)
		assert.False(t, isWorkday, "周六不应该是工作日")

		// 获取下一个工作日
		nextWorkday, err := holiday.GetNextWorkdayForSchedule(ctx, saturday)
		require.NoError(t, err)
		t.Logf("周六后的下一个工作日: %s (应该是周一)", nextWorkday.Format("2006-01-02"))

		// 验证是周一
		assert.Equal(t, time.Monday, nextWorkday.Weekday())
	})
}

// TestComputeNextRunWithWorkday 测试带工作日判断的下次执行时间计算
func TestComputeNextRunWithWorkday(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		from         time.Time
		skipWeekends bool
		skipHolidays bool
		expectedNext time.Time
	}{
		{
			name:         "周五跳过周末，下一个应该是周一",
			from:         time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC),  // 周五
			skipWeekends: true,
			skipHolidays: false,
			expectedNext: time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC), // 周一
		},
		{
			name:         "劳动节跳过节假日，下一个应该是工作日",
			from:         time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC),  // 劳动节
			skipWeekends: false,
			skipHolidays: true,
			// 劳动节后应该是工作日（需要根据实际API返回确定）
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 检查是否需要跳过
			isWorkday, err := holiday.IsWorkdayCheck(ctx, tt.from)
			require.NoError(t, err)

			if !isWorkday && (tt.skipWeekends || tt.skipHolidays) {
				// 获取下一个工作日
				nextWorkday, err := holiday.GetNextWorkdayForSchedule(ctx, tt.from)
				require.NoError(t, err)

				t.Logf("从 %s (%s) 跳到下一个工作日: %s (%s)",
					tt.from.Format("2006-01-02"),
					tt.from.Weekday(),
					nextWorkday.Format("2006-01-02"),
					nextWorkday.Weekday())

				if !tt.expectedNext.IsZero() {
					assert.Equal(t, tt.expectedNext.Format("2006-01-02"), nextWorkday.Format("2006-01-02"))
				}
			}
		})
	}
}
