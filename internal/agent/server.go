package agent

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"os"
)

// Serve accepts connections on ln and streams lines from filePath to each client.
// Reads from the beginning of the file and follows new content as it is written.
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

	f, err := os.Open(filePath)
	if err != nil {
		log.Printf("agent: open %s: %v", filePath, err)
		return
	}
	defer f.Close()

	w := bufio.NewWriter(conn)
	r := bufio.NewReader(f)
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
