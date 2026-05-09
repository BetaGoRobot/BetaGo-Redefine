package xhandler

import (
	"fmt"
	stdlog "log"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/VictoriaMetrics/metrics"
)

var metricsEnabled bool

func init() {
	metrics.ExposeMetadata(true)
}

// InitMetrics enables VictoriaMetrics push mode. If pushURL is empty, metrics
// recording becomes a no-op (noop), so callers incur negligible overhead.
func InitMetrics(pushURL string, pushInterval time.Duration, instance string) {
	if pushURL == "" {
		stdlog.Printf("[WARN] vm metrics disabled: push_url is empty, falling back to noop")
		return
	}
	if pushInterval <= 0 {
		pushInterval = 10 * time.Second
	}
	extraLabels := ""
	if instance != "" {
		extraLabels = fmt.Sprintf(`instance=%q`, instance)
	}
	metrics.InitPush(pushURL, pushInterval, extraLabels, true)
	metricsEnabled = true
}

// RecordStageExecution records metrics for a single stage execution.
// If InitMetrics was not called (or called with empty pushURL), this is a no-op.
func RecordStageExecution(stageName, chatName string, skipped bool, profile botidentity.Profile, startTime time.Time) {
	if !metricsEnabled {
		return
	}

	skippedStr := "false"
	if skipped {
		skippedStr = "true"
	}
	counterName := fmt.Sprintf(`betago_stage_execution_total{stage=%q,chat_name=%q,skipped=%q,bot_name=%q}`, stageName, chatName, skippedStr, profile.BotName)
	metrics.GetOrCreateCounter(counterName).Inc()

	histogramName := fmt.Sprintf(`betago_stage_duration_seconds{stage=%q,chat_name=%q,skipped=%q,bot_name=%q}`, stageName, chatName, skippedStr, profile.BotName)
	metrics.GetOrCreatePrometheusHistogram(histogramName).UpdateDuration(startTime)
}
