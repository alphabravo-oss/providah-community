package awsauth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBroker(t *testing.T) {
	const raw = `{"access_key_id":"source","secret_access_key":"secret","role_arn":"arn:aws:iam::123456789012:role/console","external_id":"tenant-id"}`
	for _, wrongAccount := range []bool{false, true} {
		calls := 0
		client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Host != "sts.us-east-1.amazonaws.com" {
				t.Fatalf("unexpected endpoint %s", r.URL)
			}
			b, _ := io.ReadAll(r.Body)
			body := string(b)
			reply := ""
			switch calls {
			case 1:
				if !strings.Contains(r.Header.Get("Authorization"), "Credential=source/") || !strings.Contains(body, "Action=AssumeRole") || !strings.Contains(body, "ExternalId=tenant-id") || !strings.Contains(body, "DurationSeconds=900") {
					t.Fatalf("incorrect assume-role request")
				}
				reply = `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials><AccessKeyId>temporary</AccessKeyId><SecretAccessKey>temporary-secret</SecretAccessKey><SessionToken>temporary-token</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleResult></AssumeRoleResponse>`
			case 2:
				if !strings.Contains(r.Header.Get("Authorization"), "Credential=temporary/") || r.Header.Get("X-Amz-Security-Token") != "temporary-token" || !strings.Contains(body, "Action=GetCallerIdentity") {
					t.Fatal("identity did not use role credentials")
				}
				account := "123456789012"
				if wrongAccount {
					account = "999999999999"
				}
				reply = `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>` + account + `</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`
			default:
				t.Fatal("unexpected retry")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/xml"}}, Body: io.NopCloser(strings.NewReader(reply))}, nil
		})}
		result, err := Broker(context.Background(), raw, "us-east-1", "providah-test", client)
		if calls != 2 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		if wrongAccount {
			if err == nil || result != "" {
				t.Fatal("account mismatch accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		c, err := Parse(result)
		if err != nil || c.AccessKeyID != "temporary" || c.RoleARN != "" || c.ExternalID != "" || c.AccountID != "" || strings.Contains(result, "source") {
			t.Fatal("source configuration escaped broker")
		}
	}
}

func TestCredentialBoundaries(t *testing.T) {
	base := `{"access_key_id":"key","secret_access_key":"secret"`
	for _, suffix := range []string{`,"endpoint":"http://localhost"}`, `,"external_id":"orphan"}`, `,"account_id":"wrong"}`, `,"role_arn":"arn:aws:iam::123456789012:user/test"}`, `} {}`} {
		if _, err := Parse(base + suffix); err == nil {
			t.Fatalf("accepted %s", suffix)
		}
	}
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("static credentials made a network call")
		return nil, nil
	})}
	if _, err := Broker(context.Background(), base+`}`, "us-east-1", "providah-test", client); err != nil {
		t.Fatal(err)
	}
	if _, err := Broker(context.Background(), base+`,"role_arn":"arn:aws-cn:iam::123456789012:role/test"}`, "us-east-1", "providah-test", client); err == nil {
		t.Fatal("partition mismatch accepted")
	}
}

func TestExchangeFailureNeverReturnsSource(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 403, Header: http.Header{"Content-Type": []string{"text/xml"}}, Body: io.NopCloser(strings.NewReader(`<ErrorResponse><Error><Code>AccessDenied</Code><Message>denied</Message></Error></ErrorResponse>`))}, nil
	})}
	for _, policy := range []string{`"role_arn":"arn:aws:iam::123456789012:role/console"`, `"account_id":"123456789012"`} {
		raw := `{"access_key_id":"source","secret_access_key":"secret",` + policy + `}`
		out, err := Broker(context.Background(), raw, "us-east-1", "providah-test", client)
		if err == nil || out != "" {
			t.Fatal("failed exchange returned credentials")
		}
	}
	if calls != 2 {
		t.Fatalf("unexpected retry: %d calls", calls)
	}
}
