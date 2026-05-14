package holiday

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipHolidayIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("BETAGO_RUN_HOLIDAY_INTEGRATION") != "1" {
		t.Skip("set BETAGO_RUN_HOLIDAY_INTEGRATION=1 to run live holiday API integration test")
	}
}

// TestIsWorkday 测试工作日判断
func TestIsWorkday(t *testing.T) {
	skipHolidayIntegration(t)
	ctx := context.Background()
	svc := GetService()

	tests := []struct {
		name     string
		date     time.Time
		expected bool
	}{
		{
			name:     "周三工作日应该是工作日",
			date:     time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC), // 2026-05-06 周三，工作日
			expected: true,
		},
		{
			name:     "周六不应该工作日",
			date:     time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC), // 2026-06-13 周六
			expected: false,
		},
		{
			name:     "周日不应该是工作日",
			date:     time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC), // 2026-06-14 周日
			expected: false,
		},
		{
			name:     "劳动节不应该是工作日",
			date:     time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC), // 2026-05-04 劳动节
			expected: false,
		},
		{
			name:     "元旦不应该是工作日",
			date:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), // 2026-01-01 元旦
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isWorkday, err := svc.IsWorkday(ctx, tt.date)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, isWorkday, tt.name)
		})
	}
}

// TestGetDateInfo 测试获取日期信息
func TestGetDateInfo(t *testing.T) {
	skipHolidayIntegration(t)
	ctx := context.Background()
	svc := GetService()

	tests := []struct {
		name          string
		date          string
		expectType    int
		expectHoliday bool
	}{
		{
			name:          "查询元旦",
			date:          "2026-01-01",
			expectType:    2, // 节假日
			expectHoliday: true,
		},
		{
			name:          "查询普通工作日",
			date:          "2026-05-06",
			expectType:    0, // 工作日
			expectHoliday: false,
		},
		{
			name:          "查询周六",
			date:          "2026-06-13",
			expectType:    1, // 周末
			expectHoliday: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := svc.GetDateInfo(ctx, tt.date)
			require.NoError(t, err)
			require.NotNil(t, info)
			assert.Equal(t, tt.expectType, info.Type.Type)
			if tt.expectHoliday && info.Holiday != nil {
				assert.Equal(t, tt.expectHoliday, info.Holiday.Holiday)
			}
		})
	}
}

// TestGetNextHoliday 测试获取下一个节假日
func TestGetNextHoliday(t *testing.T) {
	skipHolidayIntegration(t)
	ctx := context.Background()
	svc := GetService()

	t.Run("从2026-01-01查询下一个节假日", func(t *testing.T) {
		info, err := svc.GetNextHoliday(ctx, "2026-01-01")
		require.NoError(t, err)
		require.NotNil(t, info)
		assert.NotEmpty(t, info.Holiday.Name)
		assert.NotEmpty(t, info.Holiday.Date)
	})

	t.Run("不指定日期查询下一个节假日", func(t *testing.T) {
		info, err := svc.GetNextHoliday(ctx, "")
		require.NoError(t, err)
		require.NotNil(t, info)
		assert.NotEmpty(t, info.Holiday.Name)
	})
}

// TestGetNextWorkday 测试获取下一个工作日
func TestGetNextWorkday(t *testing.T) {
	skipHolidayIntegration(t)
	ctx := context.Background()
	svc := GetService()

	tests := []struct {
		name     string
		date     string
		wantDate string
	}{
		{
			name:     "周五的下一个工作日应该是周一",
			date:     "2026-06-12", // 周五
			wantDate: "2026-06-15", // 周一
		},
		{
			name:     "周六的下一个工作日应该是周一",
			date:     "2026-06-13", // 周六
			wantDate: "2026-06-15", // 周一
		},
		{
			name:     "周日的下一个工作日应该是周一",
			date:     "2026-06-14", // 周日
			wantDate: "2026-06-15", // 周一
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := svc.GetNextWorkday(ctx, tt.date)
			require.NoError(t, err)
			require.NotNil(t, info)
			assert.Equal(t, tt.wantDate, info.Workday.Date)
		})
	}
}

// TestGetYearHolidays 测试获取年度节假日
func TestGetYearHolidays(t *testing.T) {
	skipHolidayIntegration(t)
	ctx := context.Background()
	svc := GetService()

	tests := []struct {
		year           string
		expectMinCount int
	}{
		{"2026", 7}, // 至少有7个法定节假日
		{"2025", 7},
	}

	for _, tt := range tests {
		t.Run("查询"+tt.year+"年节假日", func(t *testing.T) {
			info, err := svc.GetYearHolidays(ctx, tt.year)
			require.NoError(t, err)
			require.NotNil(t, info)
			assert.NotNil(t, info.Holiday)

			// 统计节假日数量
			count := 0
			for _, h := range info.Holiday {
				if h.Holiday {
					count++
				}
			}
			assert.GreaterOrEqual(t, count, tt.expectMinCount)
		})
	}
}

// TestBatchGetDateInfo 测试批量查询
func TestBatchGetDateInfo(t *testing.T) {
	skipHolidayIntegration(t)
	ctx := context.Background()
	svc := GetService()

	tests := []struct {
		name      string
		dates     []string
		expectLen int
		wantErr   bool
	}{
		{
			name:      "批量查询多个日期",
			dates:     []string{"2026-01-01", "2026-05-01", "2026-10-01"},
			expectLen: 3,
			wantErr:   false,
		},
		{
			name:      "空数组应该返回错误",
			dates:     []string{},
			expectLen: 0,
			wantErr:   true,
		},
		{
			name:      "超过50个日期应该返回错误",
			dates:     make([]string, 51),
			expectLen: 0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := svc.BatchGetDateInfo(ctx, tt.dates)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, results, tt.expectLen)
			}
		})
	}
}

// TestGetTTS 测试获取语音播报文本
func TestGetTTS(t *testing.T) {
	skipHolidayIntegration(t)
	ctx := context.Background()
	svc := GetService()

	tests := []struct {
		name    string
		ttsType string
	}{
		{"放假安排", "holiday"},
		{"下一个节假日", "next"},
		{"明天放假吗", "tomorrow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tts, err := svc.GetTTS(ctx, tt.ttsType)
			require.NoError(t, err)
			assert.NotEmpty(t, tts)
			t.Logf("TTS: %s", tts)
		})
	}
}

// TestIsWorkdayCheck 测试检查工作日函数
func TestIsWorkdayCheck(t *testing.T) {
	skipHolidayIntegration(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		date     time.Time
		expected bool
	}{
		{"工作日", time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC), true},
		{"周末", time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC), false},
		{"节假日", time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isWorkday, err := IsWorkdayCheck(ctx, tt.date)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, isWorkday)
		})
	}
}

// TestGetNextWorkdayForSchedule 测试schedule专用的获取下一个工作日
func TestGetNextWorkdayForSchedule(t *testing.T) {
	skipHolidayIntegration(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		from     time.Time
		expected time.Time
	}{
		{
			name:     "周五的下一个工作日是周一",
			from:     time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC),
			expected: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "周六的下一个工作日是周一",
			from:     time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC),
			expected: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextWorkday, err := GetNextWorkdayForSchedule(ctx, tt.from)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, nextWorkday)
		})
	}
}
