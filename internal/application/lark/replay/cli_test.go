package replay

import "testing"

func TestParseCLIArgsAcceptsReplayIntentFlags(t *testing.T) {
	args, err := ParseCLIArgs([]string{
		"--chat-id", "oc_chat",
		"--message-id", "om_target",
		"--json",
		"--output", "/tmp/replay.json",
		"--live-model",
		"--history-limit", "6",
		"--profile-limit", "3",
		"--disable-history",
	})
	if err != nil {
		t.Fatalf("ParseCLIArgs() error = %v", err)
	}
	if args.ChatID != "oc_chat" || args.MessageID != "om_target" {
		t.Fatalf("unexpected target args: %+v", args)
	}
	if !args.JSON || args.OutputPath != "/tmp/replay.json" || !args.LiveModel {
		t.Fatalf("unexpected output flags: %+v", args)
	}
	if args.HistoryLimit == nil || *args.HistoryLimit != 6 {
		t.Fatalf("HistoryLimit = %v, want 6", args.HistoryLimit)
	}
	if args.ProfileLimit == nil || *args.ProfileLimit != 3 {
		t.Fatalf("ProfileLimit = %v, want 3", args.ProfileLimit)
	}
	if !args.DisableHistory || args.DisableProfile {
		t.Fatalf("unexpected disable flags: %+v", args)
	}
}

func TestParseCLIArgsRequiresChatIDAndMessageID(t *testing.T) {
	if _, err := ParseCLIArgs([]string{"--chat-id", "oc_chat"}); err == nil {
		t.Fatalf("ParseCLIArgs() error = nil, want missing message-id error")
	}
	if _, err := ParseCLIArgs([]string{"--message-id", "om_target"}); err == nil {
		t.Fatalf("ParseCLIArgs() error = nil, want missing chat-id error")
	}
}

func TestParseReplayTUIArgsAcceptsFlags(t *testing.T) {
	args, err := ParseReplayTUIArgs([]string{
		"--days", "5",
		"--limit", "12",
		"--live-model",
		"--output-dir", "/tmp/replay-batches",
	})
	if err != nil {
		t.Fatalf("ParseReplayTUIArgs() error = %v", err)
	}
	if args.Days != 5 || args.Limit != 12 {
		t.Fatalf("unexpected range args: %+v", args)
	}
	if !args.LiveModel || args.OutputDir != "/tmp/replay-batches" {
		t.Fatalf("unexpected replay tui args: %+v", args)
	}
}

func TestParseReplayTUIArgsUsesDefaults(t *testing.T) {
	args, err := ParseReplayTUIArgs(nil)
	if err != nil {
		t.Fatalf("ParseReplayTUIArgs() error = %v", err)
	}
	if args.Days != 7 || args.Limit != 20 {
		t.Fatalf("defaults = %+v, want days=7 limit=20", args)
	}
}
