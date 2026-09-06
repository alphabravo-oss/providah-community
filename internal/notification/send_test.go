package notification

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"net/textproto"
	"strings"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

func TestSMTPRequiresTLSBeforeAuthentication(t *testing.T) {
	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	certServer.Close()
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-starttls", true: "starttls"}[secure], func(t *testing.T) {
			client, server := net.Pipe()
			defer func() { _ = client.Close() }()
			result := make(chan string, 1)
			go func() {
				defer func() { _ = server.Close() }()
				_ = server.SetDeadline(time.Now().Add(5 * time.Second))
				p := textproto.NewConn(server)
				_ = p.PrintfLine("220 smtp.example.com ready")
				_, _ = p.ReadLine()
				if !secure {
					_ = p.PrintfLine("250 smtp.example.com")
					line, _ := p.ReadLine()
					result <- line
					return
				}
				_ = p.PrintfLine("250-smtp.example.com\r\n250 STARTTLS")
				line, _ := p.ReadLine()
				if line != "STARTTLS" {
					result <- "expected STARTTLS: " + line
					return
				}
				_ = p.PrintfLine("220 upgrade")
				encrypted := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
				if err := encrypted.Handshake(); err != nil {
					result <- err.Error()
					return
				}
				p = textproto.NewConn(encrypted)
				line, _ = p.ReadLine()
				if !strings.HasPrefix(line, "EHLO ") {
					result <- "expected encrypted EHLO"
					return
				}
				_ = p.PrintfLine("250-smtp.example.com\r\n250 AUTH PLAIN")
				line, _ = p.ReadLine()
				if !strings.HasPrefix(line, "AUTH PLAIN ") {
					result <- "expected encrypted authentication"
					return
				}
				_ = p.PrintfLine("235 authenticated")
				line, _ = p.ReadLine()
				if !strings.HasPrefix(line, "MAIL FROM:<sender@example.com>") {
					result <- line
					return
				}
				_ = p.PrintfLine("250 sender ok")
				line, _ = p.ReadLine()
				if line != "RCPT TO:<ops@example.com>" {
					result <- line
					return
				}
				_ = p.PrintfLine("250 recipient ok")
				line, _ = p.ReadLine()
				if line != "DATA" {
					result <- line
					return
				}
				_ = p.PrintfLine("354 send message")
				body, _ := p.ReadDotBytes()
				_ = p.PrintfLine("250 accepted")
				result <- string(body)
			}()
			err := sendSMTP(context.Background(), client, &tls.Config{ServerName: "example.com", RootCAs: roots, MinVersion: tls.VersionTLS12}, &SMTP{Host: "smtp.example.com", Port: 587, Username: "user", Password: "test-password", Sender: "sender@example.com"}, "ops@example.com", "verification code: test-code\r\n")
			_ = client.Close()
			body := <-result
			if !secure {
				if err == nil || strings.Contains(body, "AUTH") {
					t.Fatal("plaintext authentication attempted", body, err)
				}
				return
			}
			if err != nil || !strings.Contains(body, "verification code: test-code") || strings.Contains(body, "test-password") {
				t.Fatal("TLS email delivery failed", err, body)
			}
		})
	}
}

func TestNetworkAndSignature(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254", "168.63.129.16", "100.100.100.200", "0.1.2.3", "192.0.2.1", "fc00::1", "fe80::1", "64:ff9b::7f00:1", "2002:7f00:1::", "2001:db8::1"} {
		if publicIP(netip.MustParseAddr(address)) {
			t.Errorf("allowed forbidden address %s", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(address)) {
			t.Errorf("blocked public address %s", address)
		}
	}
	secret := Secret{SigningKey: "whsec_" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))}
	for _, endpoint := range []string{"http://example.com/", "https://127.0.0.1/", "https://example.com:8443/", "https://user:pass@example.com/", "https://example.com/?token=secret", "https://[::ffff:127.0.0.1]/"} {
		if Validate("webhook", endpoint, secret) == nil {
			t.Errorf("accepted unsafe endpoint %s", endpoint)
		}
	}
	if Validate("email", "a@example.com\r\nBcc: b@example.com", Secret{}) == nil {
		t.Fatal("email header injection accepted")
	}
	if Validate("email", "a@example.com", Secret{SMTP: &SMTP{Host: "smtp.example.com", Port: 25, Sender: "sender@example.com"}}) == nil {
		t.Fatal("plaintext SMTP accepted")
	}
	msg := Message{Kind: "webhook", Endpoint: "https://example.com/events", EventID: "event-1", AttemptID: "attempt-1", Secret: secret, Payload: []byte(`{"version":1,"type":"operation.failed"}`), Challenge: Challenge{Code: "receiver-only-code"}}
	req, err := webhookRequest(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	wh, err := standardwebhooks.NewWebhook(secret.SigningKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = wh.Verify(body, req.Header); err != nil {
		t.Fatal("reference receiver rejected signature", err)
	}
	if req.Header.Get("Webhook-Id") != msg.EventID || req.Header.Get("Providah-Attempt-Id") != msg.AttemptID || !strings.Contains(string(body), msg.Challenge.Code) {
		t.Fatal("identity or verification missing")
	}
	if wh.Verify(append(body, ' '), req.Header) == nil {
		t.Fatal("modified body accepted")
	}
	msg.Origin = "https://example.com"
	if _, err = Send(context.Background(), msg); err == nil {
		t.Fatal("console destination accepted")
	}
	if _, err = DialPublic(context.Background(), "tcp", "127.0.0.1:443"); err == nil {
		t.Fatal("connection-time loopback allowed")
	}
}
