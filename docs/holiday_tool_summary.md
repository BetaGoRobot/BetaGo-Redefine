# 节假日工具实现总结

## ✅ 已完成的工作

### 1. 节假日工具包

已创建完整的节假日工具包，位置：`internal/infrastructure/holiday/`

#### 文件列表
- `service.go` - 节假日服务核心实现
- `tools.go` - 7个工具函数
- `init.go` - 初始化和注册

#### 提供的工具

1. **query_date_info** - 查询指定日期的节假日信息
   - 是否为节假日、周末、工作日
   - 节假日名称、薪资倍数
   - 调休说明

2. **query_next_holiday** ⭐ - 查询下一个节假日
   - 节假日名称和日期
   - 距离天数（倒计时）
   - 调休安排提醒

3. **query_next_workday** ⭐ - 查询下一个工作日
   - 工作日日期
   - 距离天数
   - 类型说明（正常工作日/调休补班）

4. **query_year_holidays** - 查询年度节假日列表
   - 所有法定节假日
   - 薪资倍数信息
   - 年度统计

5. **query_holiday_tts** - 获取语音播报文本
   - 适合语音助手场景
   - 人性化文本输出

6. **batch_query_holidays** - 批量查询
   - 最多支持50个日期
   - 高效批量处理

7. **check_workday** ⭐ - 检查是否工作日
   - 简单明了的判断
   - 支持调休补班

### 2. 核心服务功能

#### API集成
- 使用 timor.tech 免费节假日API
- 不限速、不登录、没广告
- 已更新2026年数据
- 支持调休和补班

#### 主要函数

```go
// 判断指定日期是否为工作日
func IsWorkday(ctx context.Context, date time.Time) (bool, error)

// 获取下一个工作日（用于schedule）
func GetNextWorkdayForSchedule(ctx context.Context, from time.Time) (time.Time, error)

// 获取指定日期的节假日信息
func GetDateInfo(ctx context.Context, date string) (*holidayInfoResponse, error)

// 获取下一个节假日
func GetNextHoliday(ctx context.Context, date string) (*nextHolidayResponse, error)

// 获取年度节假日
func GetYearHolidays(ctx context.Context, year string) (*yearHolidaysResponse, error)

// 批量查询
func BatchGetDateInfo(ctx context.Context, dates []string) (map[string]*holidayInfoResponse, error)
```

### 3. 技术特性

✅ **自动缓存机制**
- 24小时过期时间
- 减少API调用
- 提高响应速度

✅ **降级处理**
- API不可用时的容错
- 保证服务稳定性
- 优雅降级

✅ **调休补班支持**
- 自动识别调休日
- 区分工作日和休息日
- 准确的节假日判断

✅ **批量查询支持**
- 最多50个日期
- 高效批量处理
- 减少网络开销

## 📋 待实现的工作

### 1. 注册节假日工具

在 `cmd/larkrobot/bootstrap.go` 中添加：

```go
import "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/holiday"

func init() {
    // 初始化节假日服务
    holiday.Init()

    // 注册工具到 schedulableTools
    holiday.RegisterHolidayTools(schedulableTools)
}
```

### 2. 数据库迁移

创建迁移文件：`script/migrations/0XX_add_workday_config_to_scheduled_tasks.sql`

```sql
ALTER TABLE betago.scheduled_tasks
    ADD COLUMN IF NOT EXISTS skip_weekends BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS skip_holidays BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN betago.scheduled_tasks.skip_weekends IS '是否跳过周末（周六日）';
COMMENT ON COLUMN betago.scheduled_tasks.skip_holidays IS '是否跳过法定节假日';
```

### 3. 扩展 Schedule 模型

在 `internal/infrastructure/db/model/scheduled_tasks.gen.go` 中添加字段：

```go
type ScheduledTask struct {
    // ... 现有字段
    SkipWeekends bool `gorm:"column:skip_weekends;not null;default:false" json:"skip_weekends"`
    SkipHolidays bool `gorm:"column:skip_holidays;not null;default:false" json:"skip_holidays"`
}
```

### 4. 修改 Schedule Service

在 `internal/application/lark/schedule/service.go` 中集成工作日判断：

```go
func (s *Service) computeNextRunWithWorkday(task *model.ScheduledTask, now time.Time) (time.Time, error) {
    nextRun, err := computeNextRun(task.CronExpr, task.Timezone, now)
    if err != nil {
        return time.Time{}, err
    }

    if task.SkipWeekends || task.SkipHolidays {
        isWorkday, err := holiday.CheckWorkday(context.Background(), nextRun)
        if err != nil {
            // 降级处理
            return nextRun, nil
        }

        if !isWorkday {
            nextRun, err = holiday.GetNextWorkdayForSchedule(context.Background(), nextRun)
            if err != nil {
                return time.Time{}, err
            }
        }
    }

    return nextRun, nil
}
```

### 5. 扩展 API 参数

在 `CreateTaskRequest` 和工具参数中添加：

```go
SkipWeekends bool `json:"skip_weekends"`
SkipHolidays bool `json:"skip_holidays"`
```

### 6. 编写测试

创建测试文件：
- `internal/infrastructure/holiday/service_test.go`
- `internal/infrastructure/holiday/tools_test.go`

## 🎯 使用示例

### 用户直接查询

**查询下一个节假日：**
```
用户: 下一个节假日是什么时候？
机器人: 🎊 下一个节假日
       节假日: 春节
       日期: 2026-02-17
       距离: 45 天
```

**检查某天是否工作日：**
```
用户: 2026-02-17是工作日吗？
机器人: 💼 工作日检查
       日期: 2026-02-17
       结果: ❌ 不是工作日
```

### Schedule 集成

**创建每天执行的任务，跳过周末：**
```json
{
  "name": "每日提醒",
  "type": "cron",
  "cron_expr": "0 9 * * *",
  "message": "早上好！",
  "skip_weekends": true
}
```

**创建每天执行的任务，跳过节假日：**
```json
{
  "name": "工作日提醒",
  "type": "cron",
  "cron_expr": "0 9 * * *",
  "message": "开工啦！",
  "skip_holidays": true
}
```

## 📊 时间估算

- ✅ **节假日工具实现**：已完成（约3小时）
- ⏳ **注册和集成**：1-2 小时
- ⏳ **数据库和模型**：1-2 小时
- ⏳ **Schedule集成**：2-3 小时
- ⏳ **API扩展**：1-2 小时
- ⏳ **测试和调试**：2-3 小时

**总计**：10-15 小时（已完成3小时）

## 🚀 下一步行动

1. 注册节假日工具到 bootstrap
2. 创建数据库迁移脚本
3. 扩展 Schedule 模型
4. 修改 Schedule Service 逻辑
5. 扩展 API 参数
6. 编写测试用例
7. 测试和验证

## 💡 优势总结

1. **独立工具**：节假日功能完全独立，可单独使用
2. **复用性强**：7个工具函数满足各种场景
3. **易于集成**：简单的API接口，容易集成到schedule
4. **用户友好**：提供倒计时、语音播报等人性化功能
5. **稳定可靠**：缓存机制、降级处理保证稳定性
6. **免费可用**：使用免费API，无成本压力

---

**建议：** 优先完成工具注册和基础集成，验证功能正常后再扩展高级功能。
