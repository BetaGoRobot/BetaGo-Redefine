package modelapi

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type stalledModelConn struct {
	net.Conn
	drop atomic.Bool
}

func (c *stalledModelConn) Write(b []byte) (int, error) {
	if c.drop.Load() {
		return len(b), nil
	}
	return c.Conn.Write(b)
}

type stalledModelListener struct {
	net.Listener
	first atomic.Pointer[stalledModelConn]
	count atomic.Int32
}

func (l *stalledModelListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &stalledModelConn{Conn: c}
	l.first.CompareAndSwap(nil, wrapped)
	l.count.Add(1)
	return wrapped, nil
}

func TestModelTransportRecoversStalledHTTP2Connection(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Error("model transport lost HTTP/2")
		}
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, "actual response")
	}))
	listener := &stalledModelListener{Listener: server.Listener}
	server.Listener = listener
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport := newModelTransport()
	client := &http.Client{Transport: transport, Timeout: 90 * time.Second}
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if transport.HTTP2 == nil || transport.HTTP2.SendPingTimeout <= 0 || transport.HTTP2.PingTimeout <= 0 {
		t.Fatal("model transport has no HTTP/2 connection health checks")
	}
	transport.HTTP2.SendPingTimeout = 40 * time.Millisecond
	transport.HTTP2.PingTimeout = 40 * time.Millisecond
	defer transport.CloseIdleConnections()
	request := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader("synthetic model request"))
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
		return err
	}
	if err := request(); err != nil {
		t.Fatal(err)
	}
	if err := request(); err != nil || listener.count.Load() != 1 {
		t.Fatalf("healthy connection was not reused: connections=%d err=%v", listener.count.Load(), err)
	}
	listener.first.Load().drop.Store(true)
	started := time.Now()
	err := request()
	if errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 800*time.Millisecond {
		t.Fatalf("stalled HTTP/2 connection survived until the request deadline: %v", err)
	}
	if err := request(); err != nil || listener.count.Load() < 2 {
		t.Fatalf("new model request did not recover on a fresh connection: connections=%d err=%v", listener.count.Load(), err)
	}
}

func TestModelTransportHeaderTimeoutDoesNotTruncateStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stalled" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(70 * time.Millisecond)
		io.WriteString(w, "data: last\n\n")
	}))
	defer server.Close()
	transport := newModelTransport()
	client := &http.Client{Transport: transport, Timeout: 90 * time.Second}
	transport.ResponseHeaderTimeout = 30 * time.Millisecond
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/stalled", nil)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("response-header stall was not bounded")
	}
	resp, err := client.Get(server.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(b), "data: last") {
		t.Fatalf("header deadline truncated a healthy stream: %q %v", b, err)
	}
}
