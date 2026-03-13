package xcommand

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/dlclark/regexp2"
	"go.uber.org/zap"
)

var (
	commandMsgRepattern  = regexp2.MustCompile(`\/(?P<commands>[^--]+)( --)*`, regexp2.RE2)                                                                   // 只校验是不是合法命令
	commandFullRepattern = regexp2.MustCompile(`((@[^ ]+\s+)|^)\/(?P<commands>\w+( )*)+( )*(--(?P<arg_name>\w+)=(?P<arg_value>("[^"]*"|\S+)))*`, regexp2.RE2) // command+参数格式校验
	commandArgRepattern  = regexp2.MustCompile(`--(?P<arg_name>\w+)(=(?P<arg_value>("[^"]*"|\S+)))?`, regexp2.RE2)
)

func GetCommand(ctx context.Context, content string) (commands []string) {
	// 校验合法性
	matched, err := commandFullRepattern.MatchString(content)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetCommand", zap.Error(err))
		return
	}
	if !matched {
		return nil
	}

	match, err := commandMsgRepattern.FindStringMatch(content)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetCommand", zap.Error(err))
		return
	}
	if match == nil {
		return nil
	}
	if match.GroupByName("commands") != nil { // 提取command
		commands = strings.Fields(match.GroupByName("commands").String())

		// 转换args
		argMatch, err := commandArgRepattern.FindStringMatch(content)
		if err != nil {
			logs.L().Ctx(ctx).Error("GetCommand", zap.Error(err))
			return
		}
		if argMatch != nil {
			lastIdx := 0
			for argMatch != nil {
				lastIdx = argMatch.Index + len(argMatch.String()) + 1
				commands = append(commands, ReBuildArgs(
					argMatch.GroupByName("arg_name").String(),
					argMatch.GroupByName("arg_value").String()),
				)
				argMatch, err = commandArgRepattern.FindNextMatch(argMatch)
				if err != nil {
					logs.L().Ctx(ctx).Error("GetCommand", zap.Error(err))
					return
				}
			}
			if lastIdx < len(content) {
				commands = append(commands, content[lastIdx:])
			}
		}
	}

	return
}

func ReBuildArgs(argName, argValue string) string {
	if trimmed := strings.Trim(argValue, "\""); trimmed != "" {
		return strings.Join([]string{"--", argName, "=", trimmed}, "")
	} else {
		return strings.Join([]string{"--", argName}, "")
	}
}
