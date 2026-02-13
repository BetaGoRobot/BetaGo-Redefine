package utils

import (
	"strconv"
	"time"
)

func UTC8Loc() *time.Location {
	return time.FixedZone("UTC+8", 8*60*60)
}

func UTC8Time() time.Time {
	return time.Now().In(UTC8Loc())
}

func Epo2DateZoneMil(epoch int64, loc *time.Location, format string) string {
	return time.Unix(epoch, 0).In(loc).Format(format)
}

func EpoMil2DateStr(epoMil string) string {
	epoMilInt, _ := strconv.ParseInt(epoMil, 10, 64)
	return time.Unix(int64(epoMilInt)/1000, 0).In(UTC8Loc()).Format("2006-01-02 15:04:05")
}
