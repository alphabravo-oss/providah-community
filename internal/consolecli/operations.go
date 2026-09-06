package consolecli

import (
	"encoding/json"
	"errors"
	"fmt"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"io"
	"os"
	"strings"
)

// Only typed command inputs are accepted; unknown fields (including credentials) fail.
func commandInput(path, org string, message proto.Message) error {
	private := false
	switch message.(type) {
	case *pb.CreateAutomationProjectRequest, *pb.SaveNotificationDestinationRequest, *pb.SetOrganizationSmtpRequest, *pb.VerifyNotificationDestinationRequest:
		private = true
	}
	data, e := inputFile(path, private)
	if e != nil {
		return e
	}
	if e = protojson.Unmarshal(data, message); e != nil {
		return e
	}
	m := message.ProtoReflect()
	field := m.Descriptor().Fields().ByName("organization_id")
	if supplied := m.Get(field).String(); supplied != "" && supplied != org {
		return errors.New("organization mismatch")
	}
	m.Set(field, protoreflect.ValueOfString(org))
	required := []protoreflect.Name{"reason"}
	switch r := message.(type) {
	case *pb.PublishServerTemplateRequest:
		required = []protoreflect.Name{"name", "connection_id", "region"}
		if r.Creation == nil {
			return errors.New("explicit creation configuration required")
		}
	case *pb.SetServerTemplateStatusRequest:
		required = []protoreflect.Name{"id", "status"}
		if r.Status != "retired" && r.Status != "revoked" {
			return errors.New("choose retired or revoked")
		}
	case *pb.ImportAutomationSourceRequest:
		required = []protoreflect.Name{"name", "runtime", "entrypoint"}
		if len(r.Archive) != 0 {
			return errors.New("supply archive with --archive, not inline JSON")
		}
		if r.Runtime != "terraform" && r.Runtime != "opentofu" && r.Runtime != "ansible" {
			return errors.New("unsupported runtime")
		}
	case *pb.PublishAutomationVersionRequest:
		required = []protoreflect.Name{"name", "source_id", "runtime_image", "connection_id", "region", "review_note"}
		if r.ExpectedVersion < 0 {
			return errors.New("invalid expected version")
		}
	case *pb.SetAutomationVersionStatusRequest:
		required = []protoreflect.Name{"id", "status"}
		if r.Status != "retired" && r.Status != "revoked" {
			return errors.New("choose retired or revoked")
		}
	case *pb.CreateAutomationProjectRequest:
		required = []protoreflect.Name{"name", "version_id", "inputs_json"}
		var values map[string]json.RawMessage
		if json.Unmarshal([]byte(r.InputsJson), &values) != nil || values == nil {
			return errors.New("explicit project input object required")
		}
	case *pb.RequestAutomationValidationRequest:
		required = []protoreflect.Name{"version_id"}
	case *pb.CancelAutomationValidationRequest:
		required = []protoreflect.Name{"id"}
	case *pb.SaveNotificationDestinationRequest:
		required = []protoreflect.Name{"name", "kind", "endpoint"}
		if (r.Id == "" && r.ExpectedRevision != 0) || (r.Id != "" && r.ExpectedRevision < 1) {
			return errors.New("reviewed revision required for replacement")
		}
		switch r.Kind {
		case "webhook":
			if r.SigningSecret == "" || r.Smtp != nil || r.UsePlatformSmtp || r.UseOrganizationSmtp {
				return errors.New("webhook signing secret required")
			}
		case "email":
			sources := 0
			if r.Smtp != nil {
				sources++
			}
			if r.UsePlatformSmtp {
				sources++
			}
			if r.UseOrganizationSmtp {
				sources++
			}
			if sources != 1 || r.SigningSecret != "" || (r.UseOrganizationSmtp && r.OrganizationSmtpRevision < 1) {
				return errors.New("select one reviewed SMTP source")
			}
		default:
			return errors.New("unsupported destination kind")
		}
	case *pb.SetOrganizationSmtpRequest:
		required = nil
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if r.ExpectedRevision < 1 || (string(fields["remove"]) != "true" && string(fields["remove"]) != "false") || r.Remove == (r.Smtp != nil) {
			return errors.New("explicit remove boolean, reviewed revision and replacement SMTP settings required")
		}
	case *pb.SendNotificationTestRequest:
		required = []protoreflect.Name{"id"}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if string(fields["verification"]) != "true" && string(fields["verification"]) != "false" {
			return errors.New("explicit verification boolean required")
		}
	case *pb.VerifyNotificationDestinationRequest:
		required = []protoreflect.Name{"id", "code"}
	case *pb.RedeliverNotificationRequest:
		required = []protoreflect.Name{"id"}
	case *pb.SetNotificationDestinationEnabledRequest:
		required = []protoreflect.Name{"id"}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if r.ExpectedRevision < 1 || (string(fields["enabled"]) != "true" && string(fields["enabled"]) != "false") {
			return errors.New("explicit enabled boolean and reviewed revision required")
		}
	case *pb.SetNotificationSubscriptionsRequest:
		required = []protoreflect.Name{"id"}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		events := fields["eventTypes"]
		if events == nil {
			events = fields["event_types"]
		}
		if r.ExpectedRevision < 1 || len(events) == 0 || events[0] != '[' {
			return errors.New("explicit eventTypes array and reviewed revision required")
		}
	case *pb.SetNotificationGroupingRequest:
		required = nil
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if r.ExpectedRevision < 1 || len(fields["seconds"]) == 0 || string(fields["seconds"]) == "null" {
			return errors.New("explicit seconds and reviewed revision required")
		}
		if r.Seconds != 0 && r.Seconds != 60 && r.Seconds != 300 && r.Seconds != 900 && r.Seconds != 3600 {
			return errors.New("grouping seconds must be 0, 60, 300, 900 or 3600")
		}
	case *pb.DeleteNotificationDestinationRequest:
		required = []protoreflect.Name{"id", "confirmation"}
		if r.ExpectedRevision < 1 {
			return errors.New("reviewed destination revision required")
		}
	case *pb.RequestOperationRequest:
		required = append(required, "resource_id", "action", "expected_status", "idempotency_key")
		if r.Action == "tags" {
			if r.ExpectedTags == nil || r.TargetTags == nil {
				return errors.New("complete expectedTags and targetTags required; use an explicit empty object to clear tags")
			}
		} else if r.ExpectedTags != nil || r.TargetTags != nil {
			return errors.New("tag inputs require the tags action")
		}
	case *pb.RequestSSHKeyCreationRequest:
		required = append(required, "connection_id", "region", "idempotency_key")
		if r.Creation == nil {
			return errors.New("missing public key configuration")
		}
		if err := (provider.SSHKeyCreate{Name: r.Creation.Name, PublicKey: r.Creation.PublicKey}).Validate(); err != nil {
			return err
		}
	case *pb.RequestServerCreationRequest:
		required = append(required, "connection_id", "region", "idempotency_key")
		if r.Creation == nil {
			return errors.New("missing configuration")
		}
	case *pb.ReviewOperationRequest:
		required = append(required, "id")
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || (string(fields["approve"]) != "true" && string(fields["approve"]) != "false") {
			return errors.New("explicit approve boolean required")
		}
	case *pb.ResolveOperationRequest:
		required = append(required, "id")
	case *pb.SaveRoleRequest:
		required = []protoreflect.Name{"name"}
		if len(r.Permissions) == 0 || (r.Id == "" && r.ExpectedRevision != 0) || (r.Id != "" && r.ExpectedRevision < 1) {
			return errors.New("explicit permissions and reviewed revision required")
		}
	case *pb.UpdateMemberRequest:
		if r.ExpectedRevision < 1 {
			return errors.New("reviewed membership revision required")
		}
		required = []protoreflect.Name{"user_id", "role_id"}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if string(fields["active"]) != "true" && string(fields["active"]) != "false" {
			return errors.New("explicit active boolean required")
		}

	case *pb.SaveTeamRequest:
		required = []protoreflect.Name{"name", "role_id"}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		users := fields["userIds"]
		if users == nil {
			users = fields["user_ids"]
		}
		if (r.Id == "" && r.ExpectedRevision != 0) || (r.Id != "" && r.ExpectedRevision < 1) || (string(fields["active"]) != "true" && string(fields["active"]) != "false") || len(users) == 0 || users[0] != '[' {
			return errors.New("explicit active boolean, userIds array and reviewed revision required")
		}
	case *pb.DeleteTeamRequest:
		required = []protoreflect.Name{"id"}
		if r.ExpectedRevision < 1 {
			return errors.New("reviewed revision required")
		}

	case *pb.SaveInventoryViewRequest, *pb.SaveDashboardRequest:
		required = []protoreflect.Name{"name"}
		id := m.Get(m.Descriptor().Fields().ByName("id")).String()
		revision := m.Get(m.Descriptor().Fields().ByName("expected_revision")).Int()
		if !m.Has(m.Descriptor().Fields().ByName("spec")) || (id == "" && revision != 0) || (id != "" && revision < 1) {
			return errors.New("specification and reviewed revision required")
		}
		if _, dashboard := message.(*pb.SaveDashboardRequest); dashboard {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				return err
			}
			teams := fields["teamIds"]
			if teams == nil {
				teams = fields["team_ids"]
			}
			if (string(fields["shared"]) != "true" && string(fields["shared"]) != "false") || len(teams) == 0 || teams[0] != '[' {
				return errors.New("explicit shared boolean and teamIds array required")
			}
		}
	case *pb.DeleteInventoryViewRequest, *pb.DeleteDashboardRequest:
		required = []protoreflect.Name{"id"}
		if m.Get(m.Descriptor().Fields().ByName("expected_revision")).Int() < 1 {
			return errors.New("reviewed revision required")
		}

	case *pb.PreviewScheduleRequest:
		required = nil
		if r.Spec == nil {
			return errors.New("missing schedule specification")
		}
	case *pb.SaveScheduleRequest:
		required = []protoreflect.Name{"name"}
		if r.Spec == nil || len(r.ResourceIds) == 0 || (r.Id == "" && r.ExpectedRevision != 0) || (r.Id != "" && r.ExpectedRevision < 1) {
			return errors.New("missing targets, specification or reviewed revision")
		}
	case *pb.ApproveScheduleRequest:
		required = []protoreflect.Name{"id"}
		if r.ExpectedRevision < 1 {
			return errors.New("reviewed revision required")
		}
	case *pb.SetScheduleEnabledRequest:
		required = []protoreflect.Name{"id"}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		identity := fields["identityEnabled"]
		if identity == nil {
			identity = fields["identity_enabled"]
		}
		if r.ExpectedRevision < 1 || (string(fields["enabled"]) != "true" && string(fields["enabled"]) != "false") || (string(identity) != "true" && string(identity) != "false") {
			return errors.New("explicit enabled and identityEnabled booleans and reviewed revision required")
		}
	case *pb.DeleteScheduleRequest:
		required = []protoreflect.Name{"id", "confirmation"}
		if r.ExpectedRevision < 1 {
			return errors.New("reviewed revision required")
		}
	default:
		return errors.New("unsupported command input")
	}
	for _, name := range required {
		if strings.TrimSpace(m.Get(m.Descriptor().Fields().ByName(name)).String()) == "" {
			return errors.New("missing required input")
		}
	}
	return nil
}
func reportMutation(out io.Writer, e error) int {
	code := report(out, e)
	if code == 1 {
		_, _ = fmt.Fprintln(out, "The request may have been recorded. Inspect operations before retrying; retain the original request key and exact input.")
	}
	return code
}

func inputFile(path string, private ...bool) ([]byte, error) {
	return boundedInputFile(path, 64<<10, len(private) > 0 && private[0])
}
func boundedInputFile(path string, limit int64, private bool) ([]byte, error) {
	before, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("input must be a regular file")
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = file.Close() }()
	info, e := file.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() > limit || (private && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("input must be a bounded regular file")
	}
	data, e := io.ReadAll(io.LimitReader(file, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(data)) > limit {
		return nil, errors.New("input too large")
	}
	return data, nil
}

func reportScheduleMutation(out io.Writer, e error) int {
	code := report(out, e)
	if code == 1 {
		_, _ = fmt.Fprintln(out, "The schedule change may have been recorded. Inspect schedules and reload the current revision before deciding whether to retry; creation retries may create duplicates.")
	}
	return code
}

func reportScopeMutation(out io.Writer, e error) int {
	code := report(out, e)
	if code == 1 {
		_, _ = fmt.Fprintln(out, "The change may have been recorded. Inspect views or dashboards and reload the current revision before retrying; creation retries may create duplicates.")
	}
	return code
}

func reportTeamMutation(out io.Writer, e error) int {
	code := report(out, e)
	if code == 1 {
		_, _ = fmt.Fprintln(out, "The team change may have been recorded. Inspect teams and reload the current revision before retrying; creation retries may create duplicates.")
	}
	return code
}

func reportAccessMutation(out io.Writer, e error) int {
	code := report(out, e)
	if code == 1 {
		_, _ = fmt.Fprintln(out, "The access change may have been recorded. Inspect access before retrying; creation retries may create duplicates.")
	}
	return code
}

func reportNotificationMutation(out io.Writer, e error) int {
	code := report(out, e)
	if code == 1 {
		_, _ = fmt.Fprintln(out, "The notification change may have been recorded. Inspect the destination, SMTP/grouping settings or delivery history before retrying. Reload revisions; repeated create/test/redelivery requests can duplicate work.")
	}
	return code
}

func reportAutomationMutation(out io.Writer, e error) int {
	code := report(out, e)
	if code == 1 {
		_, _ = fmt.Fprintln(out, "The automation request may have been recorded. Inspect templates, sources, versions, projects or validation history before retrying; retain exact inputs. Repeated publication/import requests can create additional records.")
	}
	return code
}
