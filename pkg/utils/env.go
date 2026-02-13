package utils

import "os"

func IsDevChan() bool {
	if os.Getenv("IS_DEV") != "" {
		return true
	}
	return false
}
