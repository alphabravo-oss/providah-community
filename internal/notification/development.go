package notification

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"
)

const DevelopmentWebhook = "http://webhookie:8080/hooks/generic/default"

func DevelopmentOrigin(origin string) bool {
	u, e := url.Parse(origin)
	return e == nil && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}
func captureSMTP(s *SMTP) bool {
	return s != nil && s.Host == "mailpit" && s.Port == 1025 && s.Username == "" && s.Password == "" && Address(s.Sender)
}
func ValidateDevelopment(kind, endpoint string, secret Secret) error {
	if kind == "webhook" && endpoint == DevelopmentWebhook {
		return Validate(kind, "https://capture.example/hooks", secret)
	}
	if kind == "email" && Address(endpoint) && captureSMTP(secret.SMTP) {
		return nil
	}
	return Validate(kind, endpoint, secret)
}
func SendDevelopment(ctx context.Context, m Message) (int, error) {
	return send(ctx, m, DevelopmentOrigin(m.Origin))
}
func captureClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", "webhookie:8080")
	}, MaxResponseHeaderBytes: 16 << 10, ResponseHeaderTimeout: 10 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
