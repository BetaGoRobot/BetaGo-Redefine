# Schedule 工作日识别配置实现计划

## 一、需求概述
为 schedule 系统增加工作日识别功能，使得定时任务可以：
1. 自动跳过周末（周六日）
2. 自动跳过法定节假日
3. 仅在工作日执行

## 二、技术方案（已实现节假日工具）

### 2.1 节假日工具 ✅ 已完成

已创建独立的节假日工具包：`internal/infrastructure/holiday/`

**提供的工具：**
1. `query_date_info` - 查询指定日期的节假日信息
2. `query_next_holiday` - 查询下一个节假日（包含倒计时）⭐
3. `query_next_workday` - 查询下一个工作日 ⭐
4. `query_year_holidays` - 查询年度节假日列表
5. `query_holiday_tts` - 获取语音播报文本
6. `batch_query_holidays` - 批量查询
7. `check_workday` - 检查是否工作日 ⭐

**使用的API：**
- 免费节假日API（timor.tech）
- 不限速、不登录、没广告
- 已更新2026年数据
- 支持调休和补班

**核心服务：**
```go
// 判断是否为工作日
func IsWorkday(ctx context.Context, date time.Time) (bool, error)

// 获取下一个工作日（用于schedule）
func GetNextWorkdayForSchedule(ctx context.Context, from time.Time) (time.Time, error)
```

**特性：**
- ✅ 自动缓存（24小时过期）
- ✅ 降级处理（API不可用时的容错）
- ✅ 支持批量查询
- ✅ 支持调休补班

在 `scheduled_tasks` 表中新增以下字段：

```sql
-- 跳过周末（周六日）
skip_weekends BOOLEAN NOT NULL DEFAULT FALSE;
-- 跳过法定节假日
skip_holidays BOOLEAN NOT NULL DEFAULT FALSE;
```

对应的 Go 模型修改：
- 文件：`internal/infrastructure/db/model/scheduled_tasks.gen.go`（自动生成）
- 文件：`internal/infrastructure/db/model/scheduled_tasks_ext.go`（手动扩展）

### 2.2 数据库模型扩展（待实现）

### 2.3 核心功能集成（待实现）

#### 2.3.1 Schedule Service 修改

修改 `internal/application/lark/schedule/service.go`：

1. 在 `CreateTaskRequest` 中添加：
```go
SkipWeekends bool
SkipHolidays bool
```

2. 在 `computeNextRun` 函数中增加工作日判断：

```go
import "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/holiday"

func (s *Service) computeNextRunWithWorkday(task *model.ScheduledTask, now time.Time) (time.Time, error) {
    nextRun, err := computeNextRun(task.CronExpr, task.Timezone, now)
    if err != nil {
        return time.Time{}, err
    }

    // 检查是否需要跳过周末或节假日
    if task.SkipWeekends || task.SkipHolidays {
        // 如果启用了跳过周末，检查是否为周末
        if task.SkipWeekends {
            weekDay := nextRun.Weekday()
            if weekDay == time.Saturday || weekDay == time.Sunday {
                // 跳到下一个工作日
                nextRun, err = holiday.GetNextWorkdayForSchedule(context.Background(), nextRun)
                if err != nil {
                    return time.Time{}, err
                }
            }
        }

        // 如果启用了跳过节假日，检查是否为节假日
        if task.SkipHolidays {
            isWorkday, err := holiday.CheckWorkday(context.Background(), nextRun)
            if err != nil {
                // 降级处理：如果API不可用，仍然执行任务
                logs.L().Warn("Failed to check workday, skip this check",
                    zap.Error(err),
                    zap.Time("date", nextRun))
                return nextRun, nil
            }

            // 如果不是工作日，跳到下一个工作日
            if !isWorkday {
                nextRun, err = holiday.GetNextWorkdayForSchedule(context.Background(), nextRun)
                if err != nil {
                    return time.Time{}, err
                }
            }
        }
    }

    return nextRun, nil
}
```

### 2.4 API 接口扩展（待实现）

#### 2.4.1 工具函数参数扩展

修改 `internal/application/lark/schedule/func_call_tools.go`：

在 `createScheduleArgs` 中添加：
```go
SkipWeekends bool `json:"skip_weekends"`
SkipHolidays bool `json:"skip_holidays"`
```

在 `editScheduleArgs` 中添加：
```go
SkipWeekends *bool `json:"skip_weekends,omitempty"`
SkipHolidays *bool `json:"skip_holidays,omitempty"`
```

#### 2.4.2 CLI 命令扩展

修改 `internal/application/lark/handlers/schedule_handler.go`：

在 `ScheduleCreateArgs` 中添加：
```go
SkipWeekends bool `json:"skip_weekends"`
SkipHolidays bool `json:"skip_holidays"`
```

### 2.5 卡片界面扩展（待实现）

修改 `internal/application/lark/schedule/card_edit.go` 和 `card_view.go`：

在编辑卡片中添加：
- "跳过周末" 复选框
- "跳过节假日" 复选框

在展示卡片中显示：
- 当前任务的工作日设置

## 三、已完成的工作

### 3.1 节假日工具包 ✅

**文件位置：** `internal/infrastructure/holiday/`

**已实现的功能：**
- ✅ 节假日服务（service.go）
  - IsWorkday() - 判断是否为工作日
  - GetDateInfo() - 获取日期详细信息
  - GetNextHoliday() - 获取下一个节假日
  - GetNextWorkday() - 获取下一个工作日
  - GetYearHolidays() - 获取年度节假日列表
  - GetTTS() - 获取语音播报文本
  - BatchGetDateInfo() - 批量查询

- ✅ 7个工具函数（tools.go）
  - query_date_info - 查询日期信息
  - query_next_holiday - 查询下一个节假日 ⭐
  - query_next_workday - 查询下一个工作日 ⭐
  - query_year_holidays - 查询年度节假日
  - query_holiday_tts - 语音播报文本
  - batch_query_holidays - 批量查询
  - check_workday - 检查是否工作日 ⭐

**特性：**
- ✅ 使用免费API（timor.tech）
- ✅ 自动缓存机制（24小时过期）
- ✅ 支持调休补班
- ✅ 降级处理（API不可用时的容错）
- ✅ 批量查询支持

## 四、待实现的工作

### 4.1 数据库模型扩展（必须）

创建文件：`internal/infrastructure/workday/holidays_cn.go`

```go
package workday

// 中国法定节假日数据（示例）
var holidaysCN = map[string][]Holiday{
    "2024": {
        {Date: "2024-01-01", Name: "元旦"},
        {Date: "2024-02-10", Name: "春节"},
        // ... 更多节假日
    },
    // ... 更多年份
}

type Holiday struct {
    Date string // YYYY-MM-DD
    Name string
}
```

### 4.2 Schedule Service 集成（必须）

1. 扩展 ScheduledTask 模型
2. 修改 schedule service 的计算逻辑
3. 集成 holiday.CheckWorkday() 和 holiday.GetNextWorkdayForSchedule()
4. 编写单元测试

### 4.3 API 接口扩展（必须）

1. 扩展 API 参数（skip_weekends, skip_holidays）
2. 扩展 CLI 命令参数
3. 编写集成测试

### 4.4 界面优化（可选）
1. 扩展卡片界面
2. 添加配置入口
3. 完善文档

### 阶段四：高级功能（可选）
1. 实现第三方 API 集成
2. 实现缓存机制
3. 实现自定义节假日配置

## 五、数据库迁移

创建迁移文件：`script/migrations/0XX_add_workday_config_to_scheduled_tasks.sql`

```sql
-- 添加工作日配置字段
ALTER TABLE betago.scheduled_tasks
    ADD COLUMN IF NOT EXISTS skip_weekends BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS skip_holidays BOOLEAN NOT NULL DEFAULT FALSE;

-- 添加注释
COMMENT ON COLUMN betago.scheduled_tasks.skip_weekends IS '是否跳过周末（周六日）';
COMMENT ON COLUMN betago.scheduled_tasks.skip_holidays IS '是否跳过法定节假日';
```

## 六、测试计划

### 6.1 单元测试
- `workday/service_test.go`：测试工作日判断逻辑
- `schedule/service_test.go`：测试带工作日判断的调度逻辑

### 6.2 集成测试
- 创建带工作日限制的 schedule
- 验证执行时间计算正确性
- 验证跳过周末和节假日功能

### 6.3 手动测试场景
1. 创建一个每天执行的 cron 任务，设置跳过周末
2. 创建一个每天执行的 cron 任务，设置跳过节假日
3. 验证在节假日前一天创建的任务执行时间正确

## 七、风险评估

### 7.1 性能风险
- **风险**：频繁的工作日判断可能影响性能
- **缓解**：
  - 实现缓存机制
  - 批量判断工作日
  - 优化计算算法

### 7.2 数据准确性风险
- **风险**：节假日数据可能不准确或过期
- **缓解**：
  - 定期更新本地数据
  - 提供手动更新入口
  - 支持用户自定义

### 7.3 时区问题
- **风险**：不同时区的节假日判断可能有问题
- **缓解**：
  - 明确使用任务的时区进行判断
  - 所有日期比较都基于同一时区

## 八、后续优化

1. **节假日数据自动更新**：定期从权威源同步节假日数据
2. **自定义工作日**：支持用户自定义哪些天是工作日
3. **调休支持**：支持处理调休工作日（如周末补班）
4. **多国家支持**：支持不同国家的节假日规则
5. **可视化配置**：提供日历界面选择工作日

## 九、关键文件清单

### 需要创建的新文件
1. `internal/infrastructure/workday/service.go` - 工作日服务接口
2. `internal/infrastructure/workday/service_impl.go` - 服务实现
3. `internal/infrastructure/workday/holidays_cn.go` - 中国节假日数据
4. `internal/infrastructure/workday/holiday_api.go` - API 获取（可选）
5. `script/migrations/0XX_add_workday_config_to_scheduled_tasks.sql` - 数据库迁移

### 需要修改的现有文件
1. `internal/infrastructure/db/model/scheduled_tasks.gen.go` - 数据模型（自动生成）
2. `internal/infrastructure/db/model/scheduled_tasks_ext.go` - 模型扩展方法
3. `internal/application/lark/schedule/service.go` - 核心服务逻辑
4. `internal/application/lark/schedule/func_call_tools.go` - 工具函数
5. `internal/application/lark/handlers/schedule_handler.go` - CLI 命令处理
6. `internal/application/lark/schedule/card_edit.go` - 编辑卡片
7. `internal/application/lark/schedule/card_view.go` - 展示卡片

## 十、时间估算

- **阶段一（基础架构）**：2-3 小时
- **阶段二（核心功能）**：3-4 小时
- **阶段三（界面优化）**：2-3 小时
- **阶段四（高级功能）**：4-5 小时
- **测试和调试**：2-3 小时

**总计**：13-18 小时

## 十一、实施建议

1. **优先实现基础功能**：先完成静态数据和核心逻辑，确保基本功能可用
2. **渐进式开发**：从简单到复杂，逐步添加高级功能
3. **充分测试**：特别是在节假日前后测试，确保逻辑正确
4. **文档完善**：更新用户文档，说明如何使用新功能
5. **数据维护**：建立定期更新节假日数据的流程
