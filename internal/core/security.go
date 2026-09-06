package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alphabravo-oss/providah-community/internal/artifactstore"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"io"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"filippo.io/age"
	"github.com/alphabravo-oss/providah-community/internal/auditstore"
	"github.com/alphabravo-oss/providah-community/internal/database"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"github.com/alphabravo-oss/providah-community/internal/vaultstore"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Config struct {
	Edition             string
	RuntimePolicy       func(provider.Runtime) provider.Runtime
	DatabaseBudgetBytes int64
	Artifacts           *artifactstore.Store
	DevelopmentCapture  bool
	AutomationCall      func(context.Context, automation.Request) (automation.Result, error)
	AutomationRuntimes  string

	Vault                                           *vaultstore.Config
	OIDC                                            *OIDCConfig
	OIDCHTTPClient                                  *http.Client // Test-only transport injection.
	AgePreviousIdentities                           []string
	AWSHTTPClient                                   *http.Client // Optional transport for local SDK tests.
	Origin, BootstrapToken, SessionKey, AgeIdentity string
	SMTP                                            *notification.SMTP
	ProviderRuntimes                                []provider.Runtime
	SecureCookies                                   bool
	AuditExportWrite                                func(context.Context, auditstore.Request) error
	NotificationSend                                func(context.Context, notification.Message) (int, error)
	ProviderCall                                    func(context.Context, provider.Request) (provider.Response, error)
}
type principal struct {
	MFADisabled       bool
	OIDC              pgtype.UUID
	ID, UserID, Email string
	MFAAt             time.Time
}
type principalKey struct{}

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hash(s string) []byte   { h := sha256.Sum256([]byte(s)); return h[:] }
func equal(a, b string) bool { return subtle.ConstantTimeCompare(hash(a), hash(b)) == 1 }
func (s *Service) seal(value string) ([]byte, error) {
	return encryptSecret(value, s.identity.Recipient())
}
func encryptSecret(value string, recipient age.Recipient) ([]byte, error) {
	key, ok := recipient.(*age.X25519Recipient)
	if !ok {
		return nil, errors.New("unsupported encryption recipient")
	}
	var b bytes.Buffer
	w, err := age.Encrypt(&b, recipient)
	if err != nil {
		return nil, err
	}
	if _, err = io.WriteString(w, value); err != nil {
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	return json.Marshal(secretEnvelope{KeyID: key.String(), Algorithm: "age-x25519", Ciphertext: b.Bytes()})
}

type secretEnvelope struct {
	KeyID      string `json:"key_id"`
	Algorithm  string `json:"algorithm"`
	Ciphertext []byte `json:"ciphertext"`
}

func (s *Service) open(value []byte) (string, error) { return s.openBounded(value, 65536) }
func (s *Service) openBounded(value []byte, limit int64) (string, error) {
	identities := s.identities
	if len(identities) == 0 {
		identities = []age.Identity{s.identity}
	}
	return decryptBounded(value, limit, identities...)
}
func decryptSecret(value []byte, identities ...age.Identity) (string, error) {
	return decryptBounded(value, 65536, identities...)
}
func decryptBounded(value []byte, limit int64, identities ...age.Identity) (string, error) {
	var envelope secretEnvelope
	if json.Unmarshal(value, &envelope) != nil || envelope.Algorithm != "age-x25519" || envelope.KeyID == "" {
		return "", errors.New("invalid secret envelope; run offline encryption maintenance for older data")
	}
	for _, identity := range identities {
		if key, ok := identity.(*age.X25519Identity); ok && key.Recipient().String() == envelope.KeyID {
			return decryptAge(envelope.Ciphertext, limit, key)
		}
	}
	return "", errors.New("unknown encryption key")
}

func decryptAge(value []byte, limit int64, identities ...age.Identity) (string, error) {
	r, err := age.Decrypt(bytes.NewReader(value), identities...)
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(b)) > limit {
		return "", errors.New("secret too large")
	}
	return string(b), err
}
func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, errors.New(msg)) }
func denied() error {
	return connect.NewError(connect.CodePermissionDenied, errors.New("You do not have access to this action."))
}
func unauthenticated() error {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("Sign in with your password and authenticator code."))
}
func conflict(msg string) error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New(msg))
}
func actor(ctx context.Context) principal { p, _ := ctx.Value(principalKey{}).(principal); return p }
func cookie(h http.Header, name string) string {
	r := http.Request{Header: h}
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
func (s *Service) setCookie(h http.Header, name, value string, seconds int) {
	h.Add("Set-Cookie", (&http.Cookie{Name: name, Value: value, Path: "/api/", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: seconds}).String())
}
func (s *Service) issue(h http.Header, id, refresh string) error {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: id, Issuer: "providah", Audience: jwt.ClaimStrings{"providah-console"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute))})
	signed, err := token.SignedString([]byte(s.cfg.SessionKey))
	if err != nil {
		return err
	}
	s.setCookie(h, "providah_access", signed, 900)
	s.setCookie(h, "providah_refresh", refresh, 30*24*3600)
	return nil
}
func (s *Service) authenticate(ctx context.Context, h http.Header) (principal, error) {
	claims := new(jwt.RegisteredClaims)
	_, err := jwt.ParseWithClaims(cookie(h, "providah_access"), claims, func(t *jwt.Token) (any, error) { return []byte(s.cfg.SessionKey), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("providah"), jwt.WithAudience("providah-console"), jwt.WithExpirationRequired())
	if err != nil {
		return principal{}, unauthenticated()
	}
	row, err := s.q.GetSession(ctx, claims.Subject)
	if err != nil {
		return principal{}, unauthenticated()
	}
	return principal{MFADisabled: !row.MfaEnabled, ID: row.ID, UserID: row.UserID, Email: row.Email, MFAAt: row.MfaAt.Time, OIDC: row.OidcID}, nil
}

// All RPC authorization is enforced here, including calls made outside the UI.
func (s *Service) Interceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (resp connect.AnyResponse, err error) {
			defer func() {
				if err != nil {
					if errors.Is(err, context.Canceled) {
						err = connect.NewError(connect.CodeCanceled, errors.New("Request canceled."))
						return
					}
					if errors.Is(err, context.DeadlineExceeded) {
						err = connect.NewError(connect.CodeDeadlineExceeded, errors.New("Request timed out."))
						return
					}
					var ce *connect.Error
					if !errors.As(err, &ce) {
						s.log.Error().Str("procedure", req.Spec().Procedure).Msg("request failed")
						err = connect.NewError(connect.CodeInternal, errors.New("The request could not be completed."))
					}
				}
			}()
			method := req.Spec().Procedure[strings.LastIndex(req.Spec().Procedure, "/")+1:]
			switch method {
			case "SetupStatus", "BeginSetup", "FinishSetup", "Login", "Refresh", "BeginInvitation", "AcceptInvitation", "GetOIDCStatus", "BeginOIDCLogin", "CompleteOIDCLogin":
				return next(ctx, req)
			}
			p, err := s.authenticate(ctx, req.Header())
			if err != nil {
				return nil, err
			}
			ctx = context.WithValue(ctx, principalKey{}, p)
			permission := ""
			stepUp := false
			switch method {
			case "ListProjectOwnership", "GetAutomationSource", "GetServerTemplate", "GetAutomationVersion", "GetAutomationProject", "ListAutomationProjects", "ListAutomationStates", "ListServerTemplates", "ListAutomationSources", "ListAutomationVersions", "ListAutomationValidations", "GetAutomationValidation":
				permission = "templates.read"
			case "CreateAutomationProject", "PublishServerTemplate", "SetServerTemplateStatus", "ImportAutomationSource", "PublishAutomationVersion", "SetAutomationVersionStatus", "RequestAutomationValidation", "CancelAutomationValidation":
				permission = "templates.publish"
				stepUp = true
			case "GetResourcePolicy":
				permission = "roles.manage"
			case "SaveResourcePolicy":
				permission = "roles.manage"
				stepUp = true
			case "GetIdentityPolicy":
				permission = "identity.manage"
			case "SaveIdentityPolicy":
				permission = "identity.manage"
				stepUp = true
			case "ListProviderModules":
				permission = "modules.read"
			case "SetProviderModule", "SetProviderRuntime":
				permission = "modules.manage"
				stepUp = true
			case "ListConnections", "GetConnection":
				permission = "connections.read"
			case "CreateConnection", "RotateCredential":
				permission = "connections.manage"
				stepUp = true
			case "SetConnectionEnabled", "RefreshConnection":
				permission = "connections.manage"
			case "ListDashboardTeams":
				permission = "dashboards.manage"
			case "ListResourceScopes", "GetResourceSummary", "ListDashboards", "SaveDashboard", "DeleteDashboard", "ListResources", "GetResource", "GetResourceMetrics", "ListInventoryViews", "SaveInventoryView", "DeleteInventoryView":
				permission = "resources.read"
			case "SaveAuditExport", "TestAuditExport", "SetAuditExportEnabled", "RetryAuditExport":
				permission = "audit.export.manage"
				stepUp = true
			case "ListAudit", "GetAuditExport", "ListAuditExportBatches":
				permission = "audit.read"
			case "ListMaintenancePolicies":
				permission = "maintenance.read"
			case "SaveMaintenancePolicy", "DeleteMaintenancePolicy":
				permission = "maintenance.manage"
				stepUp = true
			case "GetNotificationDelivery", "ListNotificationDeliveries", "ListNotificationAttempts":
				permission = "notifications.read"
			case "GetOrganizationSmtp", "GetNotificationGrouping", "GetNotificationDestination", "ListNotificationDestinations":
				permission = "notifications.manage"
			case "SetOrganizationSmtp", "DeleteNotificationDestination", "SetNotificationSubscriptions", "SetNotificationGrouping", "SaveNotificationDestination", "SetNotificationDestinationEnabled", "SendNotificationTest", "VerifyNotificationDestination", "RedeliverNotification":
				permission = "notifications.manage"
				stepUp = true
			case "GetSchedule", "ListSchedules", "ListScheduleOccurrences", "PreviewSchedule":
				permission = "schedules.read"
			case "SaveSchedule", "SetScheduleEnabled", "DeleteSchedule":
				permission = "schedules.manage"
				stepUp = true
			case "ApproveSchedule":
				permission = "operations.approve"
				stepUp = true
			case "GetOperationsOverview", "ListOperations", "GetOperation":
				permission = "operations.read"
			case "RequestOperation", "PreviewDeletion", "RequestServerCreation", "RequestSSHKeyCreation", "PreviewBulkPower", "RequestBulkPower":
				permission = "operations.request"
				stepUp = true
			case "CancelOperation", "ReconcileOperation":
				permission = "operations.request"
			case "ReviewOperation", "ResolveOperation":
				permission = "operations.approve"
				stepUp = true
			case "ListAccess", "ListTeams":
				permission = "members.read"
			case "SaveRole":
				permission = "roles.manage"
				stepUp = true
			case "DeleteTeam", "SaveTeam", "UpdateMember", "CreateInvitation", "RevokeInvitation":
				permission = "members.manage"
				stepUp = true
			case "GetInstallationHealth", "SetInstallationOrganization", "ListInstallationAudit", "SetInstallationUser", "ListInstallationDirectory", "BeginMFAEnrollment", "GetMFAPolicy", "SaveMFAPolicy", "CreateOrganization", "SetAccountMFA", "ListAccountSessions", "RevokeAccountSession", "GetSession", "Logout", "VerifyMfa", "GetAccountSecurity", "GenerateRecoveryCodes", "ChangePassword", "BeginOIDCLink", "UnlinkOIDC":
				return next(ctx, req)
			default:
				return nil, denied()
			}
			scoped, ok := req.Any().(interface{ GetOrganizationId() string })
			if !ok || scoped.GetOrganizationId() == "" {
				return nil, invalid("Choose an organization.")
			}
			if !identityAllowed(ctx, s.q, scoped.GetOrganizationId(), p.UserID, p.OIDC) {
				return nil, denied()
			}
			permissions, err := s.q.Permissions(ctx, database.PermissionsParams{OrgID: scoped.GetOrganizationId(), UserID: p.UserID})
			if err != nil {
				return nil, denied()
			}
			allowed := false
			for _, v := range permissions {
				if v == permission {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, denied()
			}
			if stepUp && !p.MFADisabled && time.Since(p.MFAAt) > 5*time.Minute {
				return nil, conflict("Verify MFA before performing this sensitive action.")
			}
			return next(ctx, req)
		}
	})
}
func stamp(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}
func cursor(org, filter, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s\n%s\n%s", org, filter, id)))
}
func parseCursor(token, org, filter string) (string, error) {
	if token == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", invalid("Invalid page token.")
	}
	p := strings.Split(string(b), "\n")
	if len(p) != 3 || p[0] != org || p[1] != filter {
		return "", invalid("Page token does not match this query.")
	}
	return p[2], nil
}
