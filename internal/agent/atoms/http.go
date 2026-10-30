package atoms

import (
	"fmt"
	"io"
	"net/http"
)

func identityHeaders(in Invocation) map[string]string {
	// Configuration and injected identity must use identical canonical keys;
	// otherwise Header.Set can choose between case aliases in map iteration.
	return cloneHeaders(map[string]string{"X-Agent-Tenant-ID": in.Task.Scope.TenantID, "X-Agent-App-ID": in.Task.Scope.AppID, "X-Agent-Chat-ID": in.Task.Scope.ChatID, "X-Agent-Visibility": in.Task.Scope.Visibility, "X-Agent-Task-ID": in.Task.ID, "X-Agent-Revision": fmt.Sprint(in.Task.Revision), "X-Agent-Actor-ID": in.ActorID, "X-Agent-Requester-ID": in.Task.RequesterID, "X-Agent-Execution-Key": in.ExecutionKey})
}

type boundedTransport struct{}

func (boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r, e := http.DefaultTransport.RoundTrip(req)
	if e != nil {
		return nil, e
	}
	const limit = 1 << 20
	if r.ContentLength > limit {
		r.Body.Close()
		return nil, fmt.Errorf("MCP response exceeds byte budget")
	}
	r.Body = &boundedBody{ReadCloser: r.Body, remaining: limit}
	return r, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, e := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return 0, fmt.Errorf("MCP response exceeds byte budget")
	}
	return n, e
}
