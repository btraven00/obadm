package otlp_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/btraven00/obadm/internal/otlp"
)

func TestForward_SendsLogRecords(t *testing.T) {
	svc := &mockLogService{}
	addr := startMockOTLP(t, svc)

	input := strings.NewReader(
		`{"event":"start","ts":1}` + "\n" +
			`{"event":"end","ts":2}` + "\n",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := otlp.Forward(ctx, input, addr); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	// Allow batch processor to flush.
	time.Sleep(500 * time.Millisecond)

	svc.mu.Lock()
	total := svc.recordCount
	svc.mu.Unlock()

	if total != 2 {
		t.Errorf("got %d log records, want 2", total)
	}
}

// mockLogService is a minimal in-process OTLP log collector.
type mockLogService struct {
	collectorlogspb.UnimplementedLogsServiceServer
	mu          sync.Mutex
	recordCount int
}

func (m *mockLogService) Export(_ context.Context, req *collectorlogspb.ExportLogsServiceRequest) (*collectorlogspb.ExportLogsServiceResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			m.recordCount += len(sl.GetLogRecords())
		}
	}
	return &collectorlogspb.ExportLogsServiceResponse{}, nil
}

func startMockOTLP(t *testing.T, svc *mockLogService) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	collectorlogspb.RegisterLogsServiceServer(srv, svc)
	t.Cleanup(func() { srv.Stop() })
	go srv.Serve(ln) //nolint:errcheck
	return ln.Addr().String()
}
