//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"strings"
	"testing"
)

func exerciseOrganizationSMTP(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org, other string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	initial, err := client.GetOrganizationSmtp(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: org}))
	check(err)
	if initial.Msg.Configured || initial.Msg.Revision != 0 {
		t.Fatal("unexpected initial SMTP profile")
	}
	req := &pb.SetOrganizationSmtpRequest{OrganizationId: org, Smtp: &pb.SmtpSettings{Host: "smtp.example.com", Port: 587, Sender: "sender@example.com", Username: "smtp-user", Password: "smtp-private-value"}}
	saved, err := client.SetOrganizationSmtp(ctx, connect.NewRequest(req))
	check(err)
	if !saved.Msg.Configured || saved.Msg.Revision != 1 {
		t.Fatal("SMTP profile not saved")
	}
	if _, err = client.SetOrganizationSmtp(ctx, connect.NewRequest(req)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale SMTP profile overwrite allowed", err)
	}
	req.OrganizationId = other
	if _, err = client.SetOrganizationSmtp(ctx, connect.NewRequest(req)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign SMTP write allowed", err)
	}
	if _, err = client.GetOrganizationSmtp(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: other})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign SMTP read allowed", err)
	}
	req.OrganizationId = org
	req.ExpectedRevision = 1
	req.Smtp.Port = 25
	if _, err = client.SetOrganizationSmtp(ctx, connect.NewRequest(req)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("insecure SMTP accepted", err)
	}
	req.Smtp.Port = 587
	profile, err := s.q.GetOrganizationSMTP(ctx, org)
	check(err)
	if strings.Contains(string(profile.Ciphertext), "smtp-private-value") {
		t.Fatal("SMTP credentials stored in plaintext")
	}
	meta, err := client.GetOrganizationSmtp(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: org}))
	check(err)
	body, err := json.Marshal(meta.Msg)
	check(err)
	if strings.Contains(string(body), "smtp.example.com") || strings.Contains(string(body), "smtp-private-value") {
		t.Fatal("SMTP profile leaked settings")
	}
	destination := &pb.SaveNotificationDestinationRequest{OrganizationId: org, Name: "Organization SMTP fixture", Kind: "email", Endpoint: "recipient@example.com", UseOrganizationSmtp: true, OrganizationSmtpRevision: 1}
	_, err = client.SaveNotificationDestination(ctx, connect.NewRequest(destination))
	check(err)
	rows, err := s.q.ListNotificationDestinations(ctx, org)
	check(err)
	var original database.NotificationDestination
	for _, row := range rows {
		if row.Name == destination.Name {
			original = row
		}
	}
	if original.ID == "" || original.Verified {
		t.Fatal("organization SMTP bypassed destination verification")
	}
	plain, err := s.open(original.Ciphertext)
	check(err)
	var copied notification.Secret
	check(json.Unmarshal([]byte(plain), &copied))
	if copied.SMTP == nil || copied.SMTP.Password != "smtp-private-value" {
		t.Fatal("organization SMTP not copied")
	}
	req.Smtp.Password = "replacement-private-value"
	_, err = client.SetOrganizationSmtp(ctx, connect.NewRequest(req))
	check(err)
	if _, err = client.SaveNotificationDestination(ctx, connect.NewRequest(destination)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale profile selection accepted", err)
	}
	_, err = client.SetOrganizationSmtp(ctx, connect.NewRequest(&pb.SetOrganizationSmtpRequest{OrganizationId: org, ExpectedRevision: 2, Remove: true}))
	check(err)
	profile, err = s.q.GetOrganizationSMTP(ctx, org)
	check(err)
	if profile.Ciphertext != nil || profile.Revision != 3 {
		t.Fatal("shared SMTP not erased")
	}
	retained, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: original.ID})
	check(err)
	retainedPlain, err := s.open(retained.Ciphertext)
	check(err)
	if retainedPlain != plain || retained.Revision != original.Revision {
		t.Fatal("profile change silently rotated existing destination")
	}
	destination.OrganizationSmtpRevision = 3
	if _, err = client.SaveNotificationDestination(ctx, connect.NewRequest(destination)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("removed SMTP profile used", err)
	}
	destination.UsePlatformSmtp = true
	if _, err = client.SaveNotificationDestination(ctx, connect.NewRequest(destination)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("ambiguous SMTP sources accepted", err)
	}
}
