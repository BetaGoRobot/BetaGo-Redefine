package main

import (
	"context"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/holiday"
)

func main() {
	ctx := context.Background()

	// 示例1: 检查今天是否为工作日
	today := time.Now()
	isWorkday, err := holiday.IsWorkdayCheck(ctx, today)
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}
	fmt.Printf("今天(%s)是工作日吗？ %v\n", today.Format("2006-01-02"), isWorkday)

	// 示例2: 获取下一个节假日
	svc := holiday.GetService()
	nextHoliday, err := svc.GetNextHoliday(ctx, "")
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}
	fmt.Printf("\n下一个节假日: %s\n", nextHoliday.Holiday.Name)
	fmt.Printf("日期: %s\n", nextHoliday.Holiday.Date)
	fmt.Printf("距离: %d 天\n", nextHoliday.Holiday.Rest)

	// 示例3: 获取下一个工作日
	nextWorkday, err := svc.GetNextWorkday(ctx, "")
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}
	fmt.Printf("\n下一个工作日: %s\n", nextWorkday.Workday.Date)
	fmt.Printf("日期: %s\n", nextWorkday.Workday.Date)
	fmt.Printf("距离: %d 天\n", nextWorkday.Workday.Rest)

	// 示例4: 查询年度节假日
	yearHolidays, err := svc.GetYearHolidays(ctx, "2026")
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}
	fmt.Printf("\n2026年节假日列表:\n")
	count := 0
	for date, h := range yearHolidays.Holiday {
		if h.Holiday {
			count++
			fmt.Printf("%d. %s - %s\n", count, h.Name, date)
		}
	}
}
