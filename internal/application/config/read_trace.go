package config

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type readTraceCollectorKey struct{}

type cachedConfigLookup struct {
	Value string
	OK    bool
	Valid bool
}

type configReadObservation struct {
	Key    string
	Source string
	Value  string
	Count  int
}

type featureCheckObservation struct {
	Feature string
	Source  string
	Enabled bool
	Count   int
}

type ReadTraceCollector struct {
	mu sync.Mutex

	configLookupCache map[string]cachedConfigLookup
	configReads       map[string]*configReadObservation
	featureChecks     map[string]*featureCheckObservation

	configReadCount   int
	featureCheckCount int
}

func NewReadTraceCollector() *ReadTraceCollector {
	return &ReadTraceCollector{
		configLookupCache: make(map[string]cachedConfigLookup),
		configReads:       make(map[string]*configReadObservation),
		featureChecks:     make(map[string]*featureCheckObservation),
	}
}

func WithReadTraceCollector(ctx context.Context, collector *ReadTraceCollector) context.Context {
	if ctx == nil || collector == nil {
		return ctx
	}
	return context.WithValue(ctx, readTraceCollectorKey{}, collector)
}

func ReadTraceCollectorFromContext(ctx context.Context) *ReadTraceCollector {
	if ctx == nil {
		return nil
	}
	collector, _ := ctx.Value(readTraceCollectorKey{}).(*ReadTraceCollector)
	return collector
}

func RecordResolvedConfigValue(ctx context.Context, key ConfigKey, value, source string) bool {
	collector := ReadTraceCollectorFromContext(ctx)
	if collector == nil {
		return false
	}

	name := strings.TrimSpace(string(key))
	if name == "" {
		return false
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "unknown"
	}
	value = strings.TrimSpace(value)

	entryKey := name + "|" + source + "|" + value
	collector.mu.Lock()
	defer collector.mu.Unlock()
	entry := collector.configReads[entryKey]
	if entry == nil {
		entry = &configReadObservation{
			Key:    name,
			Source: source,
			Value:  value,
		}
		collector.configReads[entryKey] = entry
	}
	entry.Count++
	collector.configReadCount++
	return true
}

func RecordFeatureCheckResult(ctx context.Context, feature string, enabled bool, source string) bool {
	collector := ReadTraceCollectorFromContext(ctx)
	if collector == nil {
		return false
	}

	feature = strings.TrimSpace(feature)
	if feature == "" {
		return false
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "unknown"
	}

	entryKey := fmt.Sprintf("%s|%t|%s", feature, enabled, source)
	collector.mu.Lock()
	defer collector.mu.Unlock()
	entry := collector.featureChecks[entryKey]
	if entry == nil {
		entry = &featureCheckObservation{
			Feature: feature,
			Source:  source,
			Enabled: enabled,
		}
		collector.featureChecks[entryKey] = entry
	}
	entry.Count++
	collector.featureCheckCount++
	return true
}

func lookupConfigValueFromTraceCache(ctx context.Context, fullKey string) (string, bool, bool) {
	collector := ReadTraceCollectorFromContext(ctx)
	if collector == nil || strings.TrimSpace(fullKey) == "" {
		return "", false, false
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	lookup, ok := collector.configLookupCache[fullKey]
	if !ok || !lookup.Valid {
		return "", false, false
	}
	return lookup.Value, lookup.OK, true
}

func storeConfigValueInTraceCache(ctx context.Context, fullKey, value string, ok bool) bool {
	collector := ReadTraceCollectorFromContext(ctx)
	if collector == nil || strings.TrimSpace(fullKey) == "" {
		return false
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.configLookupCache[fullKey] = cachedConfigLookup{
		Value: value,
		OK:    ok,
		Valid: true,
	}
	return true
}

func FlushReadTrace(ctx context.Context) {
	collector := ReadTraceCollectorFromContext(ctx)
	if collector == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.SpanContext().IsValid() {
		return
	}

	collector.mu.Lock()
	configReadCount := collector.configReadCount
	featureCheckCount := collector.featureCheckCount
	configPreview := buildConfigReadPreview(collector.configReads, 16)
	featurePreview := buildFeatureCheckPreview(collector.featureChecks, 16)
	collector.mu.Unlock()

	if configReadCount > 0 {
		span.SetAttributes(attribute.Int("config.reads.count", configReadCount))
		span.SetAttributes(attribute.StringSlice("config.reads.preview", configPreview))
	}
	if featureCheckCount > 0 {
		span.SetAttributes(attribute.Int("config.feature_checks.count", featureCheckCount))
		span.SetAttributes(attribute.StringSlice("config.feature_checks.preview", featurePreview))
	}
}

func buildConfigReadPreview(entries map[string]*configReadObservation, limit int) []string {
	if len(entries) == 0 {
		return nil
	}
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		items = append(items, fmt.Sprintf("%s=%s source=%s x%d",
			entry.Key,
			otel.PreviewString(entry.Value, 64),
			entry.Source,
			entry.Count,
		))
	}
	sort.Strings(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func buildFeatureCheckPreview(entries map[string]*featureCheckObservation, limit int) []string {
	if len(entries) == 0 {
		return nil
	}
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		items = append(items, fmt.Sprintf("%s=%t source=%s x%d",
			entry.Feature,
			entry.Enabled,
			entry.Source,
			entry.Count,
		))
	}
	sort.Strings(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}
