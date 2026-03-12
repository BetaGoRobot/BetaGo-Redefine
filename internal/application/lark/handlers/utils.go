package handlers

import (
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
)

func parseArgs(args ...string) (argsMap map[string]string, input string) {
	argsMap = make(map[string]string)
	for idx, arg := range args {
		if strings.HasPrefix(arg, "--") {
			argKV := strings.Split(arg, "=")
			if len(argKV) > 1 {
				argsMap[strings.TrimPrefix(argKV[0], "--")] = argKV[1]
			} else {
				argsMap[strings.TrimPrefix(argKV[0], "--")] = ""
			}
		} else {
			input = strings.Join(args[idx:], " ")
			break
		}
	}
	return
}

func parseEmptyToolArgs(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil
	}

	args := map[string]any{}
	if err := utils.UnmarshalStringPre(raw, &args); err != nil {
		return err
	}
	return nil
}

func normalizeRFC3339(value string) string {
	if value == "" {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return value
	}
	if t, err := time.ParseInLocation(time.DateTime, value, utils.UTC8Loc()); err == nil {
		return t.Format(time.RFC3339)
	}
	return value
}

func normalizeDateTime(value string) string {
	if value == "" {
		return ""
	}
	if _, err := time.ParseInLocation(time.DateTime, value, utils.UTC8Loc()); err == nil {
		return value
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.In(utils.UTC8Loc()).Format(time.DateTime)
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseOptionalBoolArg(argsMap map[string]string, key string) (bool, bool, error) {
	raw, ok := argsMap[key]
	if !ok {
		return false, false, nil
	}
	if strings.TrimSpace(raw) == "" {
		return true, true, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, true, err
	}
	return parsed, true, nil
}
