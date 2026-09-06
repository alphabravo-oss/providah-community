package notification

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
)

func TestDevelopmentCaptureBoundary(t *testing.T) {
	secret := Secret{SigningKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))}
	if Validate("webhook", DevelopmentWebhook, secret) == nil || ValidateDevelopment("webhook", DevelopmentWebhook, secret) != nil {
		t.Fatal("capture opt-in boundary failed")
	}
	for _, endpoint := range []string{"http://webhookie:8080/admin", "http://webhookie:8080/hooks/generic/default?next=evil", "http://127.0.0.1:8080/hooks/generic/default", "http://169.254.169.254/"} {
		if ValidateDevelopment("webhook", endpoint, secret) == nil {
			t.Fatal("arbitrary internal target accepted")
		}
	}
	smtp := Secret{SMTP: &SMTP{Host: "mailpit", Port: 1025, Sender: "test@local.test"}}
	if Validate("email", "to@local.test", smtp) == nil || ValidateDevelopment("email", "to@local.test", smtp) != nil {
		t.Fatal("SMTP capture boundary failed")
	}
	smtp.SMTP.Host = "other"
	if ValidateDevelopment("email", "to@local.test", smtp) == nil {
		t.Fatal("arbitrary SMTP accepted")
	}
	if !DevelopmentOrigin("http://localhost:8760") || DevelopmentOrigin("https://example.com") || DevelopmentOrigin("http://localhost.evil.test") {
		t.Fatal("development origin boundary")
	}
	if _, e := SendDevelopment(context.Background(), Message{Kind: "webhook", Endpoint: DevelopmentWebhook, Origin: "https://example.com", Secret: secret}); e == nil {
		t.Fatal("production origin bypassed outbound checks")
	}
}
