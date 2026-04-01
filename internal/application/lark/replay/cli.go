package replay

import (
	"encoding/json"
	"flag"
	"fmt"
	"strconv"
	"strings"
)

type CLIArgs struct {
	ChatID         string
	MessageID      string
	JSON           bool
	OutputPath     string
	LiveModel      bool
	HistoryLimit   *int
	ProfileLimit   *int
	DisableHistory bool
	DisableProfile bool
}

type ReplayTUIArgs struct {
	Days      int
	Limit     int
	LiveModel bool
	OutputDir string
}

func ParseCLIArgs(args []string) (CLIArgs, error) {
	fs := flag.NewFlagSet("replay intent", flag.ContinueOnError)

	var cliArgs CLIArgs
	var historyLimit int
	var profileLimit int
	var historySet bool
	var profileSet bool

	fs.StringVar(&cliArgs.ChatID, "chat-id", "", "target chat_id")
	fs.StringVar(&cliArgs.MessageID, "message-id", "", "target message_id")
	fs.BoolVar(&cliArgs.JSON, "json", false, "render JSON output")
	fs.StringVar(&cliArgs.OutputPath, "output", "", "write output to path")
	fs.BoolVar(&cliArgs.LiveModel, "live-model", false, "call the live intent model")
	fs.Func("history-limit", "override history limit", func(raw string) error {
		value, err := parsePositiveOrZeroInt(raw)
		if err != nil {
			return err
		}
		historyLimit = value
		historySet = true
		return nil
	})
	fs.Func("profile-limit", "override profile limit", func(raw string) error {
		value, err := parsePositiveOrZeroInt(raw)
		if err != nil {
			return err
		}
		profileLimit = value
		profileSet = true
		return nil
	})
	fs.BoolVar(&cliArgs.DisableHistory, "disable-history", false, "disable history augmentation")
	fs.BoolVar(&cliArgs.DisableProfile, "disable-profile", false, "disable profile augmentation")

	if err := fs.Parse(args); err != nil {
		return CLIArgs{}, err
	}

	cliArgs.ChatID = strings.TrimSpace(cliArgs.ChatID)
	cliArgs.MessageID = strings.TrimSpace(cliArgs.MessageID)
	cliArgs.OutputPath = strings.TrimSpace(cliArgs.OutputPath)
	if cliArgs.ChatID == "" {
		return CLIArgs{}, fmt.Errorf("chat-id is required")
	}
	if cliArgs.MessageID == "" {
		return CLIArgs{}, fmt.Errorf("message-id is required")
	}
	if historySet {
		cliArgs.HistoryLimit = intPtr(historyLimit)
	}
	if profileSet {
		cliArgs.ProfileLimit = intPtr(profileLimit)
	}
	return cliArgs, nil
}

func (a CLIArgs) RunOptions() ReplayRunOptions {
	return ReplayRunOptions{
		ReplayBuildOptions: ReplayBuildOptions{
			HistoryLimit:   a.HistoryLimit,
			ProfileLimit:   a.ProfileLimit,
			DisableHistory: a.DisableHistory,
			DisableProfile: a.DisableProfile,
		},
		LiveModel: a.LiveModel,
	}
}

func ParseReplayTUIArgs(args []string) (ReplayTUIArgs, error) {
	fs := flag.NewFlagSet("replay tui", flag.ContinueOnError)

	var cliArgs ReplayTUIArgs
	fs.IntVar(&cliArgs.Days, "days", 7, "time window in days")
	fs.IntVar(&cliArgs.Limit, "limit", 20, "max sample count")
	fs.BoolVar(&cliArgs.LiveModel, "live-model", false, "call the live model during replay")
	fs.StringVar(&cliArgs.OutputDir, "output-dir", "", "artifact root directory")

	if err := fs.Parse(args); err != nil {
		return ReplayTUIArgs{}, err
	}
	if cliArgs.Days <= 0 {
		return ReplayTUIArgs{}, fmt.Errorf("days must be > 0")
	}
	if cliArgs.Limit <= 0 {
		return ReplayTUIArgs{}, fmt.Errorf("limit must be > 0")
	}
	cliArgs.OutputDir = strings.TrimSpace(cliArgs.OutputDir)
	return cliArgs, nil
}

func RenderCLIOutput(report ReplayReport, renderJSON bool) ([]byte, error) {
	if renderJSON {
		return json.MarshalIndent(report, "", "  ")
	}
	return []byte(report.RenderText()), nil
}

func parsePositiveOrZeroInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty integer value")
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse int %q: %w", raw, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("value must be >= 0")
	}
	return value, nil
}
