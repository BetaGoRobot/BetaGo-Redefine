package modelapi

import (
	"net"
	"net/http"
	"time"
)

// Each model endpoint owns its pool. Detect a silent HTTP/2 peer before TCP's
// retransmission backoff can consume successive model request budgets.
func newModelTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		HTTP2: &http.HTTP2Config{
			SendPingTimeout:  15 * time.Second,
			PingTimeout:      5 * time.Second,
			WriteByteTimeout: 5 * time.Second,
		},
	}
}
