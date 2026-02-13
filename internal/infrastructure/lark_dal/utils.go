package lark_dal

func GenUUIDCode(srcKey, specificKey string, length int) string {
	// 重点是分发的时候，是某个消息（srcKey），只会触发一次（specificKey）
	res := srcKey + specificKey
	if len(res) > length {
		res = res[:length]
	}
	return res
}
