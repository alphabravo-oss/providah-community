//go:build integration

package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"github.com/jackc/pgx/v5"
)

func exerciseNotifications(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org, other string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	exerciseNotificationGrouping(t, s, client, ctx, org, other)
	key := "whsec_" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("z", 32)))
	save := &pb.SaveNotificationDestinationRequest{OrganizationId: org, Name: "Test receiver", Kind: "webhook", Endpoint: "https://hooks.example.com/events", SigningSecret: key}
	_, err := client.SaveNotificationDestination(ctx, connect.NewRequest(save))
	check(err)
	list, err := client.ListNotificationDestinations(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: org}))
	check(err)
	id := list.Msg.Destinations[0].Id
	save.Id = id
	save.ExpectedRevision = list.Msg.Destinations[0].Revision
	_, err = client.SaveNotificationDestination(ctx, connect.NewRequest(save))
	check(err)
	savedDestination, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: id})
	check(err)
	save.Name = "Stale replacement"
	if _, err = client.SaveNotificationDestination(ctx, connect.NewRequest(save)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale destination replacement accepted", err)
	}
	unchangedDestination, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: id})
	check(err)
	if unchangedDestination.Revision != savedDestination.Revision || unchangedDestination.Name != savedDestination.Name || string(unchangedDestination.Ciphertext) != string(savedDestination.Ciphertext) {
		t.Fatal("stale save changed destination or credentials")
	}
	if _, err = client.SetNotificationDestinationEnabled(ctx, connect.NewRequest(&pb.SetNotificationDestinationEnabledRequest{OrganizationId: org, Id: id, Enabled: false, ExpectedRevision: save.ExpectedRevision})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale destination disable accepted", err)
	}
	unchangedDestination, err = s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: id})
	check(err)
	if unchangedDestination.Enabled != savedDestination.Enabled || unchangedDestination.Revision != savedDestination.Revision {
		t.Fatal("stale toggle changed destination")
	}
	serialized, err := json.Marshal(list.Msg)
	check(err)
	if strings.Contains(string(serialized), key) {
		t.Fatal("read API exposed signing secret")
	}
	_, err = client.SendNotificationTest(ctx, connect.NewRequest(&pb.SendNotificationTestRequest{OrganizationId: other, Id: id, Verification: true}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("cross-org delivery allowed", err)
	}
	_, err = client.SendNotificationTest(ctx, connect.NewRequest(&pb.SendNotificationTestRequest{OrganizationId: org, Id: id}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("unverified operational delivery allowed", err)
	}
	previous := s.cfg.NotificationSend
	defer func() { s.cfg.NotificationSend = previous }()
	var received []notification.Message
	response := 204
	s.cfg.NotificationSend = func(_ context.Context, m notification.Message) (int, error) {
		received = append(received, m)
		if m.Secret.SigningKey != key && m.Kind == "webhook" {
			t.Fatal("wrong signing scope")
		}
		return response, nil
	}
	_, err = client.SendNotificationTest(ctx, connect.NewRequest(&pb.SendNotificationTestRequest{OrganizationId: org, Id: id, Verification: true}))
	check(err)
	check(s.notificationTick(ctx))
	if len(received) != 1 || received[0].Challenge.Code == "" {
		t.Fatal("verification not sent")
	}
	var verificationPayload map[string]any
	check(json.Unmarshal(received[0].Payload, &verificationPayload))
	if verificationPayload["console_path"] != notificationConsolePath(org, "notification.verification", id) {
		t.Fatal("verification destination link missing")
	}
	_, err = client.VerifyNotificationDestination(ctx, connect.NewRequest(&pb.VerifyNotificationDestinationRequest{OrganizationId: org, Id: id, Code: "wrong"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("wrong code accepted", err)
	}
	_, err = client.VerifyNotificationDestination(ctx, connect.NewRequest(&pb.VerifyNotificationDestinationRequest{OrganizationId: org, Id: id, Code: received[0].Challenge.Code}))
	check(err)
	event := func(action, target string) error {
		return s.transaction(ctx, func(q *database.Queries) error {
			return audit(ctx, q, org, "test", action, target, map[string]any{"credential": "must-never-leave", "provider_error": "sensitive provider response"})
		})
	}
	check(event("operation.failed", strings.Repeat("a", 64)))
	check(event("operation.failed", strings.Repeat("a", 64)))
	check(event("operation.succeeded", "success-target"))
	var count int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&count))
	if count != 2 {
		t.Fatal("deduplication or success opt-in failed", count)
	}
	response = 503
	check(s.notificationTick(ctx))
	var operationPayload map[string]any
	check(json.Unmarshal(received[1].Payload, &operationPayload))
	operationURL, e := url.Parse(operationPayload["console_path"].(string))
	check(e)
	if operationURL.Path != "/app/operations" || operationURL.Query().Get("org") != org || operationURL.Query().Get("operation") != strings.Repeat("a", 64) {
		t.Fatal("operation link missing")
	}
	latest := func() *pb.NotificationDelivery {
		t.Helper()
		out, e := client.ListNotificationDeliveries(ctx, connect.NewRequest(&pb.ListNotificationDeliveriesRequest{OrganizationId: org}))
		check(e)
		return out.Msg.Deliveries[0]
	}
	job := latest()
	if job.Status != "pending" || job.Attempts != 1 {
		t.Fatal("transient delivery not retained", job)
	}
	if strings.Contains(string(received[1].Payload), "must-never-leave") || strings.Contains(string(received[1].Payload), "provider_error") {
		t.Fatal("audit details leaked")
	}
	_, err = s.pool.Exec(ctx, "UPDATE notification_deliveries SET next_attempt=now() WHERE id=$1", job.Id)
	check(err)
	response = 204
	check(s.notificationTick(ctx))
	if latest().Status != "delivered" || received[1].EventID != received[2].EventID || received[1].AttemptID == received[2].AttemptID || string(received[1].Payload) != string(received[2].Payload) {
		t.Fatal("retry identity or immutable payload failed")
	}
	_, err = client.RedeliverNotification(ctx, connect.NewRequest(&pb.RedeliverNotificationRequest{OrganizationId: org, Id: job.Id}))
	check(err)
	check(s.notificationTick(ctx))
	if latest().Attempts != 3 {
		t.Fatal("manual redelivery lost history")
	}
	// A committed source transaction is required for any external work.
	before := count
	err = s.transaction(ctx, func(q *database.Queries) error {
		if e := audit(ctx, q, org, "test", "operation.failed", "rolled-back", nil); e != nil {
			return e
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("rollback failed")
	}
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&count))
	if count != before {
		t.Fatal("rolled-back event escaped outbox")
	}
	check(event("operation.failed", "disabled-target"))
	_, err = client.SetNotificationDestinationEnabled(ctx, connect.NewRequest(&pb.SetNotificationDestinationEnabledRequest{OrganizationId: org, Id: id, Enabled: false, ExpectedRevision: destinationRevision(t, s, ctx, org, id)}))
	check(err)
	sent := len(received)
	check(s.notificationTick(ctx))
	if len(received) != sent || latest().Status != "canceled" {
		t.Fatal("disabled destination was contacted")
	}
	// Shared email destinations prove both recipient and sender mailbox ownership.
	_, err = client.SaveNotificationDestination(ctx, connect.NewRequest(&pb.SaveNotificationDestinationRequest{OrganizationId: org, Name: "Shared mailbox", Kind: "email", Endpoint: "ops@example.com", Smtp: &pb.SmtpSettings{Host: "smtp.example.com", Port: 587, Sender: "sender@example.com", Username: "user", Password: "smtp-secret"}}))
	check(err)
	list, err = client.ListNotificationDestinations(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: org}))
	check(err)
	mailID := list.Msg.Destinations[1].Id
	_, err = client.SendNotificationTest(ctx, connect.NewRequest(&pb.SendNotificationTestRequest{OrganizationId: org, Id: mailID, Verification: true}))
	check(err)
	check(s.notificationTick(ctx))
	mail := received[len(received)-1]
	if mail.Challenge.SenderCode == "" || mail.Secret.SMTP.Password != "smtp-secret" {
		t.Fatal("SMTP verification missing")
	}
	_, err = client.VerifyNotificationDestination(ctx, connect.NewRequest(&pb.VerifyNotificationDestinationRequest{OrganizationId: org, Id: mailID, Code: mail.Challenge.Code}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("sender verification bypassed")
	}
	_, err = client.VerifyNotificationDestination(ctx, connect.NewRequest(&pb.VerifyNotificationDestinationRequest{OrganizationId: org, Id: mailID, Code: mail.Challenge.Code, SenderCode: mail.Challenge.SenderCode}))
	check(err)
	check(event("operation.failed", "recovery-target"))
	_, err = s.pool.Exec(ctx, "UPDATE organizations SET active=false WHERE id=$1", org)
	check(err)
	_, err = s.q.ClaimNotificationDelivery(ctx)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("disabled organization notification was claimed", err)
	}
	_, err = s.pool.Exec(ctx, "UPDATE organizations SET active=true WHERE id=$1", org)
	check(err)
	claimed, err := s.q.ClaimNotificationDelivery(ctx)
	check(err)
	lostAttempt := randomID()
	check(s.q.AddNotificationAttempt(ctx, database.AddNotificationAttemptParams{ID: lostAttempt, OrgID: org, DeliveryID: claimed.ID, Number: claimed.Attempts}))
	_, err = s.pool.Exec(ctx, "UPDATE notification_deliveries SET lease_until=now()-interval '1 second' WHERE id=$1", claimed.ID)
	check(err)
	response = 503
	check(s.notificationTick(ctx))
	var outcome string
	check(s.pool.QueryRow(ctx, "SELECT status FROM notification_attempts WHERE id=$1", lostAttempt).Scan(&outcome))
	if outcome != "uncertain" {
		t.Fatal("lost attempt was not recorded", outcome)
	}
	_, err = s.pool.Exec(ctx, "UPDATE notification_deliveries SET next_attempt=now(),retry_count=7 WHERE id=$1", claimed.ID)
	check(err)
	check(s.notificationTick(ctx))
	if latest().Status != "dead" {
		t.Fatal("retry budget was not bounded", latest())
	}
	_, err = client.RedeliverNotification(ctx, connect.NewRequest(&pb.RedeliverNotificationRequest{OrganizationId: org, Id: claimed.ID}))
	check(err)
	response = 204
	check(s.notificationTick(ctx))
	if latest().Status != "delivered" {
		t.Fatal("dead letter could not be redelivered", latest())
	}

	// Subscription changes preserve verified credentials but fence queued work.
	destination, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: mailID})
	check(err)
	check(event("operation.failed", "subscription-before"))
	update := &pb.SetNotificationSubscriptionsRequest{OrganizationId: org, Id: mailID, ExpectedRevision: destination.Revision, EventTypes: []string{"operation.succeeded", "operation.succeeded"}}
	_, err = client.SetNotificationSubscriptions(ctx, connect.NewRequest(update))
	check(err)
	_, err = client.SetNotificationSubscriptions(ctx, connect.NewRequest(update))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale subscriptions accepted", err)
	}
	update.ExpectedRevision++
	update.OrganizationId = other
	_, err = client.SetNotificationSubscriptions(ctx, connect.NewRequest(update))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign subscriptions accepted", err)
	}
	update.OrganizationId = org
	update.EventTypes = []string{"invented.event"}
	_, err = client.SetNotificationSubscriptions(ctx, connect.NewRequest(update))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unknown event accepted", err)
	}
	after, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: mailID})
	check(err)
	if !after.Verified || string(after.Ciphertext) != string(destination.Ciphertext) || len(after.EventTypes) != 1 {
		t.Fatal("subscription change damaged configuration")
	}
	sent = len(received)
	check(s.notificationTick(ctx))
	if len(received) != sent || latest().Status != "canceled" {
		t.Fatal("previous subscription delivery escaped")
	}
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&before))
	check(event("operation.failed", "subscription-excluded"))
	check(event("operation.succeeded", "subscription-included"))
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&count))
	if count != before+1 {
		t.Fatal("custom subscription fanout incorrect", before, count)
	}
	check(s.notificationTick(ctx))
	if latest().EventType != "operation.succeeded" || latest().Status != "delivered" {
		t.Fatal("selected success not delivered")
	}
	update.EventTypes = nil
	_, err = client.SetNotificationSubscriptions(ctx, connect.NewRequest(update))
	check(err)
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&before))
	check(event("operation.failed", "defaults-restored"))
	check(event("operation.succeeded", "defaults-success-off"))
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&count))
	if count != before+1 {
		t.Fatal("default subscriptions not restored")
	}
	check(s.notificationTick(ctx))

	// Automation outcomes reuse the transactional outbox, subscriptions and redacted payload.
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&before))
	for _, kind := range []string{"automation.validation_failed", "automation.validation_canceled", "automation.version_retired", "automation.version_revoked"} {
		check(event(kind, strings.Repeat("b", 64)))
		check(s.notificationTick(ctx))
		if latest().EventType != kind || latest().Status != "delivered" {
			t.Fatal("automation alert not delivered", latest())
		}
		payload := string(received[len(received)-1].Payload)
		if strings.Contains(payload, "must-never-leave") || strings.Contains(payload, "sensitive provider response") || !strings.Contains(payload, "/app/templates?") {
			t.Fatal("automation payload not safe or linked")
		}
	}
	check(event("automation.validation_succeeded", "default-validation-success"))
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&count))
	if count != before+4 {
		t.Fatal("validation success was not opt-in", count, before)
	}
	current, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: mailID})
	check(err)
	_, err = client.SetNotificationSubscriptions(ctx, connect.NewRequest(&pb.SetNotificationSubscriptionsRequest{OrganizationId: org, Id: mailID, ExpectedRevision: current.Revision, EventTypes: []string{"automation.validation_succeeded"}}))
	check(err)
	check(event("automation.validation_succeeded", strings.Repeat("c", 64)))
	check(s.notificationTick(ctx))
	if latest().EventType != "automation.validation_succeeded" || latest().Status != "delivered" {
		t.Fatal("explicit validation success subscription ignored")
	}
	_, err = client.SetNotificationSubscriptions(ctx, connect.NewRequest(&pb.SetNotificationSubscriptionsRequest{OrganizationId: org, Id: mailID, ExpectedRevision: current.Revision + 1}))
	check(err)

	// Exact deep-link reads are independent of directory pagination and secret storage.
	destRead, err := client.GetNotificationDestination(ctx, connect.NewRequest(&pb.GetNotificationRecordRequest{OrganizationId: org, Id: mailID}))
	check(err)
	if destRead.Msg.Id != mailID || !destRead.Msg.Verified {
		t.Fatal("destination metadata missing")
	}
	deliveryRead, err := client.GetNotificationDelivery(ctx, connect.NewRequest(&pb.GetNotificationRecordRequest{OrganizationId: org, Id: claimed.ID}))
	check(err)
	if deliveryRead.Msg.Id != claimed.ID || deliveryRead.Msg.Status != "delivered" {
		t.Fatal("delivery metadata missing")
	}
	for _, record := range []struct {
		id          string
		destination bool
	}{{mailID, true}, {claimed.ID, false}} {
		for _, scope := range []string{other, org} {
			request := &pb.GetNotificationRecordRequest{OrganizationId: scope, Id: record.id}
			if scope == org {
				request.Id = strings.Repeat("f", 64)
				if !record.destination {
					request.Id += ":" + strings.Repeat("f", 64)
				}
			}
			if record.destination {
				_, err = client.GetNotificationDestination(ctx, connect.NewRequest(request))
			} else {
				_, err = client.GetNotificationDelivery(ctx, connect.NewRequest(request))
			}
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("foreign/missing notification exposed", err)
			}
		}
	}
	_, err = client.GetNotificationDestination(ctx, connect.NewRequest(&pb.GetNotificationRecordRequest{OrganizationId: org, Id: "bad"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("invalid destination ID accepted", err)
	}
	_, err = client.GetNotificationDelivery(ctx, connect.NewRequest(&pb.GetNotificationRecordRequest{OrganizationId: org, Id: mailID}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("invalid delivery ID accepted", err)
	}

	// Removal retains delivery history while erasing active destination secrets.
	beforeRemove, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: mailID})
	check(err)
	remove := &pb.DeleteNotificationDestinationRequest{OrganizationId: org, Id: mailID, ExpectedRevision: beforeRemove.Revision, Confirmation: "wrong"}
	_, err = client.DeleteNotificationDestination(ctx, connect.NewRequest(remove))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("wrong removal confirmation accepted", err)
	}
	remove.Confirmation = beforeRemove.Name
	remove.ExpectedRevision--
	_, err = client.DeleteNotificationDestination(ctx, connect.NewRequest(remove))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale removal accepted", err)
	}
	remove.ExpectedRevision++
	remove.OrganizationId = other
	_, err = client.DeleteNotificationDestination(ctx, connect.NewRequest(remove))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign removal accepted", err)
	}
	remove.OrganizationId = org
	check(event("operation.failed", "before-removal"))
	queued := latest()
	_, err = client.DeleteNotificationDestination(ctx, connect.NewRequest(remove))
	check(err)
	removed, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: mailID})
	check(err)
	if !removed.DeletedAt.Valid || removed.Enabled || removed.Verified || removed.Ciphertext != nil || removed.Endpoint != "" || removed.VerificationHash != nil || removed.SenderHash != nil {
		t.Fatal("removed destination retained secrets or eligibility")
	}
	retained, err := client.GetNotificationDelivery(ctx, connect.NewRequest(&pb.GetNotificationRecordRequest{OrganizationId: org, Id: queued.Id}))
	check(err)
	if retained.Msg.Status != "canceled" || retained.Msg.DestinationName != beforeRemove.Name {
		t.Fatal("pending delivery not canceled or history lost")
	}
	_, err = client.GetNotificationDestination(ctx, connect.NewRequest(&pb.GetNotificationRecordRequest{OrganizationId: org, Id: mailID}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("removed destination still visible", err)
	}
	_, err = client.SetNotificationDestinationEnabled(ctx, connect.NewRequest(&pb.SetNotificationDestinationEnabledRequest{OrganizationId: org, Id: mailID, Enabled: true, ExpectedRevision: destinationRevision(t, s, ctx, org, mailID)}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("removed destination reenabled", err)
	}
	_, err = client.SetNotificationSubscriptions(ctx, connect.NewRequest(&pb.SetNotificationSubscriptionsRequest{OrganizationId: org, Id: mailID, ExpectedRevision: removed.Revision}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("removed subscriptions changed", err)
	}
	_, err = client.SendNotificationTest(ctx, connect.NewRequest(&pb.SendNotificationTestRequest{OrganizationId: org, Id: mailID, Verification: true}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("removed destination contacted", err)
	}
	_, err = client.RedeliverNotification(ctx, connect.NewRequest(&pb.RedeliverNotificationRequest{OrganizationId: org, Id: queued.Id}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("removed delivery replay allowed", err)
	}
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&before))
	check(event("operation.failed", "after-removal"))
	sent = len(received)
	check(s.notificationTick(ctx))
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE org_id=$1", org).Scan(&count))
	if count != before || len(received) != sent {
		t.Fatal("removed destination received work")
	}
	list, err = client.ListNotificationDestinations(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: org}))
	check(err)
	for _, d := range list.Msg.Destinations {
		if d.Id == mailID {
			t.Fatal("removed destination remains in directory")
		}
	}
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries WHERE destination_id=$1 AND verification_ciphertext IS NOT NULL", mailID).Scan(&count))
	if count != 0 {
		t.Fatal("verification challenges retained after removal")
	}

	exerciseOrganizationSMTP(t, s, client, ctx, org, other)
}

func exerciseNotificationGrouping(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org, other string) {
	t.Helper()
	get, e := client.GetNotificationGrouping(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: org}))
	if e != nil {
		t.Fatal(e)
	}
	if get.Msg.Seconds != 900 {
		t.Fatal("wrong grouping default")
	}
	revision := get.Msg.Revision
	set := func(seconds int32) {
		t.Helper()
		out, e := client.SetNotificationGrouping(ctx, connect.NewRequest(&pb.SetNotificationGroupingRequest{OrganizationId: org, Seconds: seconds, ExpectedRevision: revision}))
		if e != nil {
			t.Fatal(e)
		}
		revision = out.Msg.Revision
	}
	checkEvents := func(want int) {
		t.Helper()
		tx, e := s.pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		q := s.q.WithTx(tx)
		target := randomID()
		for range 2 {
			id := randomID()
			if e = q.EnqueueNotificationEvent(ctx, database.EnqueueNotificationEventParams{ID: id, OrgID: org, Type: "operation.failed", Target: target, Payload: []byte(`{}`)}); e != nil {
				t.Fatal(e)
			}
		}
		var count int
		if e = tx.QueryRow(ctx, "SELECT count(*) FROM notification_events WHERE org_id=$1 AND target=$2", org, target).Scan(&count); e != nil || count != want {
			t.Fatal("grouping event count", count, want, e)
		}
	}
	checkEvents(1)
	set(0)
	checkEvents(2)
	set(60)
	checkEvents(1)
	if _, e = client.SetNotificationGrouping(ctx, connect.NewRequest(&pb.SetNotificationGroupingRequest{OrganizationId: org, Seconds: 300, ExpectedRevision: revision - 1})); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale grouping save", e)
	}
	if _, e = client.SetNotificationGrouping(ctx, connect.NewRequest(&pb.SetNotificationGroupingRequest{OrganizationId: org, Seconds: 17, ExpectedRevision: revision})); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("invalid grouping accepted", e)
	}
	if _, e = client.SetNotificationGrouping(ctx, connect.NewRequest(&pb.SetNotificationGroupingRequest{OrganizationId: other, Seconds: 0, ExpectedRevision: revision})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("foreign grouping changed", e)
	}
	set(900)
}

func destinationRevision(t *testing.T, s *Service, ctx context.Context, org, id string) int64 {
	t.Helper()
	row, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: org, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return row.Revision
}
