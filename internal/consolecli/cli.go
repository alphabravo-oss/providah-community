// Package consolecli uses the public Connect API; it has no database or provider access.
package consolecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/term"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const help = `providahctl <session|connections|resources|resource|resource-summary|views|dashboards|operation|operations|audit|schedules|schedule|schedule-history|modules|teams|access> [flags]
Required: --url https://console.example.com; --org ID except for session.
Authentication: --email USER (hidden terminal password/TOTP prompts), or --auth-stdin
with one JSON object {"email":"...","password":"...","code":"..."} on stdin.
Output: --json for protobuf JSON; default is a terminal-safe table.
Operations: preview-deletion, request-operation, request-server, request-ssh-key, review-operation,
cancel-operation, reconcile-operation, resolve-operation. Use --help after a command.
Access: save-role, update-member, save-team, delete-team (--input FILE).
Resource scopes: save-view, delete-view, save-dashboard, delete-dashboard (--input FILE).
Schedules: preview-schedule, save-schedule, approve-schedule, set-schedule-enabled,
delete-schedule. Each accepts --input FILE with its public API request JSON.
Notifications: destinations, destination (--id), deliveries (--page/--all),
delivery (--id), delivery-attempts (--id), notification-grouping.
Configuration: set-notification-grouping, set-notification-subscriptions,
delete-destination, set-destination-enabled (--input FILE with reviewed revision).
Destination setup: save-destination, organization-smtp, set-organization-smtp.
Delivery actions: send-notification-test, verify-destination, redeliver-notification
(--input FILE). Test/redelivery requests can send messages; they never auto-retry.
Automation reads: automation-projects, automation-project (--id), project-ownership
and project-states (--id, --page/--all), validations (--status, --page/--all),
validation (--id). State commands return metadata only.
Automation versions: automation-versions, automation-version (--id).
Automation actions: create-automation-project, request-validation, cancel-validation
(--input FILE). Validation does not apply cloud infrastructure.
Automation sources: automation-sources, automation-source (--id),
import-automation-source (--input metadata.json --archive source.zip),
publish-automation-version, set-automation-version-status (--input FILE).
Native templates: server-templates, server-template (--id), publish-server-template
and set-server-template-status (--input FILE). Publication does not launch a server.
No credentials or sessions are saved.
`

func endpoint(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid endpoint")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || u.Hostname() != "localhost" && !net.ParseIP(u.Hostname()).IsLoopback()) {
		return nil, errors.New("HTTPS required except on loopback")
	}
	u.Path = ""
	return u, nil
}

func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, _ = fmt.Fprint(out, help)
		return 0
	}
	command := args[0]
	allowed := map[string]string{"server-templates": "", "server-template": " id", "publish-server-template": " input", "set-server-template-status": " input", "automation-sources": "", "automation-source": " id", "import-automation-source": " input archive", "publish-automation-version": " input", "set-automation-version-status": " input", "automation-versions": "", "automation-version": " id", "create-automation-project": " input", "request-validation": " input", "cancel-validation": " input", "automation-projects": "", "automation-project": " id", "project-ownership": " id page all", "project-states": " id page all", "validations": " page all status", "validation": " id", "organization-smtp": "", "save-destination": " input", "set-organization-smtp": " input", "send-notification-test": " input", "verify-destination": " input", "redeliver-notification": " input", "set-destination-enabled": " input", "notification-grouping": "", "set-notification-grouping": " input", "set-notification-subscriptions": " input", "delete-destination": " input", "destinations": "", "destination": " id", "deliveries": " page all", "delivery": " id", "delivery-attempts": " id", "preview-deletion": " id", "request-operation": " input", "request-server": " input", "request-ssh-key": " input", "review-operation": " input", "resolve-operation": " input", "cancel-operation": " id", "reconcile-operation": " id", "session": "", "connections": "", "resources": " connection kind provider search page sort descending all filters", "resource": " id", "operation": " id wait", "operations": " page all filters", "audit": " page actor action target from before all", "schedules": "", "schedule": " id", "schedule-history": " schedule outcome page all", "modules": "", "views": "", "dashboards": "", "resource-summary": " filters group-by", "preview-schedule": " input", "save-schedule": " input", "approve-schedule": " input", "set-schedule-enabled": " input", "delete-schedule": " input", "save-view": " input", "delete-view": " input", "save-dashboard": " input", "delete-dashboard": " input", "teams": "", "access": "", "save-team": " input", "delete-team": " input", "save-role": " input", "update-member": " input"}
	extra, exists := allowed[command]
	if !exists {
		_, _ = fmt.Fprintln(errOut, "Unknown command. Use --help.")
		return 2
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(errOut)
	address := fs.String("url", "", "Console origin; HTTPS required except on loopback")
	org := fs.String("org", "", "Organization ID")
	email := fs.String("email", "", "Email for interactive login")
	stdinAuth := fs.Bool("auth-stdin", false, "Read local login JSON from stdin")
	jsonOutput := fs.Bool("json", false, "Write protobuf JSON")
	wait := fs.Duration("wait", 0, "Wait up to this duration for operation completion or uncertainty (maximum 5m)")
	all := fs.Bool("all", false, "Collect all remaining pages in one session (bounded)")
	values := map[string]*string{}
	for _, name := range []string{"connection", "kind", "provider", "search", "page", "id", "actor", "action", "target", "from", "before", "sort", "input", "schedule", "outcome", "filters", "group-by", "status", "archive"} {
		values[name] = fs.String(name, "", "Optional "+name+" filter/context")
	}
	descending := fs.Bool("descending", false, "Descending resource sort")
	if e := fs.Parse(args[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	bad := fs.NArg() != 0
	fs.Visit(func(f *flag.Flag) {
		if !strings.Contains(" url org email auth-stdin json "+extra+" ", " "+f.Name+" ") {
			bad = true
		}
	})
	base, e := endpoint(*address)
	if *values["outcome"] != "" && *values["outcome"] != "queued" && *values["outcome"] != "skipped" {
		bad = true
	}
	if bad || e != nil || *wait < 0 || *wait > 5*time.Minute || (command != "session" && *org == "") || (strings.Contains(" resource operation schedule preview-deletion cancel-operation reconcile-operation destination delivery delivery-attempts automation-project project-ownership project-states validation automation-version automation-source server-template ", " "+command+" ") && *values["id"] == "") || (*stdinAuth && *email != "") {
		_, _ = fmt.Fprintln(errOut, "Invalid flags, endpoint or missing organization/resource ID. Use command --help.")
		return 2
	}
	var filters *pb.InventoryViewSpec
	if *values["filters"] != "" {
		conflict := false
		fs.Visit(func(f *flag.Flag) {
			if strings.Contains(" connection kind provider search sort descending ", " "+f.Name+" ") {
				conflict = true
			}
		})
		data, err := inputFile(*values["filters"])
		filters = &pb.InventoryViewSpec{}
		if conflict || err != nil || protojson.Unmarshal(data, filters) != nil {
			_, _ = fmt.Fprintln(errOut, "Invalid filter file or conflicting filter flags. No request sent.")
			return 2
		}
	}
	if command == "resource-summary" {
		if filters == nil {
			filters = &pb.InventoryViewSpec{}
		}
		if *values["group-by"] == "" {
			*values["group-by"] = "provider"
		}
		if group := *values["group-by"]; group != "provider" && group != "kind" && group != "status" {
			_, _ = fmt.Fprintln(errOut, "Summary group must be provider, kind or status.")
			return 2
		}
	}
	var readCode func() (string, error)
	var mutation proto.Message
	switch command {
	case "publish-server-template":
		mutation = &pb.PublishServerTemplateRequest{}
	case "set-server-template-status":
		mutation = &pb.SetServerTemplateStatusRequest{}
	case "import-automation-source":
		mutation = &pb.ImportAutomationSourceRequest{}
	case "publish-automation-version":
		mutation = &pb.PublishAutomationVersionRequest{}
	case "set-automation-version-status":
		mutation = &pb.SetAutomationVersionStatusRequest{}

	case "create-automation-project":
		mutation = &pb.CreateAutomationProjectRequest{}
	case "request-validation":
		mutation = &pb.RequestAutomationValidationRequest{}
	case "cancel-validation":
		mutation = &pb.CancelAutomationValidationRequest{}

	case "save-destination":
		mutation = &pb.SaveNotificationDestinationRequest{}
	case "set-organization-smtp":
		mutation = &pb.SetOrganizationSmtpRequest{}
	case "send-notification-test":
		mutation = &pb.SendNotificationTestRequest{}
	case "verify-destination":
		mutation = &pb.VerifyNotificationDestinationRequest{}
	case "redeliver-notification":
		mutation = &pb.RedeliverNotificationRequest{}

	case "set-destination-enabled":
		mutation = &pb.SetNotificationDestinationEnabledRequest{}
	case "set-notification-grouping":
		mutation = &pb.SetNotificationGroupingRequest{}
	case "set-notification-subscriptions":
		mutation = &pb.SetNotificationSubscriptionsRequest{}
	case "delete-destination":
		mutation = &pb.DeleteNotificationDestinationRequest{}

	case "request-operation":
		mutation = &pb.RequestOperationRequest{}
	case "request-server":
		mutation = &pb.RequestServerCreationRequest{}
	case "request-ssh-key":
		mutation = &pb.RequestSSHKeyCreationRequest{}
	case "review-operation":
		mutation = &pb.ReviewOperationRequest{}
	case "resolve-operation":
		mutation = &pb.ResolveOperationRequest{}
	case "save-role":
		mutation = &pb.SaveRoleRequest{}
	case "update-member":
		mutation = &pb.UpdateMemberRequest{}
	case "save-team":
		mutation = &pb.SaveTeamRequest{}
	case "delete-team":
		mutation = &pb.DeleteTeamRequest{}
	case "save-view":
		mutation = &pb.SaveInventoryViewRequest{}
	case "delete-view":
		mutation = &pb.DeleteInventoryViewRequest{}
	case "save-dashboard":
		mutation = &pb.SaveDashboardRequest{}
	case "delete-dashboard":
		mutation = &pb.DeleteDashboardRequest{}
	case "preview-schedule":
		mutation = &pb.PreviewScheduleRequest{}
	case "save-schedule":
		mutation = &pb.SaveScheduleRequest{}
	case "approve-schedule":
		mutation = &pb.ApproveScheduleRequest{}
	case "set-schedule-enabled":
		mutation = &pb.SetScheduleEnabledRequest{}
	case "delete-schedule":
		mutation = &pb.DeleteScheduleRequest{}
	}
	if mutation != nil {
		if e := commandInput(*values["input"], *org, mutation); e != nil {
			_, _ = fmt.Fprintln(errOut, "Invalid command JSON, missing required fields or organization mismatch. No request sent.")
			return 2
		}
	}
	if command == "import-automation-source" {
		archive, err := boundedInputFile(*values["archive"], 4<<20, true)
		if err != nil || len(archive) == 0 {
			_, _ = fmt.Fprintln(errOut, "A private regular archive file of 1 byte to 4 MiB is required. No request sent.")
			return 2
		}
		mutation.(*pb.ImportAutomationSourceRequest).Archive = archive
	}
	credentials := struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}{Email: *email}
	if *stdinAuth {
		data, e := io.ReadAll(io.LimitReader(in, 4097))
		if e != nil || len(data) > 4096 {
			_, _ = fmt.Fprintln(errOut, "Authentication input must be a JSON object of at most 4096 bytes.")
			return 2
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		e = decoder.Decode(&credentials)
		var trailing any
		if e != nil || decoder.Decode(&trailing) != io.EOF {
			clear(data)
			_, _ = fmt.Fprintln(errOut, "Invalid authentication JSON.")
			return 2
		}
		clear(data)
	} else {
		file, ok := in.(*os.File)
		if !ok || !term.IsTerminal(int(file.Fd())) || credentials.Email == "" {
			_, _ = fmt.Fprintln(errOut, "Use --email with a terminal, or --auth-stdin for noninteractive login.")
			return 2
		}
		read := func(prompt string) (string, error) {
			_, _ = fmt.Fprint(errOut, prompt)
			b, e := term.ReadPassword(int(file.Fd()))
			_, _ = fmt.Fprintln(errOut)
			s := string(b)
			clear(b)
			return s, e
		}
		credentials.Password, e = read("Password: ")
		readCode = func() (string, error) { return read("Authenticator code: ") }
		if e != nil {
			_, _ = fmt.Fprintln(errOut, "Unable to read authentication input.")
			return 2
		}
	}
	if len(credentials.Email) == 0 || len(credentials.Email) > 320 || len(credentials.Password) == 0 || len(credentials.Password) > 72 || (credentials.Code != "" && !regexp.MustCompile(`^[0-9]{6}$`).MatchString(credentials.Code)) {
		_, _ = fmt.Fprintln(errOut, "Email and password are required; an optional authenticator code must contain six digits.")
		return 2
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	client := providahv1connect.NewConsoleServiceClient(httpClient, base.String()+"/api", connect.WithReadMaxBytes(8<<20), connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			r.Header().Set("Origin", base.String())
			return next(ctx, r)
		}
	})))
	login := &pb.LoginRequest{Email: credentials.Email, Password: credentials.Password, Code: credentials.Code}
	_, e = authenticate(ctx, client, login, readCode)
	login.Password = ""
	login.Code = ""
	credentials.Password = ""
	credentials.Code = ""
	if e != nil {
		return report(errOut, e)
	}
	// Revoke the invocation's server session even when the read fails or is canceled.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := client.Logout(cleanup, connect.NewRequest(&pb.LogoutRequest{})); e != nil {
			_, _ = fmt.Fprintln(errOut, "Session cleanup was not confirmed; the server session will expire normally.")
		}
	}()
	ctx, cancelRead := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelRead()
	var message, combined proto.Message
	seen := map[string]bool{*values["page"]: true}
	totalBytes, totalRows := 0, 0
	for pageNumber := 1; ; pageNumber++ {
		switch command {
		case "teams":
			r, e := client.ListTeams(ctx, connect.NewRequest(&pb.ListTeamsRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "access":
			r, e := client.ListAccess(ctx, connect.NewRequest(&pb.ListAccessRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "save-role":
			r, e := client.SaveRole(ctx, connect.NewRequest(mutation.(*pb.SaveRoleRequest)))
			if e != nil {
				return reportAccessMutation(errOut, e)
			}
			message = r.Msg
		case "update-member":
			r, e := client.UpdateMember(ctx, connect.NewRequest(mutation.(*pb.UpdateMemberRequest)))
			if e != nil {
				return reportAccessMutation(errOut, e)
			}
			message = r.Msg

		case "save-team":
			r, e := client.SaveTeam(ctx, connect.NewRequest(mutation.(*pb.SaveTeamRequest)))
			if e != nil {
				return reportTeamMutation(errOut, e)
			}
			message = r.Msg
		case "delete-team":
			r, e := client.DeleteTeam(ctx, connect.NewRequest(mutation.(*pb.DeleteTeamRequest)))
			if e != nil {
				return reportTeamMutation(errOut, e)
			}
			message = r.Msg

		case "save-view":
			r, e := client.SaveInventoryView(ctx, connect.NewRequest(mutation.(*pb.SaveInventoryViewRequest)))
			if e != nil {
				return reportScopeMutation(errOut, e)
			}
			message = r.Msg
		case "delete-view":
			r, e := client.DeleteInventoryView(ctx, connect.NewRequest(mutation.(*pb.DeleteInventoryViewRequest)))
			if e != nil {
				return reportScopeMutation(errOut, e)
			}
			message = r.Msg
		case "save-dashboard":
			r, e := client.SaveDashboard(ctx, connect.NewRequest(mutation.(*pb.SaveDashboardRequest)))
			if e != nil {
				return reportScopeMutation(errOut, e)
			}
			message = r.Msg
		case "delete-dashboard":
			r, e := client.DeleteDashboard(ctx, connect.NewRequest(mutation.(*pb.DeleteDashboardRequest)))
			if e != nil {
				return reportScopeMutation(errOut, e)
			}
			message = r.Msg
		case "preview-schedule":
			r, e := client.PreviewSchedule(ctx, connect.NewRequest(mutation.(*pb.PreviewScheduleRequest)))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "save-schedule":
			r, e := client.SaveSchedule(ctx, connect.NewRequest(mutation.(*pb.SaveScheduleRequest)))
			if e != nil {
				return reportScheduleMutation(errOut, e)
			}
			message = r.Msg
		case "approve-schedule":
			r, e := client.ApproveSchedule(ctx, connect.NewRequest(mutation.(*pb.ApproveScheduleRequest)))
			if e != nil {
				return reportScheduleMutation(errOut, e)
			}
			message = r.Msg
		case "set-schedule-enabled":
			r, e := client.SetScheduleEnabled(ctx, connect.NewRequest(mutation.(*pb.SetScheduleEnabledRequest)))
			if e != nil {
				return reportScheduleMutation(errOut, e)
			}
			message = r.Msg
		case "delete-schedule":
			r, e := client.DeleteSchedule(ctx, connect.NewRequest(mutation.(*pb.DeleteScheduleRequest)))
			if e != nil {
				return reportScheduleMutation(errOut, e)
			}
			message = r.Msg
		case "preview-deletion":
			r, e := client.PreviewDeletion(ctx, connect.NewRequest(&pb.PreviewDeletionRequest{OrganizationId: *org, ResourceId: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "request-operation":
			r, e := client.RequestOperation(ctx, connect.NewRequest(mutation.(*pb.RequestOperationRequest)))
			if e != nil {
				return reportMutation(errOut, e)
			}
			message = r.Msg
		case "request-server":
			r, e := client.RequestServerCreation(ctx, connect.NewRequest(mutation.(*pb.RequestServerCreationRequest)))
			if e != nil {
				return reportMutation(errOut, e)
			}
			message = r.Msg
		case "request-ssh-key":
			r, e := client.RequestSSHKeyCreation(ctx, connect.NewRequest(mutation.(*pb.RequestSSHKeyCreationRequest)))
			if e != nil {
				return reportMutation(errOut, e)
			}
			message = r.Msg
		case "review-operation":
			r, e := client.ReviewOperation(ctx, connect.NewRequest(mutation.(*pb.ReviewOperationRequest)))
			if e != nil {
				return reportMutation(errOut, e)
			}
			message = r.Msg
		case "resolve-operation":
			r, e := client.ResolveOperation(ctx, connect.NewRequest(mutation.(*pb.ResolveOperationRequest)))
			if e != nil {
				return reportMutation(errOut, e)
			}
			message = r.Msg
		case "cancel-operation":
			r, e := client.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return reportMutation(errOut, e)
			}
			message = r.Msg
		case "reconcile-operation":
			r, e := client.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return reportMutation(errOut, e)
			}
			message = r.Msg
		case "session":
			r, e := client.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "connections":
			r, e := client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "resources":
			f := filters
			if f == nil {
				f = &pb.InventoryViewSpec{ConnectionId: *values["connection"], Kind: *values["kind"], Provider: *values["provider"], Search: *values["search"], SortBy: *values["sort"], Descending: *descending}
			}
			r, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: *org, ConnectionId: f.ConnectionId, Kind: f.Kind, Provider: f.Provider, Search: f.Search, PageToken: *values["page"], SortBy: f.SortBy, Descending: f.Descending, PageSize: 200, TagKey: f.TagKey, TagValue: f.TagValue, TagExists: f.TagExists, TagName: f.TagName, TagConditions: f.TagConditions, TagMatchAny: f.TagMatchAny, Region: f.Region, Status: f.Status}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "resource":
			r, e := client.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "operation":
			r, e := inspectOperation(ctx, client, *org, *values["id"], *wait)
			if e != nil {
				return report(errOut, e)
			}
			message = r
		case "operations":
			r, e := client.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{OrganizationId: *org, PageToken: *values["page"], ResourceFilters: filters}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "audit":
			r, e := client.ListAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{OrganizationId: *org, PageToken: *values["page"], Actor: *values["actor"], Action: *values["action"], Target: *values["target"], OccurredFrom: *values["from"], OccurredBefore: *values["before"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "schedule":
			r, e := client.GetSchedule(ctx, connect.NewRequest(&pb.GetScheduleRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "schedule-history":
			r, e := client.ListScheduleOccurrences(ctx, connect.NewRequest(&pb.ListScheduleOccurrencesRequest{OrganizationId: *org, ScheduleId: *values["schedule"], Outcome: *values["outcome"], PageToken: *values["page"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "schedules":
			r, e := client.ListSchedules(ctx, connect.NewRequest(&pb.ListSchedulesRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "resource-summary":
			r, e := client.GetResourceSummary(ctx, connect.NewRequest(&pb.GetResourceSummaryRequest{OrganizationId: *org, Filters: filters, GroupBy: *values["group-by"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "views":
			r, e := client.ListInventoryViews(ctx, connect.NewRequest(&pb.ListInventoryViewsRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "dashboards":
			r, e := client.ListDashboards(ctx, connect.NewRequest(&pb.ListDashboardsRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "notification-grouping":
			r, e := client.GetNotificationGrouping(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "set-notification-grouping":
			r, e := client.SetNotificationGrouping(ctx, connect.NewRequest(mutation.(*pb.SetNotificationGroupingRequest)))
			if e != nil {
				return reportNotificationMutation(errOut, e)
			}
			message = r.Msg
		case "set-notification-subscriptions":
			r, e := client.SetNotificationSubscriptions(ctx, connect.NewRequest(mutation.(*pb.SetNotificationSubscriptionsRequest)))
			if e != nil {
				return reportNotificationMutation(errOut, e)
			}
			message = r.Msg
		case "delete-destination":
			r, e := client.DeleteNotificationDestination(ctx, connect.NewRequest(mutation.(*pb.DeleteNotificationDestinationRequest)))
			if e != nil {
				return reportNotificationMutation(errOut, e)
			}
			message = r.Msg
		case "set-destination-enabled":
			r, e := client.SetNotificationDestinationEnabled(ctx, connect.NewRequest(mutation.(*pb.SetNotificationDestinationEnabledRequest)))
			if e != nil {
				return reportNotificationMutation(errOut, e)
			}
			message = r.Msg
		case "organization-smtp":
			r, e := client.GetOrganizationSmtp(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "save-destination":
			r, e := client.SaveNotificationDestination(ctx, connect.NewRequest(mutation.(*pb.SaveNotificationDestinationRequest)))
			if e != nil {
				return reportNotificationMutation(errOut, e)
			}
			message = r.Msg
		case "set-organization-smtp":
			r, e := client.SetOrganizationSmtp(ctx, connect.NewRequest(mutation.(*pb.SetOrganizationSmtpRequest)))
			if e != nil {
				return reportNotificationMutation(errOut, e)
			}
			message = r.Msg
		case "send-notification-test":
			r, e := client.SendNotificationTest(ctx, connect.NewRequest(mutation.(*pb.SendNotificationTestRequest)))
			if e != nil {
				return reportNotificationMutation(errOut, e)
			}
			message = r.Msg
		case "verify-destination":
			r, e := client.VerifyNotificationDestination(ctx, connect.NewRequest(mutation.(*pb.VerifyNotificationDestinationRequest)))
			if e != nil {
				return reportNotificationMutation(errOut, e)
			}
			message = r.Msg
		case "redeliver-notification":
			r, e := client.RedeliverNotification(ctx, connect.NewRequest(mutation.(*pb.RedeliverNotificationRequest)))
			if e != nil {
				return reportNotificationMutation(errOut, e)
			}
			message = r.Msg
		case "automation-projects":
			r, e := client.ListAutomationProjects(ctx, connect.NewRequest(&pb.ListAutomationProjectsRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "automation-project":
			r, e := client.GetAutomationProject(ctx, connect.NewRequest(&pb.GetAutomationProjectRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "project-ownership":
			r, e := client.ListProjectOwnership(ctx, connect.NewRequest(&pb.ListProjectOwnershipRequest{OrganizationId: *org, ProjectId: *values["id"], PageToken: *values["page"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "project-states":
			r, e := client.ListAutomationStates(ctx, connect.NewRequest(&pb.ListAutomationStatesRequest{OrganizationId: *org, ProjectId: *values["id"], BeforeSerial: *values["page"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "validations":
			r, e := client.ListAutomationValidations(ctx, connect.NewRequest(&pb.ListAutomationValidationsRequest{OrganizationId: *org, PageToken: *values["page"], Status: *values["status"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "validation":
			r, e := client.GetAutomationValidation(ctx, connect.NewRequest(&pb.GetAutomationValidationRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "create-automation-project":
			r, e := client.CreateAutomationProject(ctx, connect.NewRequest(mutation.(*pb.CreateAutomationProjectRequest)))
			if e != nil {
				return reportAutomationMutation(errOut, e)
			}
			message = r.Msg
		case "request-validation":
			r, e := client.RequestAutomationValidation(ctx, connect.NewRequest(mutation.(*pb.RequestAutomationValidationRequest)))
			if e != nil {
				return reportAutomationMutation(errOut, e)
			}
			message = r.Msg
		case "cancel-validation":
			r, e := client.CancelAutomationValidation(ctx, connect.NewRequest(mutation.(*pb.CancelAutomationValidationRequest)))
			if e != nil {
				return reportAutomationMutation(errOut, e)
			}
			message = r.Msg
		case "automation-versions":
			r, e := client.ListAutomationVersions(ctx, connect.NewRequest(&pb.ListAutomationVersionsRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "automation-version":
			r, e := client.GetAutomationVersion(ctx, connect.NewRequest(&pb.GetAutomationVersionRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "import-automation-source":
			r, e := client.ImportAutomationSource(ctx, connect.NewRequest(mutation.(*pb.ImportAutomationSourceRequest)))
			if e != nil {
				return reportAutomationMutation(errOut, e)
			}
			message = r.Msg
		case "publish-automation-version":
			r, e := client.PublishAutomationVersion(ctx, connect.NewRequest(mutation.(*pb.PublishAutomationVersionRequest)))
			if e != nil {
				return reportAutomationMutation(errOut, e)
			}
			message = r.Msg
		case "set-automation-version-status":
			r, e := client.SetAutomationVersionStatus(ctx, connect.NewRequest(mutation.(*pb.SetAutomationVersionStatusRequest)))
			if e != nil {
				return reportAutomationMutation(errOut, e)
			}
			message = r.Msg
		case "automation-sources":
			r, e := client.ListAutomationSources(ctx, connect.NewRequest(&pb.ListAutomationSourcesRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "automation-source":
			r, e := client.GetAutomationSource(ctx, connect.NewRequest(&pb.GetAutomationSourceRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "server-templates":
			r, e := client.ListServerTemplates(ctx, connect.NewRequest(&pb.ListServerTemplatesRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "server-template":
			r, e := client.GetServerTemplate(ctx, connect.NewRequest(&pb.GetServerTemplateRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "publish-server-template":
			r, e := client.PublishServerTemplate(ctx, connect.NewRequest(mutation.(*pb.PublishServerTemplateRequest)))
			if e != nil {
				return reportAutomationMutation(errOut, e)
			}
			message = r.Msg
		case "set-server-template-status":
			r, e := client.SetServerTemplateStatus(ctx, connect.NewRequest(mutation.(*pb.SetServerTemplateStatusRequest)))
			if e != nil {
				return reportAutomationMutation(errOut, e)
			}
			message = r.Msg
		case "destinations":
			r, e := client.ListNotificationDestinations(ctx, connect.NewRequest(&pb.ListNotificationDestinationsRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "destination":
			r, e := client.GetNotificationDestination(ctx, connect.NewRequest(&pb.GetNotificationRecordRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "deliveries":
			r, e := client.ListNotificationDeliveries(ctx, connect.NewRequest(&pb.ListNotificationDeliveriesRequest{OrganizationId: *org, PageToken: *values["page"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "delivery":
			r, e := client.GetNotificationDelivery(ctx, connect.NewRequest(&pb.GetNotificationRecordRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "delivery-attempts":
			r, e := client.ListNotificationAttempts(ctx, connect.NewRequest(&pb.ListNotificationAttemptsRequest{OrganizationId: *org, Id: *values["id"]}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		case "modules":
			r, e := client.ListProviderModules(ctx, connect.NewRequest(&pb.ListProviderModulesRequest{OrganizationId: *org}))
			if e != nil {
				return report(errOut, e)
			}
			message = r.Msg
		}

		if !*all {
			break
		}
		totalBytes += proto.Size(message)
		reflected := message.ProtoReflect()
		fields := reflected.Descriptor().Fields()
		for i := 0; i < fields.Len(); i++ {
			f := fields.Get(i)
			if f.IsList() {
				totalRows += reflected.Get(f).List().Len()
			}
		}
		if totalBytes > 64<<20 || totalRows > 100000 {
			_, _ = fmt.Fprintln(errOut, "Result limit exceeded; use narrower filters. No partial result written.")
			return 6
		}
		tokenField := fields.ByName("next_page_token")
		if tokenField == nil {
			tokenField = fields.ByName("next_before_serial")
		}
		next := reflected.Get(tokenField).String()
		if combined == nil {
			combined = message
		} else {
			proto.Merge(combined, message)
		}
		if next == "" {
			combined.ProtoReflect().Clear(tokenField)
			message = combined
			break
		}
		if seen[next] || pageNumber >= 1000 {
			_, _ = fmt.Fprintln(errOut, "Pagination repeated a cursor or exceeded 1000 pages. No partial result written.")
			return 6
		}
		seen[next] = true
		*values["page"] = next
	}
	if *jsonOutput {
		data, e := protojson.MarshalOptions{Indent: "  "}.Marshal(message)
		if e == nil {
			_, e = fmt.Fprintln(out, string(data))
		}
		if e != nil {
			_, _ = fmt.Fprintln(errOut, "Output could not be written.")
			return 1
		}
		return 0
	}
	if e := table(out, message); e != nil {
		_, _ = fmt.Fprintln(errOut, "Output could not be written.")
		return 1
	}
	return 0
}

func report(out io.Writer, e error) int {
	code, description := 1, "Request failed; check connectivity and server availability."
	switch connect.CodeOf(e) {
	case connect.CodeUnauthenticated:
		code, description = 3, "Authentication failed or session expired."
	case connect.CodePermissionDenied:
		code, description = 4, "Access denied by the server."
	case connect.CodeInvalidArgument:
		code, description = 2, "Invalid request filters or context."
	case connect.CodeFailedPrecondition, connect.CodeAborted:
		code, description = 5, "Server policy or current state prevents this request."
	case connect.CodeResourceExhausted:
		code, description = 6, "Server rate or capacity limit reached."
	}
	_, _ = fmt.Fprintln(out, description)
	return code
}

// Quote untrusted names/values so terminal controls and embedded newlines cannot execute.
func table(out io.Writer, message proto.Message) error {
	switch message.(type) {
	case *pb.AutomationSource, *pb.AutomationProject, *pb.ServerTemplate, *pb.OperationResponse:
		return detailTable(out, message)
	}

	if access, ok := message.(*pb.ListAccessResponse); ok {
		root := access.ProtoReflect()
		fields := root.Descriptor().Fields()
		for i := 0; i < fields.Len(); i++ {
			f := fields.Get(i)
			if _, e := fmt.Fprintf(out, "%s:\n", f.Name()); e != nil {
				return e
			}
			list := root.Get(f).List()
			for j := 0; j < list.Len(); j++ {
				if f.Kind() == protoreflect.MessageKind {
					if e := table(out, list.Get(j).Message().Interface()); e != nil {
						return e
					}
				} else if _, e := fmt.Fprintln(out, strconv.QuoteToASCII(list.Get(j).String())); e != nil {
					return e
				}
			}
		}
		return nil
	}

	root := message.ProtoReflect()
	fields := root.Descriptor().Fields()
	var rows []protoreflect.Message
	var record protoreflect.MessageDescriptor
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.Kind() == protoreflect.MessageKind {
			record = f.Message()
			if f.IsList() {
				list := root.Get(f).List()
				for n := 0; n < list.Len(); n++ {
					rows = append(rows, list.Get(n).Message())
				}
			} else if root.Has(f) {
				rows = append(rows, root.Get(f).Message())
			}
			break
		}
	}
	if record == nil {
		for i := 0; i < fields.Len(); i++ {
			f := fields.Get(i)
			if _, e := fmt.Fprintf(out, "%s: %s\n", f.Name(), strconv.QuoteToASCII(fmt.Sprint(root.Get(f).Interface()))); e != nil {
				return e
			}
		}
		return nil
	}
	columns := []protoreflect.FieldDescriptor{}
	for _, name := range []protoreflect.Name{"id", "name", "resource_name", "provider", "kind", "region", "status", "action", "actor", "target", "occurred_at", "size", "expected_size", "target_size", "enabled", "approved", "next_local", "last_detail", "schedule_id", "scheduled_for", "local_time", "outcome", "operation_id", "operation_status", "detail", "sso_required", "label", "total", "oldest_observation", "due", "local", "skipped_reason", "maintenance_restrictions", "identity_enabled", "shared", "owned", "revision", "role_id", "active", "user_ids", "endpoint", "verified", "event_types", "event_id", "destination_name", "event_type", "attempts", "updated_at", "number", "response_code", "created_at", "native_id", "resource_id", "first_state_id", "conflicting", "version_id", "project_id", "serial", "lineage", "sha256", "state_bytes", "locked", "runtime", "runtime_image", "runtime_available", "version", "connection_id", "connection_revision"} {
		if f := record.Fields().ByName(name); f != nil {
			columns = append(columns, f)
		}
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, f := range columns {
		_, _ = fmt.Fprintf(writer, "%s\t", f.Name())
	}
	_, _ = fmt.Fprintln(writer)
	for _, row := range rows {
		for _, f := range columns {
			value := fmt.Sprint(row.Get(f).Interface())
			if f.Kind() == protoreflect.EnumKind {
				if v := f.Enum().Values().ByNumber(row.Get(f).Enum()); v != nil {
					value = string(v.Name())
				}
			}
			_, _ = fmt.Fprintf(writer, "%s\t", strconv.QuoteToASCII(value))
		}
		_, _ = fmt.Fprintln(writer)
	}
	if e := writer.Flush(); e != nil {
		return e
	}
	f := fields.ByName("next_page_token")
	if f == nil {
		f = fields.ByName("next_before_serial")
	}
	if f != nil && root.Get(f).String() != "" {
		_, e := fmt.Fprintf(out, "Next page: --page %s\n", strconv.QuoteToASCII(root.Get(f).String()))
		return e
	}
	return nil
}

// These detail records have nested configuration that must not hide their identity.
func detailTable(out io.Writer, message proto.Message) error {
	m := message.ProtoReflect()
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.Kind() != protoreflect.MessageKind || f.IsMap() {
			if _, err := fmt.Fprintf(out, "%s: %s\n", f.Name(), strconv.QuoteToASCII(fmt.Sprint(m.Get(f).Interface()))); err != nil {
				return err
			}
			continue
		}
		if f.IsList() {
			list := m.Get(f).List()
			for j := 0; j < list.Len(); j++ {
				if _, err := fmt.Fprintf(out, "%s:\n", f.Name()); err != nil {
					return err
				}
				if err := detailTable(out, list.Get(j).Message().Interface()); err != nil {
					return err
				}
			}
		} else if m.Has(f) {
			if _, err := fmt.Fprintf(out, "%s:\n", f.Name()); err != nil {
				return err
			}
			if err := detailTable(out, m.Get(f).Message().Interface()); err != nil {
				return err
			}
		}
	}
	return nil
}
