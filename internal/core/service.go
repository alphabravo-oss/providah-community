package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"github.com/alphabravo-oss/providah-community/internal/awsauth"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"math"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"filippo.io/age"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/internal/vaultstore"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	automationRuntimes []automation.Runtime
	stateSlots         chan struct{}

	telemetry *telemetry
	vault     *vaultstore.Store
	oidc      *oidcClient
	providahv1connect.UnimplementedConsoleServiceHandler
	pool       *pgxpool.Pool
	q          *database.Queries
	identity   *age.X25519Identity
	identities []age.Identity
	cfg        Config
	log        zerolog.Logger
}

func New(pool *pgxpool.Pool, cfg Config, log zerolog.Logger) (*Service, error) {
	if cfg.DatabaseBudgetBytes < 0 {
		return nil, errors.New("database budget must not be negative")
	}
	if cfg.DevelopmentCapture && (cfg.SecureCookies || !notification.DevelopmentOrigin(cfg.Origin)) {
		return nil, errors.New("development capture requires a local HTTP console origin")
	}
	automationRuntimes, runtimeErr := automation.ParseRuntimes(cfg.AutomationRuntimes)
	if runtimeErr != nil {
		return nil, runtimeErr
	}
	if len(cfg.ProviderRuntimes) > 0 {
		if err := provider.ValidateRuntimes(cfg.ProviderRuntimes); err != nil {
			return nil, err
		}
	}
	if len(cfg.BootstrapToken) < 32 || len(cfg.SessionKey) < 32 || cfg.Origin == "" {
		return nil, errors.New("bootstrap token and session key must have at least 32 characters; origin is required")
	}
	identity, err := age.ParseX25519Identity(cfg.AgeIdentity)
	if err != nil {
		return nil, errors.New("a valid persistent AGE_IDENTITY is required")
	}
	identities := []age.Identity{identity}
	seen := map[string]bool{identity.Recipient().String(): true}
	if len(cfg.AgePreviousIdentities) > 8 {
		return nil, errors.New("at most eight previous encryption keys are supported")
	}
	for _, raw := range cfg.AgePreviousIdentities {
		previous, e := age.ParseX25519Identity(raw)
		if e != nil {
			return nil, errors.New("invalid previous encryption key")
		}
		id := previous.Recipient().String()
		if seen[id] {
			return nil, errors.New("duplicate encryption key")
		}
		seen[id] = true
		identities = append(identities, previous)
	}
	s := &Service{stateSlots: make(chan struct{}, 4), automationRuntimes: automationRuntimes, telemetry: newTelemetry(), pool: pool, q: database.New(pool), identity: identity, identities: identities, cfg: cfg, log: log}
	s.registerPoolMetrics()
	if cfg.Vault != nil {
		s.vault, err = vaultstore.New(*cfg.Vault)
		if err != nil {
			return nil, err
		}
	}
	if cfg.OIDC != nil {
		s.oidc, err = newOIDCClient(cfg)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Service) transaction(ctx context.Context, fn func(*database.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func audit(ctx context.Context, q *database.Queries, org, who, action, target string, details map[string]any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	if err := q.AddAudit(ctx, database.AddAuditParams{OrgID: org, Actor: who, Action: action, Target: target, Details: b}); err != nil {
		return err
	}
	return enqueueNotification(ctx, q, org, action, target, details)
}
func (s *Service) limit(ctx context.Context, key string) error {
	n, err := s.q.RateLimit(ctx, key)
	if err != nil {
		return err
	}
	if n > 10 {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("Too many attempts. Try again in a minute."))
	}
	return nil
}
func (s *Service) bootstrap(ctx context.Context, token string) error {
	if !equal(token, s.cfg.BootstrapToken) {
		return denied()
	}
	n, err := s.q.UserCount(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return conflict("Setup is already complete.")
	}
	return nil
}
func (s *Service) SetupStatus(ctx context.Context, req *connect.Request[pb.SetupStatusRequest]) (*connect.Response[pb.SetupStatusResponse], error) {
	n, err := s.q.UserCount(ctx)
	return connect.NewResponse(&pb.SetupStatusResponse{Required: n == 0}), err
}
func (s *Service) BeginSetup(ctx context.Context, req *connect.Request[pb.BeginSetupRequest]) (*connect.Response[pb.BeginSetupResponse], error) {
	if err := s.limit(ctx, "setup"); err != nil {
		return nil, err
	}
	if err := s.bootstrap(ctx, req.Msg.Token); err != nil {
		return nil, err
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Providah", AccountName: "administrator"})
	if err != nil {
		return nil, err
	}
	cipher, err := s.seal(key.Secret())
	if err != nil {
		return nil, err
	}
	err = s.transaction(ctx, func(q *database.Queries) error {
		if err := q.LockSetup(ctx); err != nil {
			return err
		}
		n, err := q.UserCount(ctx)
		if err != nil {
			return err
		}
		if n != 0 {
			return conflict("Setup is already complete.")
		}
		return q.SetPendingSetup(ctx, cipher)
	})
	return connect.NewResponse(&pb.BeginSetupResponse{Secret: key.Secret(), Uri: key.URL()}), err
}
func checkTOTP(code, secret string) bool {
	valid, err := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	return err == nil && valid
}
func (s *Service) FinishSetup(ctx context.Context, req *connect.Request[pb.FinishSetupRequest]) (*connect.Response[pb.SessionResponse], error) {
	if err := s.limit(ctx, "setup"); err != nil {
		return nil, err
	}
	if err := s.bootstrap(ctx, req.Msg.Token); err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(req.Msg.Email))
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || len(email) > 254 {
		return nil, invalid("Enter a valid email address.")
	}
	if len(req.Msg.Password) < 12 || len(req.Msg.Password) > 72 {
		return nil, invalid("Use a password between 12 and 72 bytes.")
	}
	name := strings.TrimSpace(req.Msg.OrganizationName)
	if name == "" || len(name) > 120 {
		return nil, invalid("Organization name must be between 1 and 120 characters.")
	}
	password, err := bcrypt.GenerateFromPassword([]byte(req.Msg.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	uid, org, sid, refresh := randomID(), randomID(), randomID(), randomID()
	err = s.transaction(ctx, func(q *database.Queries) error {
		if err := q.LockSetup(ctx); err != nil {
			return err
		}
		n, err := q.UserCount(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return conflict("Setup is already complete.")
		}
		cipher, err := q.PendingSetup(ctx)
		if err != nil {
			return conflict("Begin setup again; enrollment has expired.")
		}
		secret, err := s.open(cipher)
		if err != nil {
			return err
		}
		if !checkTOTP(req.Msg.Code, secret) {
			return invalid("Authenticator code is invalid or expired.")
		}
		if err = q.CreateUser(ctx, database.CreateUserParams{ID: uid, Email: email, PasswordHash: password, TotpCiphertext: cipher, LastTotpStep: time.Now().Unix() / 30}); err != nil {
			return err
		}
		if err = q.CreateOrganization(ctx, database.CreateOrganizationParams{ID: org, Name: name}); err != nil {
			return err
		}
		if err = q.CreateBuiltinRoles(ctx, org); err != nil {
			return err
		}
		if err = q.JoinMembership(ctx, database.JoinMembershipParams{OrgID: org, UserID: uid, RoleID: pgtype.Text{String: "administrator", Valid: true}}); err != nil {
			return err
		}
		if err = q.CreateSession(ctx, database.CreateSessionParams{ID: sid, UserID: uid, RefreshHash: hash(refresh)}); err != nil {
			return err
		}
		if err = audit(ctx, q, org, email, "installation.initialized", org, map[string]any{"name": name}); err != nil {
			return err
		}
		return q.ClearSetup(ctx)
	})
	if err != nil {
		return nil, err
	}
	response, err := s.sessionResponse(ctx, uid, email)
	if err != nil {
		return nil, err
	}
	err = s.issue(response.Header(), sid, refresh)
	return response, err
}
func (s *Service) Login(ctx context.Context, req *connect.Request[pb.LoginRequest]) (*connect.Response[pb.SessionResponse], error) {
	email := strings.ToLower(strings.TrimSpace(req.Msg.Email))
	if err := s.limit(ctx, "login:"+hex.EncodeToString(hash(email))); err != nil {
		return nil, err
	}
	row, err := s.q.UserByEmail(ctx, email)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoO5G9v2pPjO84U8ZCVvWv4MtcC4KqBxGm"), []byte(req.Msg.Password))
		return nil, unauthenticated()
	}
	if bcrypt.CompareHashAndPassword(row.PasswordHash, []byte(req.Msg.Password)) != nil {
		return nil, unauthenticated()
	}
	if len(req.Msg.Password) > 72 || len(req.Msg.Code) > 6 || len(req.Msg.RecoveryCode) > 64 || (req.Msg.Code != "" && req.Msg.RecoveryCode != "") {
		return nil, unauthenticated()
	}
	sid, refresh := randomID(), randomID()
	mfaRequired := false
	err = s.transaction(ctx, func(q *database.Queries) error {
		locked, err := q.LockRecoveryUser(ctx, row.ID)
		if err != nil {
			return unauthenticated()
		}
		if !bytes.Equal(locked.PasswordHash, row.PasswordHash) {
			return unauthenticated()
		}
		if locked.MfaEnabled && req.Msg.Code == "" && req.Msg.RecoveryCode == "" {
			mfaRequired = true
			return nil
		}
		recovered := req.Msg.RecoveryCode != ""
		if recovered {
			verifier := recoveryVerifier(row.ID, req.Msg.RecoveryCode)
			if verifier == nil {
				return unauthenticated()
			}
			n, err := q.ConsumeRecoveryCode(ctx, database.ConsumeRecoveryCodeParams{UserID: row.ID, Verifier: verifier})
			if err != nil {
				return err
			}
			if n != 1 {
				return unauthenticated()
			}
			if err = q.DeleteUserSessions(ctx, row.ID); err != nil {
				return err
			}
		} else if locked.MfaEnabled {
			secret, err := s.open(locked.TotpCiphertext)
			if err != nil {
				return err
			}
			if !checkTOTP(req.Msg.Code, secret) {
				return unauthenticated()
			}
			n, err := q.ConsumeTOTP(ctx, database.ConsumeTOTPParams{ID: row.ID, LastTotpStep: time.Now().Unix() / 30})
			if err != nil {
				return err
			}
			if n != 1 {
				return unauthenticated()
			}
		}
		if err = q.CreateSession(ctx, database.CreateSessionParams{ID: sid, UserID: row.ID, RefreshHash: hash(refresh)}); err != nil {
			return err
		}
		if recovered || !locked.MfaEnabled {
			if err = q.RestrictRecoverySession(ctx, sid); err != nil {
				return err
			}
			if recovered {
				return accountAudit(ctx, q, row.ID, row.Email, "account.recovery_code_used")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if mfaRequired {
		return connect.NewResponse(&pb.SessionResponse{MfaRequired: true}), nil
	}
	response, err := s.sessionResponse(ctx, row.ID, row.Email)
	if err != nil {
		return nil, err
	}
	err = s.issue(response.Header(), sid, refresh)
	return response, err
}
func (s *Service) sessionResponse(ctx context.Context, uid, email string, identities ...pgtype.UUID) (*connect.Response[pb.SessionResponse], error) {
	identity := pgtype.UUID{}
	if len(identities) > 0 {
		identity = identities[0]
	}
	rows, err := s.q.OrganizationsForSession(ctx, database.OrganizationsForSessionParams{UserID: uid, OidcID: identity})
	if err != nil {
		return nil, err
	}
	user, e := s.q.UserByEmail(ctx, email)
	if e != nil || user.ID != uid {
		return nil, unauthenticated()
	}
	r := &pb.SessionResponse{Email: email, GlobalAdmin: user.GlobalAdmin, MfaEnabled: user.MfaEnabled}
	for _, o := range rows {
		if !o.Allowed {
			o.Permissions = []string{}
		}
		r.Organizations = append(r.Organizations, &pb.Organization{MfaRequired: o.MfaRequired && !user.MfaEnabled, SsoRequired: !o.Allowed && (!o.MfaRequired || user.MfaEnabled), Id: o.ID, Name: o.Name, Permissions: o.Permissions})
	}
	return connect.NewResponse(r), nil
}
func (s *Service) GetSession(ctx context.Context, req *connect.Request[pb.GetSessionRequest]) (*connect.Response[pb.SessionResponse], error) {
	p := actor(ctx)
	return s.sessionResponse(ctx, p.UserID, p.Email, p.OIDC)
}
func (s *Service) Refresh(ctx context.Context, req *connect.Request[pb.RefreshRequest]) (*connect.Response[pb.SessionResponse], error) {
	value := cookie(req.Header(), "providah_refresh")
	if value == "" {
		return nil, unauthenticated()
	}
	var id, uid, email string
	var identity pgtype.UUID
	refresh := randomID()
	err := s.transaction(ctx, func(q *database.Queries) error {
		row, err := q.SessionByRefresh(ctx, hash(value))
		if err != nil {
			return unauthenticated()
		}
		id, uid, email = row.ID, row.UserID, row.Email
		identity = row.OidcID
		return q.RotateSession(ctx, database.RotateSessionParams{ID: id, RefreshHash: hash(refresh)})
	})
	if err != nil {
		return nil, err
	}
	r, err := s.sessionResponse(ctx, uid, email, identity)
	if err != nil {
		return nil, err
	}
	return r, s.issue(r.Header(), id, refresh)
}
func (s *Service) Logout(ctx context.Context, req *connect.Request[pb.LogoutRequest]) (*connect.Response[pb.LogoutResponse], error) {
	err := s.q.DeleteSession(ctx, actor(ctx).ID)
	r := connect.NewResponse(&pb.LogoutResponse{})
	s.setCookie(r.Header(), "providah_access", "", -1)
	s.setCookie(r.Header(), "providah_refresh", "", -1)
	return r, err
}
func connection(r database.ListConnectionsRow) *pb.Connection {
	return &pb.Connection{CredentialSource: r.CredentialSource, Id: r.ID, OrganizationId: r.OrgID, Name: r.Name, Provider: r.Provider, Region: r.Region, Enabled: r.Enabled, CreatedAt: stamp(r.CreatedAt), ScanStatus: pb.ScanStatus(pb.ScanStatus_value["SCAN_STATUS_"+strings.ToUpper(r.ScanStatus)]), ScanError: r.ScanError, LastScanAt: stamp(r.LastScanAt)}
}
func (s *Service) ListConnections(ctx context.Context, req *connect.Request[pb.ListConnectionsRequest]) (*connect.Response[pb.ListConnectionsResponse], error) {
	rows, err := s.q.ListConnections(ctx, req.Msg.OrganizationId)
	r := &pb.ListConnectionsResponse{ExternalSecretsEnabled: s.vault != nil}
	for _, v := range rows {
		r.Connections = append(r.Connections, connection(v))
	}
	return connect.NewResponse(r), err
}
func (s *Service) GetConnection(ctx context.Context, req *connect.Request[pb.GetConnectionRequest]) (*connect.Response[pb.ConnectionResponse], error) {
	if !notificationRecordID.MatchString(req.Msg.Id) {
		return nil, invalid("Invalid connection ID.")
	}
	row, err := s.q.GetConnectionMetadata(ctx, database.GetConnectionMetadataParams{OrgID: req.Msg.OrganizationId, ID: req.Msg.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, denied()
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.ConnectionResponse{Connection: connection(database.ListConnectionsRow(row))}), nil
}
func validateCredential(provider, credential string) error {
	if provider != "aws" && provider != "digitalocean" && provider != "hetzner" {
		return invalid("Unsupported provider.")
	}
	ref, err := vaultstore.Parse(credential)
	if err != nil {
		return invalid("Invalid pinned external credential reference.")
	}
	if ref != nil {
		return nil
	}

	if len(credential) < 8 || len(credential) > 16384 {
		return invalid("Credential must be between 8 and 16384 bytes.")
	}
	if provider == "aws" {
		if _, err := awsauth.Parse(credential); err != nil {
			return invalid("Invalid AWS credentials or role configuration.")
		}
	} else if provider != "digitalocean" && provider != "hetzner" {
		return invalid("Unsupported provider.")
	}
	return nil
}
func (s *Service) CreateConnection(ctx context.Context, req *connect.Request[pb.CreateConnectionRequest]) (*connect.Response[pb.ConnectionResponse], error) {
	m := req.Msg
	name := strings.TrimSpace(m.Name)
	if name == "" || len(name) > 120 || len(m.Region) > 120 {
		return nil, invalid("Enter a name up to 120 characters and a valid region.")
	}
	prepared, err := s.prepareCredential(m.Provider, m.Credential)
	if err != nil {
		return nil, err
	}
	m.Credential = prepared
	if m.Provider == "aws" && m.Region == "" {
		m.Region = "us-east-1"
	}
	cipher, err := s.seal(m.Credential)
	if err != nil {
		return nil, err
	}
	var c *pb.Connection
	err = s.transaction(ctx, func(q *database.Queries) error {
		row, err := q.CreateConnection(ctx, database.CreateConnectionParams{CredentialSource: credentialSource(m.Credential), ID: randomID(), OrgID: m.OrganizationId, Name: name, Provider: m.Provider, Region: m.Region, KeyID: s.identity.Recipient().String(), Ciphertext: cipher})
		if err != nil {
			return err
		}
		c = connection(database.ListConnectionsRow(row))
		return audit(ctx, q, m.OrganizationId, actor(ctx).Email, "connection.created", c.Id, map[string]any{"name": name, "provider": m.Provider})
	})
	return connect.NewResponse(&pb.ConnectionResponse{Connection: c}), err
}
func (s *Service) SetConnectionEnabled(ctx context.Context, req *connect.Request[pb.SetConnectionEnabledRequest]) (*connect.Response[pb.ConnectionResponse], error) {
	m := req.Msg
	var c *pb.Connection
	err := s.transaction(ctx, func(q *database.Queries) error {
		row, err := q.SetConnectionEnabled(ctx, database.SetConnectionEnabledParams{OrgID: m.OrganizationId, ID: m.Id, Enabled: m.Enabled})
		if errors.Is(err, pgx.ErrNoRows) {
			return denied()
		}
		if err != nil {
			return err
		}
		c = connection(database.ListConnectionsRow(row))
		return audit(ctx, q, m.OrganizationId, actor(ctx).Email, "connection.enabled_changed", m.Id, map[string]any{"enabled": m.Enabled})
	})
	return connect.NewResponse(&pb.ConnectionResponse{Connection: c}), err
}
func (s *Service) RotateCredential(ctx context.Context, req *connect.Request[pb.RotateCredentialRequest]) (*connect.Response[pb.ConnectionResponse], error) {
	m := req.Msg
	rows, err := s.q.ListConnections(ctx, m.OrganizationId)
	if err != nil {
		return nil, err
	}
	provider := ""
	for _, r := range rows {
		if r.ID == m.Id {
			provider = r.Provider
		}
	}
	if provider == "" {
		return nil, denied()
	}
	m.Credential, err = s.prepareCredential(provider, m.Credential)
	if err != nil {
		return nil, err
	}
	cipher, err := s.seal(m.Credential)
	if err != nil {
		return nil, err
	}
	var c *pb.Connection
	err = s.transaction(ctx, func(q *database.Queries) error {
		row, err := q.RotateCredential(ctx, database.RotateCredentialParams{CredentialSource: credentialSource(m.Credential), OrgID: m.OrganizationId, ID: m.Id, Ciphertext: cipher, KeyID: s.identity.Recipient().String()})
		if err != nil {
			return err
		}
		c = connection(database.ListConnectionsRow(row))
		return audit(ctx, q, m.OrganizationId, actor(ctx).Email, "connection.credential_rotated", m.Id, map[string]any{})
	})
	return connect.NewResponse(&pb.ConnectionResponse{Connection: c}), err
}
func (s *Service) ListResources(ctx context.Context, req *connect.Request[pb.ListResourcesRequest]) (*connect.Response[pb.ListResourcesResponse], error) {
	m := req.Msg
	if e := validateResourceScope(m.ConnectionId, m.Region, m.Status); e != nil {
		return nil, e
	}
	if e := validateTagFilter(m.TagKey, m.TagValue, m.TagName, m.TagExists); e != nil {
		return nil, e
	}
	if e := validateTagConditions(m.TagConditions, m.TagMatchAny, m.TagKey, m.TagName); e != nil {
		return nil, e
	}
	if len(m.Search) > 200 || len(m.PageToken) > 8192 || len(m.Kind) > 80 || len(m.ConnectionId) > 64 {
		return nil, invalid("Query is too long.")
	}
	switch m.SortBy {
	case "", "id", "name", "provider", "kind", "region", "status":
	default:
		return nil, invalid("Unsupported inventory sort.")
	}
	filter := base64Filter(m.Provider, m.Search) + ":" + strconv.Quote(m.Kind) + ":" + strconv.Quote(m.ConnectionId)
	if m.SortBy != "" || m.Descending {
		filter += ":" + m.SortBy + ":" + strconv.FormatBool(m.Descending)
	}
	if m.TagKey != "" || m.TagName != "" {
		filter += ":tags:" + strconv.Quote(m.TagKey) + ":" + strconv.Quote(m.TagValue) + ":" + strconv.Quote(m.TagName) + ":" + strconv.FormatBool(m.TagExists)
	}
	if len(m.TagConditions) > 0 {
		digest := sha256.Sum256(tagConditionsJSON(m.TagConditions))
		filter += ":conditions:" + hex.EncodeToString(digest[:]) + strconv.FormatBool(m.TagMatchAny)
	}
	if m.Region != "" || m.Status != "" {
		filter += ":scope:" + strconv.Quote(m.Region) + ":" + strconv.Quote(m.Status)
	}
	after, err := parseCursor(m.PageToken, m.OrganizationId, filter)
	if err != nil {
		return nil, err
	}
	afterValue := after
	if after != "" && (m.SortBy != "" || m.Descending) {
		var boundary []string
		if json.Unmarshal([]byte(after), &boundary) != nil || len(boundary) != 2 || len(boundary[0]) > 512 || len(boundary[1]) != 64 {
			return nil, invalid("Invalid inventory page token.")
		}
		afterValue, after = boundary[0], boundary[1]
	}
	size := m.PageSize
	if size <= 0 {
		size = 50
	}
	if size > 200 {
		size = 200
	}
	rows, err := s.q.ListResources(ctx, database.ListResourcesParams{FilterRegion: m.Region, FilterStatus: m.Status, TagConditions: tagConditionsJSON(m.TagConditions), TagMatchAny: m.TagMatchAny, TagKey: m.TagKey, TagValue: m.TagValue, TagName: m.TagName, TagExists: m.TagExists, ConnectionID: m.ConnectionId, Kind: m.Kind, OrgID: m.OrganizationId, Provider: m.Provider, Search: m.Search, AfterID: after, AfterValue: afterValue, SortBy: m.SortBy, Descending: m.Descending, PageSize: size + 1})
	if err != nil {
		return nil, err
	}
	r := &pb.ListResourcesResponse{}
	if len(rows) > int(size) {
		rows = rows[:size]
		last := rows[len(rows)-1]
		boundary := last.ID
		if m.SortBy != "" || m.Descending {
			encoded, _ := json.Marshal([]string{last.SortValue, last.ID})
			boundary = string(encoded)
		}
		r.NextPageToken = cursor(m.OrganizationId, filter, boundary)
	}
	for _, v := range rows {
		r.Resources = append(r.Resources, &pb.Resource{Tags: resourceTags(v.TagMetadata), Id: v.ID, ConnectionId: v.ConnectionID, NativeId: v.NativeID, Name: v.Name, Provider: v.Provider, Kind: v.Kind, Region: v.Region, Status: v.Status, ObservedAt: stamp(v.ObservedAt), PublicIp: v.PublicIp, PrivateIp: v.PrivateIp, Size: v.Size})
	}
	return connect.NewResponse(r), nil
}
func base64Filter(a, b string) string { return strconv.Quote(a) + ":" + strconv.Quote(b) }
func (s *Service) ListAudit(ctx context.Context, req *connect.Request[pb.ListAuditRequest]) (*connect.Response[pb.ListAuditResponse], error) {
	return s.listAudit(ctx, req, false)
}
func (s *Service) ListInstallationAudit(ctx context.Context, req *connect.Request[pb.ListAuditRequest]) (*connect.Response[pb.ListAuditResponse], error) {
	if e := s.installationReadAccess(ctx); e != nil {
		return nil, e
	}
	if req.Msg.Source != "installation" && req.Msg.Source != "organizations" {
		return nil, invalid("Choose an audit source.")
	}
	if len(req.Msg.OrganizationId) > 64 || (req.Msg.Source == "installation" && req.Msg.OrganizationId != "") {
		return nil, invalid("Choose a valid organization scope.")
	}
	return s.listAudit(ctx, req, true)
}
func (s *Service) listAudit(ctx context.Context, req *connect.Request[pb.ListAuditRequest], installation bool) (*connect.Response[pb.ListAuditResponse], error) {
	m := req.Msg
	if len(m.Actor) > 320 || len(m.Action) > 200 || len(m.Target) > 512 || len(m.PageToken) > 8192 {
		return nil, invalid("Audit filter is too long.")
	}
	parseTime := func(value string) (pgtype.Timestamptz, error) {
		if value == "" {
			return pgtype.Timestamptz{}, nil
		}
		t, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || t.Year() < 1 || t.Year() > 9999 {
			return pgtype.Timestamptz{}, invalid("Use a valid RFC3339 timestamp with a timezone.")
		}
		return pgtype.Timestamptz{Time: t, Valid: true}, nil
	}
	from, err := parseTime(m.OccurredFrom)
	if err != nil {
		return nil, err
	}
	until, err := parseTime(m.OccurredBefore)
	if err != nil {
		return nil, err
	}
	if from.Valid && until.Valid && !from.Time.Before(until.Time) {
		return nil, invalid("The end must be after the start.")
	}
	filter := "audit"
	if installation {
		filter = "installation-audit:" + m.Source
	}
	if m.Actor != "" || m.Action != "" || m.Target != "" || m.OccurredFrom != "" || m.OccurredBefore != "" {
		encoded, _ := json.Marshal([]string{m.Actor, m.Action, m.Target, m.OccurredFrom, m.OccurredBefore})
		filter += string(encoded)
	}
	id, err := parseCursor(m.PageToken, m.OrganizationId, filter)
	if err != nil {
		return nil, err
	}
	before := int64(math.MaxInt64)
	if id != "" {
		before, err = strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, invalid("Invalid page token.")
		}
	}
	if installation {
		var rows []database.ListInstallationAuditRow
		var e error
		if m.Source == "installation" {
			rows, e = s.q.ListInstallationAudit(ctx, database.ListInstallationAuditParams{BeforeID: before, Actor: m.Actor, Action: m.Action, Target: m.Target, OccurredFrom: from, OccurredBefore: until})
		} else {
			var events []database.ListGlobalOrganizationAuditRow
			events, e = s.q.ListGlobalOrganizationAudit(ctx, database.ListGlobalOrganizationAuditParams{BeforeID: before, OrgID: m.OrganizationId, Actor: m.Actor, Action: m.Action, Target: m.Target, OccurredFrom: from, OccurredBefore: until})
			for _, v := range events {
				rows = append(rows, database.ListInstallationAuditRow(v))
			}
		}
		if e != nil {
			return nil, e
		}
		out := &pb.ListAuditResponse{}
		if len(rows) > 100 {
			rows = rows[:100]
			out.NextPageToken = cursor(m.OrganizationId, filter, strconv.FormatInt(rows[99].ID, 10))
		}
		for _, v := range rows {
			out.Events = append(out.Events, &pb.AuditEvent{Id: strconv.FormatInt(v.ID, 10), Actor: v.Actor, Action: v.Action, Target: v.Target, Details: string(v.Details), OccurredAt: stamp(v.OccurredAt), OrganizationId: v.OrgID, OrganizationName: v.OrganizationName})
		}
		return connect.NewResponse(out), nil
	}
	rows, err := s.q.ListAudit(ctx, database.ListAuditParams{OrgID: m.OrganizationId, ID: before, Actor: m.Actor, Action: m.Action, Target: m.Target, OccurredFrom: from, OccurredBefore: until})
	if err != nil {
		return nil, err
	}
	r := &pb.ListAuditResponse{}
	if len(rows) > 100 {
		rows = rows[:100]
		r.NextPageToken = cursor(m.OrganizationId, filter, strconv.FormatInt(rows[99].ID, 10))
	}
	for _, v := range rows {
		r.Events = append(r.Events, &pb.AuditEvent{Id: strconv.FormatInt(v.ID, 10), Actor: v.Actor, Action: v.Action, Target: v.Target, OccurredAt: stamp(v.OccurredAt), Details: string(v.Details)})
	}
	return connect.NewResponse(r), nil
}
