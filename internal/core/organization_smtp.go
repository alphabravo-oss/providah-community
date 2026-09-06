package core

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"github.com/jackc/pgx/v5"
)

func (s *Service) GetOrganizationSmtp(ctx context.Context, r *connect.Request[pb.ListNotificationDestinationsRequest]) (*connect.Response[pb.OrganizationSmtp], error) {
	row, err := s.q.GetOrganizationSMTPMetadata(ctx, r.Msg.OrganizationId)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, denied()
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.OrganizationSmtp{Configured: row.Configured, Revision: row.Revision}), nil
}
func (s *Service) SetOrganizationSmtp(ctx context.Context, r *connect.Request[pb.SetOrganizationSmtpRequest]) (*connect.Response[pb.OrganizationSmtp], error) {
	v := r.Msg
	if v.Remove == (v.Smtp != nil) {
		return nil, invalid("Provide SMTP settings or remove the profile.")
	}
	var ciphertext []byte
	if !v.Remove {
		smtp := v.Smtp
		secret := notification.Secret{SMTP: &notification.SMTP{Host: smtp.Host, Port: smtp.Port, Sender: smtp.Sender, Username: smtp.Username, Password: smtp.Password}}
		validate := notification.Validate
		if s.cfg.DevelopmentCapture {
			validate = notification.ValidateDevelopment
		}
		if err := validate("email", smtp.Sender, secret); err != nil {
			return nil, invalid(err.Error())
		}
		raw, err := json.Marshal(secret)
		if err != nil {
			return nil, err
		}
		ciphertext, err = s.seal(string(raw))
		if err != nil {
			return nil, err
		}
	}
	var out *pb.OrganizationSmtp
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := s.accessLock(ctx, q, v.OrganizationId, "notifications.manage"); err != nil {
			return err
		}
		current, err := q.GetOrganizationSMTPMetadata(ctx, v.OrganizationId)
		if err != nil {
			return err
		}
		if current.Revision != v.ExpectedRevision {
			return conflict("Organization SMTP changed. Reload before saving.")
		}
		revision, err := q.SetOrganizationSMTP(ctx, database.SetOrganizationSMTPParams{OrgID: v.OrganizationId, Ciphertext: ciphertext})
		if err != nil {
			return err
		}
		out = &pb.OrganizationSmtp{Configured: !v.Remove, Revision: revision}
		return audit(ctx, q, v.OrganizationId, actor(ctx).Email, "notification.smtp_changed", v.OrganizationId, map[string]any{"configured": !v.Remove, "revision": revision})
	})
	return connect.NewResponse(out), err
}
