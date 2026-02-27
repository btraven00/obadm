package otlp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

// Forward reads OTLP JSON lines from r and forwards each to the appropriate
// OTLP gRPC service at addr. Each line must be a JSON-serialized OTLP payload
// with a top-level "resourceSpans", "resourceLogs", or "resourceMetrics" key.
// Blocks until r returns EOF, an error, or ctx is cancelled.
func Forward(ctx context.Context, r io.Reader, addr string) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}
	defer conn.Close()

	traceSvc := collectortracev1.NewTraceServiceClient(conn)
	logSvc := collectorlogsv1.NewLogsServiceClient(conn)
	metricsSvc := collectormetricsv1.NewMetricsServiceClient(conn)

	unmarshaler := protojson.UnmarshalOptions{DiscardUnknown: true}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		if err := dispatchLine(ctx, line, unmarshaler, traceSvc, logSvc, metricsSvc); err != nil {
			log.Printf("otlp: %v", err)
		}
	}

	return scanner.Err()
}

// peek is used to identify the payload type from the top-level JSON key.
type peek struct {
	ResourceSpans   json.RawMessage `json:"resourceSpans"`
	ResourceLogs    json.RawMessage `json:"resourceLogs"`
	ResourceMetrics json.RawMessage `json:"resourceMetrics"`
}

func dispatchLine(
	ctx context.Context,
	line []byte,
	u protojson.UnmarshalOptions,
	traceSvc collectortracev1.TraceServiceClient,
	logSvc collectorlogsv1.LogsServiceClient,
	metricsSvc collectormetricsv1.MetricsServiceClient,
) error {
	var p peek
	if err := json.Unmarshal(line, &p); err != nil {
		return fmt.Errorf("json peek: %w", err)
	}

	switch {
	case p.ResourceSpans != nil:
		var td tracev1.TracesData
		if err := u.Unmarshal(line, &td); err != nil {
			return fmt.Errorf("unmarshal traces: %w", err)
		}
		_, err := traceSvc.Export(ctx, &collectortracev1.ExportTraceServiceRequest{
			ResourceSpans: td.ResourceSpans,
		})
		return err

	case p.ResourceLogs != nil:
		var ld logsv1.LogsData
		if err := u.Unmarshal(line, &ld); err != nil {
			return fmt.Errorf("unmarshal logs: %w", err)
		}
		_, err := logSvc.Export(ctx, &collectorlogsv1.ExportLogsServiceRequest{
			ResourceLogs: ld.ResourceLogs,
		})
		return err

	case p.ResourceMetrics != nil:
		var md metricsv1.MetricsData
		if err := u.Unmarshal(line, &md); err != nil {
			return fmt.Errorf("unmarshal metrics: %w", err)
		}
		_, err := metricsSvc.Export(ctx, &collectormetricsv1.ExportMetricsServiceRequest{
			ResourceMetrics: md.ResourceMetrics,
		})
		return err

	default:
		return fmt.Errorf("unrecognized payload (no resourceSpans/resourceLogs/resourceMetrics)")
	}
}
