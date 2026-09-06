// Package egress provides an HTTPS-only, exact-host dependency proxy.
package egress

import (
	"context"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func ValidHost(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`).MatchString(label) {
			return false
		}
	}
	return true
}

type Proxy struct {
	Hosts []string
	Slots chan struct{}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, port, e := net.SplitHostPort(r.RequestURI)
	allowed := false
	for _, h := range p.Hosts {
		allowed = allowed || h == host
	}
	if e != nil || r.Method != http.MethodConnect || port != "443" || !allowed || r.Host != r.RequestURI || !ValidHost(host) {
		http.Error(w, "Destination blocked", http.StatusForbidden)
		return
	}
	select {
	case p.Slots <- struct{}{}:
		defer func() { <-p.Slots }()
	default:
		http.Error(w, "Proxy busy", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	upstream, e := notification.DialPublic(ctx, "tcp", net.JoinHostPort(host, port))
	if e != nil {
		http.Error(w, "Destination unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()
	hijack, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Tunnel unavailable", 500)
		return
	}
	client, rw, e := hijack.Hijack()
	if e != nil {
		return
	}
	defer func() { _ = client.Close() }()
	deadline := time.Now().Add(2 * time.Minute)
	if err := client.SetDeadline(deadline); err != nil {
		return
	}
	if err := upstream.SetDeadline(deadline); err != nil {
		return
	}
	if _, e = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); e != nil {
		return
	}
	if e = rw.Flush(); e != nil {
		return
	}
	done := make(chan struct{}, 1)
	go func() { _, _ = io.Copy(upstream, io.LimitReader(rw, 64<<20)); _ = upstream.Close(); done <- struct{}{} }()
	_, _ = io.Copy(client, io.LimitReader(upstream, 64<<20))
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

// Relay makes the protected Unix-socket proxy usable by ordinary HTTPS_PROXY clients.
// The validation container itself remains on --network=none.
func Relay(ctx context.Context, socket string) (net.Listener, error) {
	listener, e := net.Listen("tcp", "127.0.0.1:8080")
	if e != nil {
		return nil, e
	}
	go func() { <-ctx.Done(); _ = listener.Close() }()
	go func() {
		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer func() { _ = client.Close() }()
				remote, e := (&net.Dialer{}).DialContext(ctx, "unix", socket)
				if e != nil {
					return
				}
				defer func() { _ = remote.Close() }()
				deadline := time.Now().Add(2 * time.Minute)
				if err := client.SetDeadline(deadline); err != nil {
					return
				}
				if err := remote.SetDeadline(deadline); err != nil {
					return
				}
				done := make(chan struct{}, 1)
				go func() { _, _ = io.Copy(remote, client); _ = remote.Close(); done <- struct{}{} }()
				_, _ = io.Copy(client, remote)
				_ = client.Close()
				_ = remote.Close()
				<-done
			}()
		}
	}()
	return listener, nil
}
