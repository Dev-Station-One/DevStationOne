package mail

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSend_Disabled(t *testing.T) {
	if err := Send(Config{}, "user@example.com", "subject", "body"); err == nil {
		t.Fatal("expected error when SMTP is not configured")
	}
}

func TestSend_RejectsHeaderInjection(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 25, From: "noreply@example.com"}
	if err := Send(cfg, "user@example.com\r\nBcc: evil@example.com", "subject", "body"); err == nil {
		t.Fatal("expected error for CRLF in To header")
	}
	if err := Send(cfg, "user@example.com", "subject\r\nBcc: evil@example.com", "body"); err != nil && !strings.Contains(err.Error(), "invalid header value") {
		t.Fatalf("expected error for CRLF in Subject header, got: %v", err)
	}
}

// fakeSMTP accepts one connection, walks the client through a minimal
// command exchange, and returns the DATA payload verbatim.
func fakeSMTP(t *testing.T) (addr string, dataCh <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		send := func(s string) { conn.Write([]byte(s)) }
		send("220 test ESMTP\r\n")
		var msg []byte
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				send("250-test\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(cmd, "AUTH PLAIN"):
				send("235 ok\r\n")
			case strings.HasPrefix(cmd, "MAIL FROM"):
				send("250 ok\r\n")
			case strings.HasPrefix(cmd, "RCPT TO"):
				send("250 ok\r\n")
			case strings.HasPrefix(cmd, "DATA"):
				send("354 go\r\n")
				for {
					ln2, err := r.ReadString('\n')
					if err != nil {
						return
					}
					msg = append(msg, []byte(ln2)...)
					if ln2 == ".\r\n" {
						break
					}
				}
				ch <- string(msg)
				send("250 accepted\r\n")
			case strings.HasPrefix(cmd, "QUIT"):
				send("221 bye\r\n")
				return
			}
		}
	}()
	return ln.Addr().String(), ch
}

func TestSend_IncludesMessageStreamHeader(t *testing.T) {
	addr, dataCh := fakeSMTP(t)
	host, port, _ := strings.Cut(addr, ":")
	portNum, _ := strconv.Atoi(port)
	cfg := Config{Host: host, Port: portNum, Username: "u", Password: "p", From: "noreply@example.com", Stream: "devstationone"}
	if err := Send(cfg, "user@example.com", "subject", "body"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case msg := <-dataCh:
		if !strings.Contains(msg, "X-PM-Message-Stream: devstationone\r\n") {
			t.Fatalf("message missing stream header:\n%s", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no DATA payload received")
	}
}
