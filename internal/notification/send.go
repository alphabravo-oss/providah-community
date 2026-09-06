package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

type SMTP struct {
	Host                       string
	Port                       int32
	Username, Password, Sender string
}
type Secret struct {
	SigningKey string
	SMTP       *SMTP
}
type Challenge struct{ Code, SenderCode string }
type Message struct {
	Kind, Endpoint, EventID, AttemptID, Origin string
	Payload                                    []byte
	Secret                                     Secret
	Challenge                                  Challenge
}

func Address(value string) bool {
	a, err := mail.ParseAddress(value)
	return err == nil && a.Address == value && !strings.ContainsAny(value, "\r\n") && len(value) <= 254
}
func Validate(kind, endpoint string, secret Secret) error {
	if kind == "webhook" {
		if err := ValidateHTTPS(endpoint); err != nil {
			return err
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret.SigningKey, "whsec_"))
		if err != nil || len(key) < 32 || len(key) > 64 {
			return errors.New("Use a Standard Webhooks secret containing 32–64 base64-encoded random bytes.")
		}
		return nil
	}
	if kind != "email" || !Address(endpoint) {
		return errors.New("Use a single email address.")
	}
	s := secret.SMTP
	if s == nil || !Address(s.Sender) || s.Host == "" || strings.ContainsAny(s.Host, " /\\:@\r\n") || (s.Port != 465 && s.Port != 587) || (s.Username == "") != (s.Password == "") {
		return errors.New("Configure an SMTP host, port 465 or 587, sender address, and matching authentication fields.")
	}
	if ip, err := netip.ParseAddr(s.Host); err == nil && !publicIP(ip) {
		return errors.New("This network destination is blocked.")
	}
	return nil
}

var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("168.63.129.16/32"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range blocked {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// Resolve on every new connection, reject mixed public/private answers, and dial
// only the checked literal address. Neither proxies nor a second DNS lookup apply.
func DialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	return dialChecked(ctx, network, address, "")
}
func dialChecked(ctx context.Context, network, address, controlHost string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("Destination resolution failed.")
	}
	var control []netip.Addr
	if controlHost != "" {
		control, err = net.DefaultResolver.LookupNetIP(ctx, "ip", controlHost)
		if err != nil {
			return nil, errors.New("Console address resolution failed.")
		}
	}
	for _, ip := range ips {
		for _, forbidden := range control {
			if ip.Unmap() == forbidden.Unmap() {
				return nil, errors.New("Console control addresses are blocked.")
			}
		}
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, errors.New("Destination address is blocked.")
		}
	}
	var last error
	for _, ip := range ips {
		c, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return c, nil
		}
		last = e
	}
	return nil, last
}
func Send(ctx context.Context, m Message) (int, error) { return send(ctx, m, false) }
func send(ctx context.Context, m Message, capture bool) (int, error) {
	validate := Validate
	if capture {
		validate = ValidateDevelopment
	}
	if err := validate(m.Kind, m.Endpoint, m.Secret); err != nil {
		return 0, err
	}
	origin, err := url.Parse(m.Origin)
	if err != nil {
		return 0, errors.New("Invalid console origin.")
	}
	host := ""
	if m.Kind == "webhook" {
		u, _ := url.Parse(m.Endpoint)
		host = u.Hostname()
	} else {
		host = m.Secret.SMTP.Host
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(origin.Hostname(), ".")) {
		return 0, errors.New("Console control endpoints are blocked.")
	}
	if m.Kind == "email" {
		body := fmt.Sprintf("Providah notification\r\n\r\nEvent ID: %s\r\n\r\nOpen the console: %s\r\n", m.EventID, consoleLink(m.Origin, m.Payload))
		if m.Challenge.Code != "" {
			body = "Verify this notification destination in Providah. This code expires in 30 minutes.\r\n\r\nDestination code: " + m.Challenge.Code + "\r\n\r\nOpen the console: " + consoleLink(m.Origin, m.Payload) + "\r\n"
		} else {
			body += string(m.Payload) + "\r\n"
		}
		if err := sendMail(ctx, m.Secret.SMTP, m.Endpoint, body, origin.Hostname(), capture); err != nil {
			return 0, err
		}
		if m.Challenge.SenderCode != "" {
			if err := sendMail(ctx, m.Secret.SMTP, m.Secret.SMTP.Sender, "Verify the sender in Providah. This code expires in 30 minutes.\r\n\r\nSender code: "+m.Challenge.SenderCode+"\r\n\r\nOpen the console: "+consoleLink(m.Origin, m.Payload)+"\r\n", origin.Hostname(), capture); err != nil {
				return 0, err
			}
		}
		return 250, nil
	}
	req, err := webhookRequest(ctx, m)
	if err != nil {
		return 0, err
	}
	client, err := PublicHTTPSClient(m.Origin)
	if capture && m.Endpoint == DevelopmentWebhook {
		client, err = captureClient(), nil
	}
	if err != nil {
		return 0, err
	}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	// Do not retain remote response bodies in diagnostics or audit records.
	return response.StatusCode, nil
}

// Shared outbound transport for webhooks and S3-compatible audit storage.
func PublicHTTPSClient(consoleOrigin string) (*http.Client, error) {
	origin, err := url.Parse(consoleOrigin)
	if err != nil || origin.Hostname() == "" {
		return nil, errors.New("Invalid console origin.")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialChecked(ctx, network, address, origin.Hostname())
	}, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10, ResponseHeaderTimeout: 10 * time.Second}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func ValidateHTTPS(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Port() != "" && u.Port() != "443") || len(endpoint) > 2048 {
		return errors.New("Use an HTTPS endpoint on port 443 without credentials, query parameters, or a fragment.")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicIP(ip) {
		return errors.New("This network destination is blocked.")
	}
	return nil
}

func webhookRequest(ctx context.Context, m Message) (*http.Request, error) {
	var err error
	payload := m.Payload
	if m.Challenge.Code != "" {
		var p map[string]any
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		p["verification_code"] = m.Challenge.Code
		payload, err = json.Marshal(p)
		if err != nil {
			return nil, err
		}
	}
	wh, err := standardwebhooks.NewWebhook(m.Secret.SigningKey)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	signature, err := wh.Sign(m.EventID, now, payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Webhook-Id", m.EventID)
	req.Header.Set("Webhook-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("Webhook-Signature", signature)
	req.Header.Set("Providah-Attempt-Id", m.AttemptID)

	return req, nil
}

func sendMail(ctx context.Context, s *SMTP, to, body, controlHost string, capture bool) error {
	var conn net.Conn
	var err error
	if capture && captureSMTP(s) {
		conn, err = (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", "mailpit:1025")
	} else {
		conn, err = dialChecked(ctx, "tcp", net.JoinHostPort(s.Host, strconv.Itoa(int(s.Port))), controlHost)
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return sendSMTP(ctx, conn, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}, s, to, body)
}
func sendSMTP(ctx context.Context, conn net.Conn, config *tls.Config, s *SMTP, to, body string) error {
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	if s.Port == 465 {
		secure := tls.Client(conn, config)
		if err := secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if s.Port == 587 {
		if err = client.StartTLS(config); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err = client.Mail(s.Sender); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	_, err = io.WriteString(writer, "From: "+s.Sender+"\r\nTo: "+to+"\r\nSubject: Providah notification\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n"+body)
	if err != nil {
		return err
	}
	return writer.Close()
}

// Old payloads continue to open home; payloads cannot redirect email links off site.
func consoleLink(origin string, payload []byte) string {
	base := strings.TrimRight(origin, "/")
	var p struct {
		Path string `json:"console_path"`
	}
	if json.Unmarshal(payload, &p) != nil || len(p.Path) > 2048 {
		return base + "/"
	}
	u, err := url.Parse(p.Path)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(p.Path, "\r\n\\") {
		return base + "/"
	}
	switch u.Path {
	case "/", "/app", "/app/operations", "/app/schedules", "/admin/notifications", "/admin/connections", "/admin/modules", "/admin/audit-export":
		return base + u.RequestURI()
	default:
		return base + "/"
	}
}
