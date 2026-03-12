package handlers

import "testing"

func TestReplyAddParseCLIUsesTypedEnums(t *testing.T) {
	arg, err := ReplyAdd.ParseCLI([]string{
		"--word=天气",
		"--type=substr",
		"--reply=今天天气不错",
		"--reply_type=image",
	})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.Type != ReplyMatchTypeSubstr {
		t.Fatalf("unexpected match type: %+v", arg)
	}
	if arg.ReplyType != ReplyContentTypeImage {
		t.Fatalf("unexpected reply type: %+v", arg)
	}
}

func TestTrendParseCLIDefaultsTypedChartType(t *testing.T) {
	arg, err := Trend.ParseCLI(nil)
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.ChartType != TrendChartTypeLine {
		t.Fatalf("expected default line chart type, got: %+v", arg)
	}
}

func TestWordCloudParseCLIDefaultsTypedSort(t *testing.T) {
	arg, err := WordCloud.ParseCLI(nil)
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.Sort != WordCloudSortTypeRelevance {
		t.Fatalf("expected default relevance sort, got: %+v", arg)
	}
}

func TestImageAddParseCLINormalizesTypeAlias(t *testing.T) {
	arg, err := ImageAdd.ParseCLI([]string{"--type=img"})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.Type != ImageAssetTypeImage {
		t.Fatalf("expected image asset type, got: %+v", arg)
	}
}

func TestOneWordParseCLIUsesTypedEnum(t *testing.T) {
	arg, err := OneWord.ParseCLI([]string{"--type=诗词"})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.Type != OneWordTypePoetry {
		t.Fatalf("unexpected oneword type: %+v", arg)
	}
}

func TestDebugCardParseCLIUsesTypedSpecEnum(t *testing.T) {
	arg, err := DebugCard.ParseCLI([]string{"--spec=config"})
	if err != nil {
		t.Fatalf("ParseCLI() error = %v", err)
	}
	if arg.Spec != DebugCardSpec("config") {
		t.Fatalf("unexpected debug card spec: %+v", arg)
	}
}
