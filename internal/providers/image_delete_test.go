package providers

import (
	"context"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestImageDeletion(t *testing.T) {
	mode, writes := "", 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		action := r.Form.Get("Action")
		if r.URL.Host != "ec2.us-east-1.amazonaws.com" || !strings.Contains(r.Header.Get("Authorization"), "/us-east-1/ec2/aws4_request") {
			t.Fatal("wrong signed target")
		}
		body, code := "", 200
		switch action {
		case "DescribeImages":
			if r.Form.Get("ImageId.1") != "ami-12345678" || r.Form.Get("IncludeDisabled") != "true" || r.Form.Get("IncludeDeprecated") != "true" {
				t.Fatal("incorrect discovery scope")
			}
			body = `<imagesSet><item><imageId>ami-12345678</imageId><imageState>available</imageState><imageOwnerId>123456789012</imageOwnerId><creationDate>2026-09-01T00:00:00Z</creationDate><name>recovery</name><deregistrationProtection>disabled</deregistrationProtection><blockDeviceMapping><item><deviceName>/dev/sda1</deviceName><ebs><snapshotId>snap-12345678</snapshotId></ebs></item></blockDeviceMapping></item></imagesSet>`
			switch mode {
			case "protected":
				body = strings.ReplaceAll(body, "<deregistrationProtection>disabled", "<deregistrationProtection>enabled")
			case "unknown-protection":
				body = strings.ReplaceAll(body, "<deregistrationProtection>disabled", "<deregistrationProtection>unknown")
			case "changed":
				body = strings.ReplaceAll(body, "snap-12345678", "snap-87654321")
			case "wrong":
				body = strings.ReplaceAll(body, "ami-12345678", "ami-87654321")
			case "empty":
				body = "<imagesSet/>"
			case "deregistered":
				body = strings.ReplaceAll(body, "available", "deregistered")
			}
		case "DescribeImageAttribute":
			if r.Form.Get("ImageId") != "ami-12345678" || r.Form.Get("Attribute") != "launchPermission" {
				t.Fatal("wrong attribute target")
			}
			body = `<imageId>ami-12345678</imageId><launchPermission><item><userId>123456789099</userId></item></launchPermission>`
			if mode == "sharing-changed" {
				body = strings.ReplaceAll(body, "123456789099", "123456789088")
			}
		case "DeregisterImage":
			writes++
			if r.Form.Get("ImageId") != "ami-12345678" || r.Form.Get("DeleteAssociatedSnapshots") != "false" {
				t.Fatal("unsafe deletion request")
			}
			body = "<return>true</return>"
			if mode == "empty-ack" {
				body = ""
			}
		default:
			t.Fatal("unexpected write/read", action)
		}
		body = "<" + action + "Response>" + body + "</" + action + "Response>"
		if mode == "denied" || mode == "missing" || mode == "lost" && action == "DeregisterImage" {
			code = 403
			ec := "UnauthorizedOperation"
			if mode == "missing" {
				ec = "InvalidAMIID.NotFound"
				code = 400
			}
			if mode == "lost" {
				ec = "InternalError"
				code = 500
			}
			body = "<Response><Errors><Error><Code>" + ec + "</Code><Message>failed</Message></Error></Errors></Response>"
		}
		return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	req := provider.Request{Provider: "aws", Region: "us-east-1", Credential: `{"access_key_id":"fake","secret_access_key":"fake-secret"}`, Power: &provider.PowerRequest{ResourceKind: "compute.image", OperationID: strings.Repeat("a", 64), NativeID: "ami-12345678", ExpectedStatus: "available", Action: "delete", Phase: "preview"}}
	run := func() *provider.PowerResult {
		if e := req.Power.Validate("aws"); e != nil {
			t.Fatal(e)
		}
		return deleteImage(context.Background(), req, client)
	}
	preview := run()
	if preview.Outcome != "preview" || writes != 0 || !strings.Contains(preview.DeletionImpact, "snap-12345678") || !strings.Contains(preview.DeletionImpact, "123456789099") {
		t.Fatal("missing review", preview)
	}
	req.Power.Phase, req.Power.DeletionImpact = "submit", preview.DeletionImpact
	for _, m := range []string{"protected", "unknown-protection", "changed", "sharing-changed", "wrong", "empty", "missing", "denied", "deregistered"} {
		mode = m
		if run().Outcome != "failed" || writes != 0 {
			t.Fatal("unsafe submit", m)
		}
	}
	for _, m := range []string{"", "lost", "empty-ack"} {
		mode = m
		before := writes
		got := run()
		want := "accepted"
		if m != "" {
			want = "uncertain"
		}
		if got.Outcome != want || writes != before+1 {
			t.Fatal("write acknowledgement or retry", m, got, writes)
		}
	}
	req.Power.Phase = "observe"
	for _, m := range []string{"", "denied", "empty", "missing", "deregistered"} {
		mode = m
		before := writes
		want := "accepted"
		if m == "empty" || m == "missing" || m == "deregistered" {
			want = "succeeded"
		}
		if got := run(); got.Outcome != want || writes != before {
			t.Fatal("unsafe observation", m, got)
		}
	}
}
