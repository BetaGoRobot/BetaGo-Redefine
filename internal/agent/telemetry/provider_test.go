package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	logpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

type collectorData struct {
	sync.Mutex
	traces  []*tracepb.ExportTraceServiceRequest
	logs    []*logpb.ExportLogsServiceRequest
	metrics []*metricpb.ExportMetricsServiceRequest
}
type traceSink struct {
	tracepb.UnimplementedTraceServiceServer
	data *collectorData
}

func (s *traceSink) Export(_ context.Context, r *tracepb.ExportTraceServiceRequest) (*tracepb.ExportTraceServiceResponse, error) {
	s.data.Lock()
	defer s.data.Unlock()
	s.data.traces = append(s.data.traces, r)
	return &tracepb.ExportTraceServiceResponse{}, nil
}

type logSink struct {
	logpb.UnimplementedLogsServiceServer
	data *collectorData
}

func (s *logSink) Export(_ context.Context, r *logpb.ExportLogsServiceRequest) (*logpb.ExportLogsServiceResponse, error) {
	s.data.Lock()
	defer s.data.Unlock()
	s.data.logs = append(s.data.logs, r)
	return &logpb.ExportLogsServiceResponse{}, nil
}

type metricSink struct {
	metricpb.UnimplementedMetricsServiceServer
	data *collectorData
}

func (s *metricSink) Export(_ context.Context, r *metricpb.ExportMetricsServiceRequest) (*metricpb.ExportMetricsServiceResponse, error) {
	s.data.Lock()
	defer s.data.Unlock()
	s.data.metrics = append(s.data.metrics, r)
	return &metricpb.ExportMetricsServiceResponse{}, nil
}

func TestAllSignalsExportAndStdoutShareTraceWithReadableBusinessPayload(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	data := &collectorData{}
	server := grpc.NewServer()
	tracepb.RegisterTraceServiceServer(server, &traceSink{data: data})
	logpb.RegisterLogsServiceServer(server, &logSink{data: data})
	metricpb.RegisterMetricsServiceServer(server, &metricSink{data: data})
	go server.Serve(listener)
	defer server.Stop()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+listener.Addr().String())
	t.Setenv("OTEL_SERVICE_NAME", "betago-agent-next-test")
	oldLog, oldTrace, oldMeter, oldPropagation, oldError := slog.Default(), otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator(), otel.GetErrorHandler()
	defer func() {
		slog.SetDefault(oldLog)
		otel.SetTracerProvider(oldTrace)
		otel.SetMeterProvider(oldMeter)
		otel.SetTextMapPropagator(oldPropagation)
		otel.SetErrorHandler(oldError)
	}()
	var output bytes.Buffer
	stop, err := initialize(context.Background(), "agent", &output)
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := Start(Context(context.Background(), "message_id", "message-test", "task_id", "task-test"), "agent.synthetic_verification")
	traceID := span.SpanContext().TraceID().String()
	Error(ctx, "agent.synthetic_failure", Wrap("model.response", "provider_http_rejected", errors.New("secret-canary")), "api_key", "secret-canary", "content", "readable-business-content", "http_status", 429)
	End(ctx, span, nil)
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = stop(shutdown); err != nil {
		t.Fatal(err)
	}
	data.Lock()
	defer data.Unlock()
	if len(data.traces) == 0 || len(data.logs) == 0 || len(data.metrics) == 0 {
		t.Fatalf("missing exported signals: traces=%d logs=%d metrics=%d", len(data.traces), len(data.logs), len(data.metrics))
	}
	serialized := output.String()
	if !strings.Contains(serialized, traceID) || !strings.Contains(serialized, `"service.name":"betago-agent-next-test"`) || !strings.Contains(serialized, `"service.namespace":"betago"`) {
		t.Fatal("stdout missing trace correlation")
	}
	traceLogFound := false
	resourceNamespaceFound, logLevelFound := false, false
	for _, request := range data.logs {
		body, _ := protojson.Marshal(request)
		serialized += string(body)
		for _, resource := range request.ResourceLogs {
			identity := map[string]string{}
			for _, a := range resource.Resource.Attributes {
				identity[a.Key] = a.Value.GetStringValue()
				if a.Key == "service.namespace" && a.Value.GetStringValue() == "betago" {
					resourceNamespaceFound = true
				}
			}
			if identity["service.name"] != identity["service_name"] || identity["service.namespace"] != identity["service_namespace"] || identity["service_namespace"] != "betago" {
				t.Fatal("OTLP resource does not match existing correlation field names")
			}
			for _, scope := range resource.ScopeLogs {
				for _, record := range scope.LogRecords {
					for _, a := range record.Attributes {
						if a.Key == "level" && a.Value.GetStringValue() == "ERROR" {
							logLevelFound = true
						}
					}
					if len(record.TraceId) > 0 {
						traceLogFound = true
					}
				}
			}
		}
	}
	for _, request := range data.traces {
		body, _ := protojson.Marshal(request)
		serialized += string(body)
	}
	if !traceLogFound || !resourceNamespaceFound || !logLevelFound || !strings.Contains(serialized, "readable-business-content") || strings.Contains(serialized, "secret-canary") {
		t.Fatal("missing OTLP log trace association or disclosed private payload")
	}
}
