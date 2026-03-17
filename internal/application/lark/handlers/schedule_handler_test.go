package handlers

import "testing"

func TestScheduleCreateParseCLI(t *testing.T) {
	arg, err := ScheduleCreate.ParseCLI([]string{
		"--name=午休提醒",
		"--type=once",
		"--run_at=2026-03-11T13:00:00+08:00",
		"--message=记得午休",
		"--notify_on_error=true",
	})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.Name != "午休提醒" || arg.Type != "once" || !arg.NotifyOnError {
		t.Fatalf("unexpected args: %+v", arg)
	}
}

func TestScheduleCreateParseCLISupportsBareBoolFlag(t *testing.T) {
	arg, err := ScheduleCreate.ParseCLI([]string{
		"--name=午休提醒",
		"--type=once",
		"--notify_on_error",
		"--notify_result=false",
	})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if !arg.NotifyOnError || arg.NotifyResult {
		t.Fatalf("unexpected bool args: %+v", arg)
	}
}

func TestScheduleManageParseCLI(t *testing.T) {
	arg, err := ScheduleManage.ParseCLI([]string{"--limit=10"})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.Limit != 10 {
		t.Fatalf("unexpected args: %+v", arg)
	}
	if arg.ChatScope != ScheduleChatScopeCurrent {
		t.Fatalf("expected manage to default to current chat scope, got %+v", arg)
	}
}

func TestScheduleQueryParseCLIUsesCreatorOpenIDAlias(t *testing.T) {
	arg, err := ScheduleQuery.ParseCLI([]string{"--status=paused", "--open_id=ou_creator"})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.Status != "paused" || arg.CreatorOpenID != "ou_creator" {
		t.Fatalf("unexpected args: %+v", arg)
	}
	if arg.ChatScope != ScheduleChatScopeCurrent {
		t.Fatalf("expected query to default to current chat scope, got %+v", arg)
	}
}

func TestScheduleListParseCLIIgnoresLegacyCrossChatFlags(t *testing.T) {
	arg, err := ScheduleList.ParseCLI([]string{"--chat_scope=all", "--chat_id=oc_target", "--limit=5"})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.Limit != 5 {
		t.Fatalf("unexpected args: %+v", arg)
	}
}

func TestSchedulePauseParseCLI(t *testing.T) {
	arg, err := SchedulePause.ParseCLI([]string{"--id=task-1"})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.ID != "task-1" {
		t.Fatalf("unexpected id: %+v", arg)
	}
}

func TestScheduleDeleteParseCLIMissingID(t *testing.T) {
	_, err := ScheduleDelete.ParseCLI(nil)
	if err == nil || err.Error() != "usage: /schedule delete --id=task_id" {
		t.Fatalf("unexpected error: %v", err)
	}
}
