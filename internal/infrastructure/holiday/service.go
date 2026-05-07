package holiday

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/cache"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const (
	// API基础地址
	apiBaseURL = "http://timor.tech/api/holiday"
)

// DateInfo 日期信息
type DateInfo struct {
	Date string `json:"date"`     // 日期 YYYY-MM-DD (可能不存在)
	Type int    `json:"type"`     // 类型：0=工作日、1=周末、2=节假日
	Name string `json:"name"`     // 节假日名称（如果有）
	Week int    `json:"week"`     // 星期几（1-7）
}

// HolidayInfo 节假日信息
type HolidayInfo struct {
	Date    string `json:"date"`     // 日期 YYYY-MM-DD
	Name    string `json:"name"`     // 节假日名称
	Holiday bool   `json:"holiday"`  // 是否为节假日
	Wage    int    `json:"wage"`     // 薪资倍数（1-3）
	Reason  string `json:"reason"`   // 调休原因（如果是调休）
}

// NextHolidayInfo 下一个节假日信息
type NextHolidayInfo struct {
	Holiday HolidayInfo `json:"holiday"` // 下一个节假日信息
	Days    int         `json:"days"`    // 距离天数
	Workday *HolidayInfo `json:"workday,omitempty"` // 如果之前有调休，返回调休信息
}

// NextWorkdayInfo 下一个工作日信息
type NextWorkdayInfo struct {
	Date string `json:"date"`     // 日期 YYYY-MM-DD
	Type int    `json:"type"`     // 类型：0=工作日、1=周末、2=节假日
	Name string `json:"name"`     // 名称
	Days int    `json:"days"`     // 距离天数（可能不存在）
	Week int    `json:"week"`     // 星期几（1-7）
}

// API响应结构
type holidayInfoResponse struct {
	Code    int          `json:"code"`
	Type    DateInfo     `json:"type"`
	Holiday *HolidayInfo `json:"holiday"` // 可能为null
}

type nextHolidayResponse struct {
	Code     int             `json:"code"`
	Holiday  NextHolidayInfo `json:"holiday"`
	Workday  *NextHolidayInfo `json:"workday,omitempty"`
}

type nextWorkdayResponse struct {
	Code    int              `json:"code"`
	Workday NextWorkdayInfo  `json:"workday"`
}

type yearHolidaysResponse struct {
	Code    int                    `json:"code"`
	Holiday map[string]HolidayInfo `json:"holiday"`
	Type    map[string]DateInfo    `json:"type,omitempty"`
}

type ttsResponse struct {
	Code int    `json:"code"`
	TTS  string `json:"tts"`
}

// Service 节假日服务
type Service struct {
	client *httpclient
}

var (
	globalService *Service
)

// initService 初始化节假日服务
func initService() {
	globalService = &Service{
		client: newHTTPClient(),
	}
}

// GetService 获取节假日服务实例
func GetService() *Service {
	if globalService == nil {
		initService()
	}
	return globalService
}

// IsWorkday 判断指定日期是否为工作日
func (s *Service) IsWorkday(ctx context.Context, date time.Time) (bool, error) {
	ctx, span := otel.StartNamed(ctx, "holiday.is_workday")
	defer span.End()
	var err error
	defer otel.RecordErrorPtr(span, &err)

	dateStr := date.Format("2006-01-02")
	span.SetAttributes(attribute.String("holiday.date", dateStr))

	info, err := s.GetDateInfo(ctx, dateStr)
	if err != nil {
		return false, err
	}

	// 工作日判断：type=0表示工作日
	// type字段：0=工作日、1=周末、2=节假日
	isWorkday := info.Type.Type == 0

	span.SetAttributes(attribute.Bool("holiday.is_workday", isWorkday))
	return isWorkday, nil
}

// GetDateInfo 获取指定日期的节假日信息
func (s *Service) GetDateInfo(ctx context.Context, date string) (*holidayInfoResponse, error) {
	ctx, span := otel.StartNamed(ctx, "holiday.get_date_info")
	defer span.End()
	var err error
	defer otel.RecordErrorPtr(span, &err)

	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	span.SetAttributes(attribute.String("holiday.date", date))

	// 使用缓存包装函数
	cacheKey := "holiday:info:" + date
	result, err := cache.GetOrExecute(ctx, cacheKey, func() (*holidayInfoResponse, error) {
		url := fmt.Sprintf("%s/info/%s", apiBaseURL, date)
		var res holidayInfoResponse
		if err := s.client.GetJSON(ctx, url, &res); err != nil {
			return nil, err
		}
		return &res, nil
	})

	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get holiday info",
			zap.Error(err),
			zap.String("date", date))
		return nil, fmt.Errorf("failed to get holiday info: %w", err)
	}

	return result, nil
}

// GetNextHoliday 获取下一个节假日
func (s *Service) GetNextHoliday(ctx context.Context, date string) (*nextHolidayResponse, error) {
	ctx, span := otel.StartNamed(ctx, "holiday.get_next_holiday")
	defer span.End()
	var err error
	defer otel.RecordErrorPtr(span, &err)

	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	span.SetAttributes(attribute.String("holiday.date", date))

	// 使用缓存包装函数
	cacheKey := "holiday:next:" + date
	result, err := cache.GetOrExecute(ctx, cacheKey, func() (*nextHolidayResponse, error) {
		url := fmt.Sprintf("%s/next/%s", apiBaseURL, date)
		var res nextHolidayResponse
		if err := s.client.GetJSON(ctx, url, &res); err != nil {
			return nil, err
		}
		return &res, nil
	})

	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get next holiday",
			zap.Error(err),
			zap.String("date", date))
		return nil, fmt.Errorf("failed to get next holiday: %w", err)
	}

	return result, nil
}

// GetNextWorkday 获取下一个工作日
func (s *Service) GetNextWorkday(ctx context.Context, date string) (*nextWorkdayResponse, error) {
	ctx, span := otel.StartNamed(ctx, "holiday.get_next_workday")
	defer span.End()
	var err error
	defer otel.RecordErrorPtr(span, &err)

	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	span.SetAttributes(attribute.String("holiday.date", date))

	// 使用缓存包装函数
	cacheKey := "holiday:workday:" + date
	result, err := cache.GetOrExecute(ctx, cacheKey, func() (*nextWorkdayResponse, error) {
		url := fmt.Sprintf("%s/workday/next/%s", apiBaseURL, date)
		var res nextWorkdayResponse
		if err := s.client.GetJSON(ctx, url, &res); err != nil {
			return nil, err
		}
		return &res, nil
	})

	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get next workday",
			zap.Error(err),
			zap.String("date", date))
		return nil, fmt.Errorf("failed to get next workday: %w", err)
	}

	return result, nil
}

// GetYearHolidays 获取指定年份的所有节假日
func (s *Service) GetYearHolidays(ctx context.Context, year string) (*yearHolidaysResponse, error) {
	ctx, span := otel.StartNamed(ctx, "holiday.get_year_holidays")
	defer span.End()
	var err error
	defer otel.RecordErrorPtr(span, &err)

	if year == "" {
		year = strconv.Itoa(time.Now().Year())
	}
	span.SetAttributes(attribute.String("holiday.year", year))

	// 使用缓存包装函数
	cacheKey := "holiday:year:" + year
	result, err := cache.GetOrExecute(ctx, cacheKey, func() (*yearHolidaysResponse, error) {
		url := fmt.Sprintf("%s/year/%s", apiBaseURL, year)
		var res yearHolidaysResponse
		if err := s.client.GetJSON(ctx, url, &res); err != nil {
			return nil, err
		}
		return &res, nil
	})

	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get year holidays",
			zap.Error(err),
			zap.String("year", year))
		return nil, fmt.Errorf("failed to get year holidays: %w", err)
	}

	return result, nil
}

// GetTTS 获取语音播报文本
func (s *Service) GetTTS(ctx context.Context, ttsType string) (string, error) {
	ctx, span := otel.StartNamed(ctx, "holiday.get_tts")
	defer span.End()
	var err error
	defer otel.RecordErrorPtr(span, &err)

	span.SetAttributes(attribute.String("holiday.tts_type", ttsType))

	// 构建URL
	var url string
	switch ttsType {
	case "next":
		url = fmt.Sprintf("%s/tts/next", apiBaseURL)
	case "tomorrow":
		url = fmt.Sprintf("%s/tts/tomorrow", apiBaseURL)
	default:
		url = fmt.Sprintf("%s/tts", apiBaseURL)
	}

	// 使用缓存包装函数
	cacheKey := "holiday:tts:" + ttsType
	result, err := cache.GetOrExecute(ctx, cacheKey, func() (string, error) {
		var res ttsResponse
		if err := s.client.GetJSON(ctx, url, &res); err != nil {
			return "", err
		}
		return res.TTS, nil
	})

	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to get TTS",
			zap.Error(err),
			zap.String("tts_type", ttsType))
		return "", fmt.Errorf("failed to get TTS: %w", err)
	}

	return result, nil
}

// BatchGetDateInfo 批量获取日期信息
func (s *Service) BatchGetDateInfo(ctx context.Context, dates []string) (map[string]*holidayInfoResponse, error) {
	ctx, span := otel.StartNamed(ctx, "holiday.batch_get_date_info")
	defer span.End()
	var err error
	defer otel.RecordErrorPtr(span, &err)

	span.SetAttributes(attribute.Int("holiday.dates_count", len(dates)))

	if len(dates) == 0 {
		return nil, fmt.Errorf("dates cannot be empty")
	}

	if len(dates) > 50 {
		return nil, fmt.Errorf("too many dates, maximum is 50")
	}

	// 使用缓存包装函数
	cacheKey := "holiday:batch:" + strings.Join(dates, ",")
	result, err := cache.GetOrExecute(ctx, cacheKey, func() (map[string]*holidayInfoResponse, error) {
		// 构建URL
		url := fmt.Sprintf("%s/batch?d=%s", apiBaseURL, strings.Join(dates, "&d="))

		// 调用API
		var res map[string]holidayInfoResponse
		if err := s.client.GetJSON(ctx, url, &res); err != nil {
			return nil, err
		}

		// 转换结果
		output := make(map[string]*holidayInfoResponse)
		for k, v := range res {
			vCopy := v
			output[k] = &vCopy
		}

		return output, nil
	})

	if err != nil {
		logs.L().Ctx(ctx).Error("Failed to batch get date info",
			zap.Error(err),
			zap.Int("dates_count", len(dates)))
		return nil, fmt.Errorf("failed to batch get date info: %w", err)
	}

	return result, nil
}

// IsWorkdayCheck 检查指定日期是否为工作日（简单版本，用于schedule）
func IsWorkdayCheck(ctx context.Context, date time.Time) (bool, error) {
	return GetService().IsWorkday(ctx, date)
}

// GetNextWorkdayForSchedule 为schedule系统获取下一个工作日
func GetNextWorkdayForSchedule(ctx context.Context, from time.Time) (time.Time, error) {
	ctx, span := otel.StartNamed(ctx, "holiday.get_next_workday_for_schedule")
	defer span.End()

	dateStr := from.Format("2006-01-02")
	result, err := GetService().GetNextWorkday(ctx, dateStr)
	if err != nil {
		return time.Time{}, err
	}

	nextDate, err := time.Parse("2006-01-02", result.Workday.Date)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse next workday: %w", err)
	}

	return nextDate, nil
}
