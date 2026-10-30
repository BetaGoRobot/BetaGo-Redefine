package ark_dal

import (
	"net"
	"net/http"
	"time"
)

// Use a dedicated pool so a silent upstream connection cannot indefinitely
// hold model requests. HTTP/2 pings close unresponsive active connections;
// response-header and request budgets also cover a responsive but stalled peer.
func newArkHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
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
		},
	}
}
