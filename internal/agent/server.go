package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"os"
)

// resumeMsg is the handshake the client sends before the agent starts streaming.
type resumeMsg struct {
	ResumeLine int64 `json:"resume_line"`
}

// Serve accepts connections on ln and streams lines from filePath to each client.
// Each client must first send {"resume_line": N}; the agent skips N lines then
// streams from line N+1 onward, following new content as it is written.
func Serve(ctx context.Context, filePath string, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go handleConn(ctx, conn, filePath)
	}
}

func handleConn(ctx context.Context, conn net.Conn, filePath string) {
	defer conn.Close()

	// Read the resume handshake from the client.
	var msg resumeMsg
	if err := json.NewDecoder(conn).Decode(&msg); err != nil {
		log.Printf("agent: read resume msg: %v", err)
		return
	}

	// Wait for the file to appear — it may not exist yet if the benchmark
	// hasn't started writing.
	var f *os.File
	for {
		var err error
		f, err = os.Open(filePath)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			log.Printf("agent: open %s: %v", filePath, err)
			return
		}
		log.Printf("agent: waiting for %s...", filePath)
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	defer f.Close()

	w := bufio.NewWriter(conn)
	r := bufio.NewReader(f)

	// Skip msg.ResumeLine complete lines.
	var skipped int64
	for skipped < msg.ResumeLine {
		_, err := r.ReadString('\n')
		if err == io.EOF {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if err != nil {
			log.Printf("agent: skip line: %v", err)
			return
		}
		skipped++
	}

	var pending strings.Builder

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := r.ReadString('\n')
		pending.WriteString(line)

		if err == io.EOF {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if err != nil {
			log.Printf("agent: read %s: %v", filePath, err)
			return
		}

		full := strings.TrimRight(pending.String(), "\r\n")
		pending.Reset()
		if full == "" {
			continue
		}

		if _, err := w.WriteString(full + "\n"); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}
