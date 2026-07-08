package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// GenUUIDStr builds a de-dup UUID for a seed within a 2-minute bucket.
//
// When the bucketed seed exceeds length we must NOT prefix-truncate: callers
// commonly build seeds as "<messageID><suffix>", and a real Lark message ID is
// already ~43 chars, so naive truncation drops the suffix and collapses
// distinct seeds (e.g. per-order split-order reply cards) into one UUID, which
// makes Lark silently dedup all but the first message. Hash instead so the full
// seed decides uniqueness while staying within the 2-minute bucket.
func GenUUIDStr(str string, length int) string {
	st := strconv.Itoa(int(time.Now().Truncate(time.Minute * 2).Unix()))
	str = st + str
	if len(str) > length {
		sum := sha256.Sum256([]byte(str))
		hexed := hex.EncodeToString(sum[:])
		if length < len(hexed) {
			return hexed[:length]
		}
		return hexed
	}
	return str
}

func GenUUIDCode(srcKey, specificKey string, length int) string {
	// 重点是分发的时候，是某个消息（srcKey），只会触发一次（specificKey）
	res := srcKey + specificKey
	if len(res) > length {
		res = res[:length]
	}
	return res
}
