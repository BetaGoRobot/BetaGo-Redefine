// Package web reads public internet material. It has no group credentials,
// cookies, authority, or access to internal network destinations.
package web

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/net/idna"
)

type Page struct {
	URL, Title, Text     string
	FetchedAt            time.Time
	Truncated, Untrusted bool
}
type Source struct{ URL, Title, Excerpt string }
type SearchResult struct {
	Query, Text string
	Sources     []Source
	RetrievedAt time.Time
	Untrusted   bool
}
type Searcher interface {
	Search(context.Context, string, int) (SearchResult, error)
}
type PageReader interface {
	Read(context.Context, string) (Page, error)
}

type Reader struct {
	lookup func(context.Context, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func NewReader() *Reader {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return &Reader{lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}, dial: d.DialContext}
}

var errRead = errors.New("public webpage unavailable or rejected")
var special = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range special {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// PublicURL checks syntax only. Every fetch additionally resolves and pins all
// DNS answers before dialing, including each redirect destination.
func PublicURL(raw string) bool { _, err := parseURL(raw); return err == nil }
func parseURL(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, errRead
	}
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Opaque != "" || u.Hostname() == "" {
		return nil, errRead
	}
	port := u.Port()
	if port != "" && !(u.Scheme == "http" && port == "80" || u.Scheme == "https" && port == "443") {
		return nil, errRead
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, e := netip.ParseAddr(host); e == nil {
		if !publicAddress(ip) {
			return nil, errRead
		}
	} else {
		host, e = idna.Lookup.ToASCII(host)
		if e != nil || host == "" {
			return nil, errRead
		}
		if !strings.Contains(host, ".") {
			return nil, errRead
		}
		for _, suffix := range []string{".local", ".localhost", ".internal", ".home", ".lan", ".invalid"} {
			if strings.HasSuffix(host, suffix) {
				return nil, errRead
			}
		}
	}
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.Fragment = ""
	return u, nil
}
func (r *Reader) Read(ctx context.Context, raw string) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u, err := parseURL(raw)
	if err != nil || r == nil || r.lookup == nil || r.dial == nil {
		return Page{}, errRead
	}
	for redirects := 0; redirects <= 3; redirects++ {
		ips := []netip.Addr{}
		if ip, e := netip.ParseAddr(u.Hostname()); e == nil {
			ips = append(ips, ip)
		} else {
			ips, err = r.lookup(ctx, u.Hostname())
		}
		if err != nil || len(ips) == 0 || len(ips) > 32 {
			return Page{}, errRead
		}
		for _, ip := range ips {
			if !publicAddress(ip) {
				return Page{}, errRead
			}
		}
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		transport := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 32768, ResponseHeaderTimeout: 8 * time.Second, TLSHandshakeTimeout: 5 * time.Second, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var conn net.Conn
			var e error
			for _, ip := range ips {
				conn, e = r.dial(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					return conn, nil
				}
				if ctx.Err() != nil {
					break
				}
			}
			return nil, e
		}}
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			return Page{}, errRead
		}
		req.Header.Set("User-Agent", "GroupAgentReader/1.0")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain")
		req.Header.Set("Accept-Encoding", "identity")
		resp, e := client.Do(req)
		if e != nil {
			transport.CloseIdleConnections()
			return Page{}, errRead
		}
		if resp.StatusCode == 301 || resp.StatusCode == 302 || resp.StatusCode == 303 || resp.StatusCode == 307 || resp.StatusCode == 308 {
			loc, e := resp.Location()
			resp.Body.Close()
			transport.CloseIdleConnections()
			if e != nil || redirects == 3 {
				return Page{}, errRead
			}
			u, err = parseURL(loc.String())
			if err != nil {
				return Page{}, errRead
			}
			continue
		}
		page, e := decodePage(resp, u.String())
		resp.Body.Close()
		transport.CloseIdleConnections()
		if e != nil {
			return Page{}, errRead
		}
		return page, nil
	}
	return Page{}, errRead
}

func decodePage(resp *http.Response, finalURL string) (Page, error) {
	const maxBytes = 2 << 20
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.ContentLength > maxBytes || resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		return Page{}, errRead
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return Page{}, errRead
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	media, _, err := mime.ParseMediaType(ct)
	if err != nil || media != "text/html" && media != "application/xhtml+xml" && media != "text/plain" {
		return Page{}, errRead
	}
	reader, err := charset.NewReader(strings.NewReader(string(data)), ct)
	if err != nil {
		return Page{}, errRead
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, 6*maxBytes+1))
	if err != nil || len(decoded) > 6*maxBytes {
		return Page{}, errRead
	}
	page := Page{URL: finalURL, FetchedAt: time.Now().UTC(), Untrusted: true}
	if media == "text/plain" {
		page.Text = string(decoded)
	} else {
		doc, err := html.Parse(strings.NewReader(string(decoded)))
		if err != nil {
			return Page{}, errRead
		}
		var text, title strings.Builder
		var walk func(*html.Node, bool)
		walk = func(n *html.Node, inTitle bool) {
			if n.Type == html.ElementNode {
				switch n.Data {
				case "script", "style", "noscript", "svg", "template", "nav", "footer", "form":
					return
				}
				for _, a := range n.Attr {
					if a.Key == "hidden" || a.Key == "aria-hidden" && a.Val == "true" {
						return
					}
				}
				inTitle = inTitle || n.Data == "title"
				if n.Data == "head" {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Data == "title" {
							walk(c, true)
						}
					}
					return
				}
			}
			if n.Type == html.TextNode {
				if inTitle {
					title.WriteString(n.Data)
				} else {
					text.WriteString(n.Data)
					text.WriteByte(' ')
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c, inTitle)
			}
		}
		walk(doc, false)
		page.Title = strings.Join(strings.Fields(title.String()), " ")
		page.Text = text.String()
	}
	page.Text = strings.Join(strings.Fields(page.Text), " ")
	if !utf8.ValidString(page.Text) || strings.TrimSpace(page.Text) == "" {
		return Page{}, errRead
	}
	runes := []rune(page.Text)
	if len(runes) > 12000 {
		page.Text = string(runes[:12000])
		page.Truncated = true
	}
	title := []rune(page.Title)
	if len(title) > 256 {
		page.Title = string(title[:256])
	}
	return page, nil
}
