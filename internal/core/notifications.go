package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var notificationEventTypes = []string{"account.password_changed", "account.recovery_code_used", "account.recovery_codes_replaced", "approval.rejected", "approval.requested", "automation.validation_canceled", "automation.validation_failed", "automation.validation_succeeded", "automation.version_retired", "automation.version_revoked", "audit.export_backlog", "audit.export_failed", "connection.credential_rotated", "connection.refresh_failed", "invitation.created", "invitation.revoked", "maintenance.policy_deleted", "maintenance.policy_saved", "member.updated", "operation.canceled", "operation.expired", "operation.failed", "operation.succeeded", "operation.uncertain", "provider.module_state_changed", "provider.runtime_changed", "role.saved", "schedule.saved", "schedule.skipped", "security.encryption_key_rotated"}

func notificationPayload(id, org, kind, target string) []byte {
	// Deliberately exclude audit details, actor names, credentials, resource names,
	// native IDs, and provider errors. Full context remains behind console RBAC.
	b, _ := json.Marshal(map[string]any{"version": 1, "id": id, "organization_id": org, "type": kind, "occurred_at": time.Now().UTC().Format(time.RFC3339), "console_path": notificationConsolePath(org, kind, target)})
	return b
}

// Paths contain only internal IDs; names, audit details and credentials stay in the console.
func notificationConsolePath(org, kind, target string) string {
	path, key := "/app", ""
	switch {
	case strings.HasPrefix(kind, "operation."), strings.HasPrefix(kind, "approval."):
		path, key = "/app/operations", "operation"
	case strings.HasPrefix(kind, "automation.validation_"):
		path, key = "/app/templates", "validation"
	case strings.HasPrefix(kind, "automation.version_"):
		path, key = "/app/templates", "version"
	case strings.HasPrefix(kind, "schedule."):
		path, key = "/app/schedules", "schedule"
	case strings.HasPrefix(kind, "notification."):
		path, key = "/admin/notifications", "destination"
	case strings.HasPrefix(kind, "connection."):
		path, key = "/admin/connections", "connection"
	case strings.HasPrefix(kind, "provider."):
		path = "/admin/modules"
	case strings.HasPrefix(kind, "audit.export_"):
		path = "/admin/audit-export"
	}
	values := url.Values{"org": {org}}
	if key != "" && notificationRecordID.MatchString(target) {
		values.Set(key, target)
	}
	return path + "?" + values.Encode()
}

var notificationRecordID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func enqueueNotification(ctx context.Context, q *database.Queries, org, action, target string, details map[string]any) error {
	kind := ""
	switch {
	case slices.Contains(notificationEventTypes, action):
		kind = action
	case action == "operation.requested":
		if details["status"] == "awaiting_approval" {
			kind = "approval.requested"
		}
	case action == "operation.reviewed":
		if details["decision"] == "rejected" {
			kind = "approval.rejected"
		}
	case action == "schedule.occurrence":
		if details["outcome"] == "skipped" {
			kind = "schedule.skipped"
		}
	}
	if kind == "" {
		return nil
	}
	id := newOperationID()
	// Grouping is computed atomically from the organization policy in PostgreSQL.
	return q.EnqueueNotificationEvent(ctx, database.EnqueueNotificationEventParams{ID: id, OrgID: org, Type: kind, Target: target, Payload: notificationPayload(id, org, kind, target), Success: kind == "operation.succeeded" || kind == "automation.validation_succeeded"})
}
func canManageNotifications(ctx context.Context, q *database.Queries, org string) bool {
	p, err := q.Permissions(ctx, database.PermissionsParams{OrgID: org, UserID: actor(ctx).UserID})
	return err == nil && slices.Contains(p, "notifications.manage")
}
func (s *Service) ListNotificationDestinations(ctx context.Context, req *connect.Request[pb.ListNotificationDestinationsRequest]) (*connect.Response[pb.ListNotificationDestinationsResponse], error) {
	rows, err := s.q.ListNotificationDestinations(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	if len(rows) > 100 {
		return nil, conflict("The destination directory currently supports 100 entries.")
	}
	profile, err := s.q.GetOrganizationSMTPMetadata(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	out := &pb.ListNotificationDestinationsResponse{OrganizationSmtp: &pb.OrganizationSmtp{Configured: profile.Configured, Revision: profile.Revision}, DevelopmentCapture: s.cfg.DevelopmentCapture, PlatformSmtpAvailable: s.cfg.SMTP != nil, AvailableEventTypes: notificationEventTypes}
	for _, d := range rows {
		out.Destinations = append(out.Destinations, &pb.NotificationDestination{Id: d.ID, Name: d.Name, Kind: d.Kind, Endpoint: d.Endpoint, Enabled: d.Enabled, Verified: d.Verified, IncludeSuccess: d.IncludeSuccess, EventTypes: d.EventTypes, Revision: d.Revision})
	}
	return connect.NewResponse(out), nil
}
func (s *Service) SaveNotificationDestination(ctx context.Context, req *connect.Request[pb.SaveNotificationDestinationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	r.Name = strings.TrimSpace(r.Name)
	r.Endpoint = strings.TrimSpace(r.Endpoint)
	if (r.Id == "" && r.ExpectedRevision != 0) || (r.Id != "" && r.ExpectedRevision < 1) {
		return nil, invalid("Supply the reviewed destination revision for replacement.")
	}
	if len(r.Name) < 1 || len(r.Name) > 120 {
		return nil, invalid("Name the notification destination.")
	}
	if r.UseOrganizationSmtp && (r.Kind != "email" || r.UsePlatformSmtp || r.Smtp != nil || r.SigningSecret != "") {
		return nil, invalid("Choose one SMTP configuration source.")
	}
	id := r.Id
	if id == "" {
		id = randomID()
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := s.accessLock(ctx, q, r.OrganizationId, "notifications.manage"); err != nil {
			return err
		}
		secret := notification.Secret{SigningKey: r.SigningSecret}
		if r.UseOrganizationSmtp {
			profile, err := q.GetOrganizationSMTP(ctx, r.OrganizationId)
			if errors.Is(err, pgx.ErrNoRows) {
				return conflict("Configure organization SMTP first.")
			}
			if err != nil {
				return err
			}
			if profile.Ciphertext == nil || profile.Revision != r.OrganizationSmtpRevision {
				return conflict("Organization SMTP changed. Reload before saving.")
			}
			raw, err := s.open(profile.Ciphertext)
			if err != nil {
				return err
			}
			if err = json.Unmarshal([]byte(raw), &secret); err != nil {
				return err
			}
		} else if r.UsePlatformSmtp {
			secret.SMTP = s.cfg.SMTP
		} else if v := r.Smtp; v != nil {
			secret.SMTP = &notification.SMTP{Host: v.Host, Port: v.Port, Username: v.Username, Password: v.Password, Sender: v.Sender}
		}
		validate := notification.Validate
		if s.cfg.DevelopmentCapture {
			validate = notification.ValidateDevelopment
		}
		if err := validate(r.Kind, r.Endpoint, secret); err != nil {
			return invalid(err.Error())
		}
		raw, err := json.Marshal(secret)
		if err != nil {
			return err
		}
		cipher, err := s.seal(string(raw))
		if err != nil {
			return err
		}
		if r.Id == "" {
			rows, err := q.ListNotificationDestinations(ctx, r.OrganizationId)
			if err != nil {
				return err
			}
			if len(rows) >= 100 {
				return conflict("The destination directory currently supports 100 entries.")
			}
			if err := q.CreateNotificationDestination(ctx, database.CreateNotificationDestinationParams{ID: id, OrgID: r.OrganizationId, Name: r.Name, Kind: r.Kind, Endpoint: r.Endpoint, Ciphertext: cipher, IncludeSuccess: r.IncludeSuccess}); err != nil {
				return err
			}
		} else {
			old, err := q.LockNotificationDestination(ctx, database.LockNotificationDestinationParams{OrgID: r.OrganizationId, ID: id})
			if err != nil {
				return denied()
			}
			if old.Revision != r.ExpectedRevision {
				return conflict("Destination changed. Reload and review it before replacing its configuration.")
			}
			if old.Kind != r.Kind {
				return invalid("Create a new destination to change its type.")
			}
			if err = q.UpdateNotificationDestination(ctx, database.UpdateNotificationDestinationParams{OrgID: r.OrganizationId, ID: id, Name: r.Name, Endpoint: r.Endpoint, Ciphertext: cipher, IncludeSuccess: r.IncludeSuccess}); err != nil {
				return err
			}
		}
		var details map[string]any
		if r.UseOrganizationSmtp {
			details = map[string]any{"smtp_source": "organization", "smtp_revision": r.OrganizationSmtpRevision}
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "notification.destination_saved", id, details)
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) SetNotificationDestinationEnabled(ctx context.Context, req *connect.Request[pb.SetNotificationDestinationEnabledRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	err := s.transaction(ctx, func(q *database.Queries) error {
		if !canManageNotifications(ctx, q, r.OrganizationId) {
			return denied()
		}
		destination, err := q.LockNotificationDestination(ctx, database.LockNotificationDestinationParams{OrgID: r.OrganizationId, ID: r.Id})
		if err != nil {
			return denied()
		}
		if r.ExpectedRevision < 1 || destination.Revision != r.ExpectedRevision {
			return conflict("Destination changed. Reload and review before enabling or disabling it.")
		}
		if err := q.SetNotificationDestinationEnabled(ctx, database.SetNotificationDestinationEnabledParams{OrgID: r.OrganizationId, ID: r.Id, Enabled: r.Enabled}); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "notification.destination_state_changed", r.Id, map[string]any{"enabled": r.Enabled})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) SendNotificationTest(ctx context.Context, req *connect.Request[pb.SendNotificationTestRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if err := s.limit(ctx, "notification-test:"+r.OrganizationId); err != nil {
		return nil, err
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		if !canManageNotifications(ctx, q, r.OrganizationId) {
			return denied()
		}
		d, err := q.LockNotificationDestination(ctx, database.LockNotificationDestinationParams{OrgID: r.OrganizationId, ID: r.Id})
		if err != nil {
			return denied()
		}
		if !d.Enabled {
			return conflict("Enable this destination first.")
		}
		var cipher []byte
		kind := "notification.test"
		if r.Verification {
			kind = "notification.verification"
			challenge := notification.Challenge{Code: randomID()}
			var senderHash []byte
			if d.Kind == "email" {
				challenge.SenderCode = randomID()
				senderHash = hash(challenge.SenderCode)
			}
			raw, e := json.Marshal(challenge)
			if e != nil {
				return e
			}
			cipher, e = s.seal(string(raw))
			if e != nil {
				return e
			}
			if err = q.SetNotificationChallenge(ctx, database.SetNotificationChallengeParams{OrgID: d.OrgID, ID: d.ID, VerificationHash: hash(challenge.Code), SenderHash: senderHash}); err != nil {
				return err
			}
		} else if !d.Verified {
			return conflict("Verify the destination first.")
		}
		id := newOperationID()
		if err = q.CreateDirectNotification(ctx, database.CreateDirectNotificationParams{ID: id, OrgID: d.OrgID, Type: kind, Target: d.ID, Payload: notificationPayload(id, d.OrgID, kind, d.ID), Bucket: pgtype.Int8{Int64: time.Now().UnixNano(), Valid: true}, VerificationCiphertext: cipher}); err != nil {
			return err
		}
		return audit(ctx, q, d.OrgID, actor(ctx).Email, "notification.test_queued", d.ID, nil)
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) VerifyNotificationDestination(ctx context.Context, req *connect.Request[pb.VerifyNotificationDestinationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if err := s.limit(ctx, "notification-verify:"+r.OrganizationId); err != nil {
		return nil, err
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		if !canManageNotifications(ctx, q, r.OrganizationId) {
			return denied()
		}
		d, err := q.LockNotificationDestination(ctx, database.LockNotificationDestinationParams{OrgID: r.OrganizationId, ID: r.Id})
		if err != nil {
			return denied()
		}
		if !d.VerificationExpires.Valid || time.Now().After(d.VerificationExpires.Time) || subtle.ConstantTimeCompare(d.VerificationHash, hash(strings.TrimSpace(r.Code))) != 1 || (d.Kind == "email" && subtle.ConstantTimeCompare(d.SenderHash, hash(strings.TrimSpace(r.SenderCode))) != 1) {
			return invalid("Verification codes are invalid or expired.")
		}
		if err = q.VerifyNotificationDestination(ctx, database.VerifyNotificationDestinationParams{OrgID: d.OrgID, ID: d.ID}); err != nil {
			return err
		}
		return audit(ctx, q, d.OrgID, actor(ctx).Email, "notification.destination_verified", d.ID, nil)
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) ListNotificationDeliveries(ctx context.Context, req *connect.Request[pb.ListNotificationDeliveriesRequest]) (*connect.Response[pb.ListNotificationDeliveriesResponse], error) {
	r := req.Msg
	after, err := parseCursor(r.PageToken, r.OrganizationId, "deliveries")
	if err != nil {
		return nil, err
	}
	if after == "" {
		after = "~"
	}
	rows, err := s.q.ListNotificationDeliveries(ctx, database.ListNotificationDeliveriesParams{OrgID: r.OrganizationId, ID: after})
	if err != nil {
		return nil, err
	}
	out := &pb.ListNotificationDeliveriesResponse{}
	if len(rows) > 100 {
		out.NextPageToken = cursor(r.OrganizationId, "deliveries", rows[99].ID)
		rows = rows[:100]
	}
	for _, d := range rows {
		out.Deliveries = append(out.Deliveries, &pb.NotificationDelivery{Id: d.ID, EventId: d.EventID, DestinationName: d.DestinationName, EventType: d.Type, Status: d.Status, Attempts: d.Attempts, Detail: d.Detail, UpdatedAt: stamp(d.UpdatedAt)})
	}
	return connect.NewResponse(out), nil
}
func (s *Service) ListNotificationAttempts(ctx context.Context, req *connect.Request[pb.ListNotificationAttemptsRequest]) (*connect.Response[pb.ListNotificationAttemptsResponse], error) {
	rows, err := s.q.ListNotificationAttempts(ctx, database.ListNotificationAttemptsParams{OrgID: req.Msg.OrganizationId, DeliveryID: req.Msg.Id})
	if err != nil {
		return nil, err
	}
	out := &pb.ListNotificationAttemptsResponse{}
	for _, a := range rows {
		out.Attempts = append(out.Attempts, &pb.NotificationAttempt{Id: a.ID, Number: a.Number, Status: a.Status, ResponseCode: a.ResponseCode, Detail: a.Detail, CreatedAt: stamp(a.CreatedAt)})
	}
	return connect.NewResponse(out), nil
}
func (s *Service) RedeliverNotification(ctx context.Context, req *connect.Request[pb.RedeliverNotificationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if err := s.limit(ctx, "notification-redelivery:"+r.OrganizationId); err != nil {
		return nil, err
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		if !canManageNotifications(ctx, q, r.OrganizationId) {
			return denied()
		}
		d, err := q.LockNotificationDelivery(ctx, database.LockNotificationDeliveryParams{OrgID: r.OrganizationId, ID: r.Id})
		if err != nil {
			return denied()
		}
		if d.Status == "pending" || d.Status == "sending" || len(d.VerificationCiphertext) > 0 {
			return conflict("Only completed operational deliveries can be redelivered. Send a fresh verification separately.")
		}
		dest, err := q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: d.OrgID, ID: d.DestinationID})
		if err != nil {
			return err
		}
		if !dest.Enabled || !dest.Verified || dest.Revision != d.DestinationRevision {
			return conflict("Destination changed or is disabled; create a new test with its current configuration.")
		}
		if err = q.RedeliverNotification(ctx, database.RedeliverNotificationParams{OrgID: d.OrgID, ID: d.ID}); err != nil {
			return err
		}
		return audit(ctx, q, d.OrgID, actor(ctx).Email, "notification.redelivery_queued", d.ID, nil)
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}

// ponytail: one delivery per process at a time; add workers using the same leases when throughput requires it.
func (s *Service) StartNotifications(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.notificationTick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error().Msg("notification coordinator failed")
			}
		}
	}
}
func (s *Service) notificationTick(ctx context.Context) error {
	var job database.NotificationDelivery
	attempt := randomID()
	err := s.transaction(ctx, func(q *database.Queries) error {
		var e error
		job, e = q.ClaimNotificationDelivery(ctx)
		if e != nil {
			return e
		}
		if e = q.MarkNotificationLostAttempts(ctx, database.MarkNotificationLostAttemptsParams{OrgID: job.OrgID, DeliveryID: job.ID}); e != nil {
			return e
		}
		if err := q.AddNotificationAttempt(ctx, database.AddNotificationAttemptParams{ID: attempt, OrgID: job.OrgID, DeliveryID: job.ID, Number: job.Attempts}); err != nil {
			return err
		}
		return audit(ctx, q, job.OrgID, "system:notifications", "notification.sending", job.ID, map[string]any{"attempt": attempt})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	status, detail, code := "pending", "Delivery failed; retry scheduled.", 0
	dest, err := s.q.GetNotificationDestination(ctx, database.GetNotificationDestinationParams{OrgID: job.OrgID, ID: job.DestinationID})
	if err != nil {
		return err
	}
	verification := len(job.VerificationCiphertext) > 0
	if !dest.Enabled || dest.Revision != job.DestinationRevision || (!verification && !dest.Verified) {
		status = "canceled"
		detail = "Destination disabled, unverified, or changed."
	} else if verification && (!dest.VerificationExpires.Valid || time.Now().After(dest.VerificationExpires.Time)) {
		status = "dead"
		detail = "Verification expired; request a fresh test."
	} else if job.RetryCount > 8 {
		status = "dead"
		detail = "Retry budget exhausted after interrupted attempts."
	} else {
		event, e := s.q.GetNotificationEvent(ctx, database.GetNotificationEventParams{OrgID: job.OrgID, ID: job.EventID})
		if e != nil {
			return e
		}
		raw, e := s.open(dest.Ciphertext)
		if e != nil {
			return e
		}
		message := notification.Message{Kind: dest.Kind, Endpoint: dest.Endpoint, Origin: s.cfg.Origin, EventID: job.EventID, AttemptID: attempt, Payload: event.Payload}
		if e = json.Unmarshal([]byte(raw), &message.Secret); e != nil {
			return e
		}
		if verification {
			raw, e = s.open(job.VerificationCiphertext)
			if e != nil {
				return e
			}
			if e = json.Unmarshal([]byte(raw), &message.Challenge); e != nil {
				return e
			}
		}
		send := s.cfg.NotificationSend
		if send == nil {
			send = notification.Send
			if s.cfg.DevelopmentCapture {
				send = notification.SendDevelopment
			}
		}
		callCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		code, e = send(callCtx, message)
		cancel()
		if e == nil && code >= 200 && code < 300 {
			status = "delivered"
			detail = "Destination accepted the delivery; downstream completion is not implied."
		} else if (e == nil && code >= 300 && code < 500 && code != 408 && code != 429) || job.RetryCount >= 8 {
			status = "dead"
			detail = "Delivery failed permanently or exhausted its retry budget."
		}
	}
	return s.transaction(ctx, func(q *database.Queries) error {
		current, e := q.LockNotificationDelivery(ctx, database.LockNotificationDeliveryParams{OrgID: job.OrgID, ID: job.ID})
		if e != nil {
			return e
		}
		if current.Status != "sending" || current.Attempts != job.Attempts || !current.LeaseUntil.Valid || !current.LeaseUntil.Time.Equal(job.LeaseUntil.Time) || time.Now().After(current.LeaseUntil.Time) {
			return nil
		}
		if code < 0 || code > 599 {
			return errors.New("invalid notification response code")
		}
		if e = q.FinishNotificationAttempt(ctx, database.FinishNotificationAttemptParams{OrgID: job.OrgID, ID: attempt, Status: status, ResponseCode: int32(code), Detail: detail}); e != nil {
			return e
		}
		delay := time.Minute * time.Duration(1<<min(job.RetryCount-1, 8))
		if e = q.FinishNotificationDelivery(ctx, database.FinishNotificationDeliveryParams{OrgID: job.OrgID, ID: job.ID, Status: status, Detail: detail, NextAttempt: dbTime(time.Now().Add(delay))}); e != nil {
			return e
		}
		return audit(ctx, q, job.OrgID, "system:notifications", "notification."+status, job.ID, map[string]any{"attempt": attempt, "response_code": code})
	})
}

func (s *Service) SetNotificationSubscriptions(ctx context.Context, req *connect.Request[pb.SetNotificationSubscriptionsRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if len(r.EventTypes) > len(notificationEventTypes) {
		return nil, invalid("Select supported notification events.")
	}
	for _, kind := range r.EventTypes {
		if !slices.Contains(notificationEventTypes, kind) {
			return nil, invalid("Select supported notification events.")
		}
	}
	kinds := append([]string{}, r.EventTypes...)
	slices.Sort(kinds)
	kinds = slices.Compact(kinds)
	err := s.transaction(ctx, func(q *database.Queries) error {
		if !canManageNotifications(ctx, q, r.OrganizationId) {
			return denied()
		}
		d, err := q.LockNotificationDestination(ctx, database.LockNotificationDestinationParams{OrgID: r.OrganizationId, ID: r.Id})
		if err != nil {
			return denied()
		}
		if r.ExpectedRevision <= 0 || r.ExpectedRevision != d.Revision {
			return conflict("Destination changed. Reload and review it before continuing.")
		}
		if err = q.SetNotificationSubscriptions(ctx, database.SetNotificationSubscriptionsParams{OrgID: d.OrgID, ID: d.ID, EventTypes: kinds}); err != nil {
			return err
		}
		return audit(ctx, q, d.OrgID, actor(ctx).Email, "notification.subscriptions_changed", d.ID, map[string]any{"event_types": kinds})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}

func (s *Service) GetNotificationDestination(ctx context.Context, r *connect.Request[pb.GetNotificationRecordRequest]) (*connect.Response[pb.NotificationDestination], error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(r.Msg.Id) {
		return nil, invalid("Invalid destination ID.")
	}
	d, err := s.q.GetNotificationDestinationMetadata(ctx, database.GetNotificationDestinationMetadataParams{OrgID: r.Msg.OrganizationId, ID: r.Msg.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, denied()
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.NotificationDestination{Id: d.ID, Name: d.Name, Kind: d.Kind, Endpoint: d.Endpoint, Enabled: d.Enabled, Verified: d.Verified, IncludeSuccess: d.IncludeSuccess, EventTypes: d.EventTypes, Revision: d.Revision}), nil
}
func (s *Service) GetNotificationDelivery(ctx context.Context, r *connect.Request[pb.GetNotificationRecordRequest]) (*connect.Response[pb.NotificationDelivery], error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}:[a-f0-9]{64}$`).MatchString(r.Msg.Id) {
		return nil, invalid("Invalid delivery ID.")
	}
	d, err := s.q.GetNotificationDeliveryMetadata(ctx, database.GetNotificationDeliveryMetadataParams{OrgID: r.Msg.OrganizationId, ID: r.Msg.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, denied()
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.NotificationDelivery{Id: d.ID, EventId: d.EventID, DestinationName: d.DestinationName, EventType: d.Type, Status: d.Status, Attempts: d.Attempts, Detail: d.Detail, UpdatedAt: stamp(d.UpdatedAt)}), nil
}

func (s *Service) DeleteNotificationDestination(ctx context.Context, r *connect.Request[pb.DeleteNotificationDestinationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	err := s.transaction(ctx, func(q *database.Queries) error {
		if !canManageNotifications(ctx, q, r.Msg.OrganizationId) {
			return denied()
		}
		d, err := q.LockNotificationDestination(ctx, database.LockNotificationDestinationParams{OrgID: r.Msg.OrganizationId, ID: r.Msg.Id})
		if err != nil {
			return denied()
		}
		if r.Msg.ExpectedRevision < 1 || d.Revision != r.Msg.ExpectedRevision {
			return conflict("Destination changed. Reload and review it before removing.")
		}
		if r.Msg.Confirmation != d.Name {
			return invalid("Enter the exact destination name.")
		}
		if err = q.DeleteNotificationDestination(ctx, database.DeleteNotificationDestinationParams{OrgID: d.OrgID, ID: d.ID}); err != nil {
			return err
		}
		if err = q.CancelRemovedDestinationDeliveries(ctx, database.CancelRemovedDestinationDeliveriesParams{OrgID: d.OrgID, DestinationID: d.ID}); err != nil {
			return err
		}
		if err = q.ClearDestinationChallenges(ctx, database.ClearDestinationChallengesParams{OrgID: d.OrgID, DestinationID: d.ID}); err != nil {
			return err
		}
		return audit(ctx, q, d.OrgID, actor(ctx).Email, "notification.destination_removed", d.ID, map[string]any{"name": d.Name, "kind": d.Kind})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}

func (s *Service) GetNotificationGrouping(ctx context.Context, req *connect.Request[pb.ListNotificationDestinationsRequest]) (*connect.Response[pb.NotificationGrouping], error) {
	row, err := s.q.GetNotificationGrouping(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.NotificationGrouping{Seconds: row.NotificationGroupSeconds, Revision: row.NotificationGroupRevision}), nil
}
func (s *Service) SetNotificationGrouping(ctx context.Context, req *connect.Request[pb.SetNotificationGroupingRequest]) (*connect.Response[pb.NotificationGrouping], error) {
	r := req.Msg
	if !slices.Contains([]int32{0, 60, 300, 900, 3600}, r.Seconds) {
		return nil, invalid("Choose every event or a 1, 5, 15 or 60 minute window.")
	}
	var out *pb.NotificationGrouping
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := s.accessLock(ctx, q, r.OrganizationId, "notifications.manage"); err != nil {
			return err
		}
		row, err := q.SetNotificationGrouping(ctx, database.SetNotificationGroupingParams{ID: r.OrganizationId, NotificationGroupSeconds: r.Seconds, NotificationGroupRevision: r.ExpectedRevision})
		if errors.Is(err, pgx.ErrNoRows) {
			return conflict("Notification grouping changed. Reload before saving.")
		}
		if err != nil {
			return err
		}
		out = &pb.NotificationGrouping{Seconds: row.NotificationGroupSeconds, Revision: row.NotificationGroupRevision}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "notification.grouping_changed", r.OrganizationId, map[string]any{"seconds": r.Seconds, "revision": row.NotificationGroupRevision})
	})
	return connect.NewResponse(out), err
}
