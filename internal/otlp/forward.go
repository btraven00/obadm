package otlp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// Forward reads JSONL lines from r and exports each line as an OTLP LogRecord
// to the gRPC endpoint at addr (e.g. "localhost:4317").
// Blocks until r returns EOF or an error, or ctx is cancelled.
func Forward(ctx context.Context, r io.Reader, addr string) error {
	exp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(addr),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("create otlp exporter: %w", err)
	}

	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
	)
	defer provider.Shutdown(ctx) //nolint:errcheck

	logger := provider.Logger("obadm/stream")

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		var rec log.Record
		rec.SetTimestamp(time.Now())
		rec.SetObservedTimestamp(time.Now())
		rec.SetSeverity(log.SeverityInfo)
		rec.SetBody(log.StringValue(line))

		// Extract fields from JSON and attach as attributes.
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err == nil {
			attrs := make([]log.KeyValue, 0, len(m))
			for k, v := range m {
				attrs = append(attrs, log.String(k, fmt.Sprintf("%v", v)))
			}
			rec.AddAttributes(attrs...)
		}

		logger.Emit(ctx, rec)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}
