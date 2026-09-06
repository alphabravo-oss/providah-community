package providers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/oauth2"
)

// No mutation retries: a lost response is not proof that the provider did nothing.
func Power(ctx context.Context, r provider.Request, client *http.Client) provider.Response {
	result := provider.PowerResult{Outcome: "failed", Error: "invalid_configuration"}
	if r.Validate() == nil && r.Power != nil && provider.CommunityAction(r.Provider, r.Power.ResourceKind) {
		if r.Power.Action == "create" && r.Power.ResourceKind == "access.ssh_key" {
			return provider.Response{Version: provider.Protocol, Power: createSSHKey(ctx, r, client)}
		}
		if r.Power.Action == "tags" {
			return provider.Response{Version: provider.Protocol, Power: tagServer(ctx, r, client)}
		}
		if r.Power.ResourceKind == "compute.placement_group" {
			return provider.Response{Version: provider.Protocol, Power: deletePlacementGroup(ctx, r, client)}
		}
		if r.Power.ResourceKind == "organization.project" {
			return provider.Response{Version: provider.Protocol, Power: deleteCloudProject(ctx, r, client)}
		}
		if r.Power.ResourceKind == "compute.image" {
			return provider.Response{Version: provider.Protocol, Power: deleteImage(ctx, r, client)}
		}
		if r.Power.ResourceKind == "network.load_balancer" {
			return provider.Response{Version: provider.Protocol, Power: deleteLoadBalancer(ctx, r, client)}
		}
		if r.Power.ResourceKind == "access.ssh_key" {
			return provider.Response{Version: provider.Protocol, Power: deleteSSHKey(ctx, r, client)}
		}
		if provider.NetworkDeleteKind(r.Power.ResourceKind) {
			return provider.Response{Version: provider.Protocol, Power: deleteNetworkResource(ctx, r, client)}
		}
		if r.Power.ResourceKind == "storage.volume" {
			return provider.Response{Version: provider.Protocol, Power: deleteVolume(ctx, r, client)}
		}
		if r.Power.ResourceKind == "storage.snapshot" {
			return provider.Response{Version: provider.Protocol, Power: deleteSnapshot(ctx, r, client)}
		}
		if r.Power.Action == "snapshot" {
			return provider.Response{Version: provider.Protocol, Power: snapshotServer(ctx, r, client)}
		}
		if r.Power.Action == "create" {
			return provider.Response{Version: provider.Protocol, Power: createServer(ctx, r, client)}
		}
		switch r.Provider {
		case "aws":
			result = awsPower(ctx, r, client)
		case "digitalocean":
			result = doPower(ctx, r, client)
		case "hetzner":
			result = hetznerPower(ctx, r, client)
		}
	}
	return provider.Response{Version: provider.Protocol, Power: &result}
}
func PowerReadFailure(phase string) provider.PowerResult {
	if phase != "observe" {
		return provider.PowerResult{Outcome: "failed", Error: "preflight_failed"}
	}
	return provider.PowerResult{Outcome: "accepted", Error: "observation_failed"}
}
func PowerStateMismatch(p *provider.PowerRequest, status string) bool {
	return status != p.ExpectedStatus || !provider.ResourceActionAllowed(p.ResourceKind, p.Action, status)
}
func PowerObserved(p *provider.PowerRequest, status, actionStatus string) provider.PowerResult {
	out := provider.PowerResult{Outcome: "accepted", ActionID: p.ActionID, Status: status}
	if actionStatus == "error" || actionStatus == "errored" {
		out.Outcome = "failed"
		out.Error = "provider_failed"
		return out
	}
	if provider.ResourceActionReached(p.ResourceKind, p.Action, status) && (p.Action != "restart" || actionStatus == "success" || actionStatus == "completed") {
		out.Outcome = "succeeded"
	}
	return out
}
func awsPower(ctx context.Context, r provider.Request, client *http.Client) provider.PowerResult {
	p := r.Power
	svc, err := AWSClient(r, client, 1)
	if err != nil {
		return provider.PowerResult{Outcome: "failed", Error: "invalid_configuration"}
	}
	state, err := svc.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{p.NativeID}})
	var apiErr smithy.APIError
	if p.Action == "delete" && p.Phase == "observe" && errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidInstanceID.NotFound" {
		return provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
	}
	if err != nil || len(state.Reservations) != 1 || len(state.Reservations[0].Instances) != 1 {
		return PowerReadFailure(p.Phase)
	}
	server := state.Reservations[0].Instances[0]
	if aws.ToString(server.InstanceId) != p.NativeID || server.State == nil {
		return PowerReadFailure(p.Phase)
	}
	status := string(server.State.Name)
	if p.Action == "resize" {
		size := string(server.InstanceType)
		if p.Phase == "observe" {
			return resizeObserved(p, status, size, "")
		}
		if server.RootDeviceType != types.DeviceTypeEbs || size != p.ExpectedSize || PowerStateMismatch(p, status) {
			return provider.PowerResult{Outcome: "failed", Error: "state_changed", Status: status}
		}
		_, err = svc.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{InstanceId: aws.String(p.NativeID), InstanceType: &types.AttributeValue{Value: aws.String(p.TargetSize)}})
		if err != nil {
			return provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
		}
		return provider.PowerResult{Outcome: "accepted", Status: status}
	}
	if p.Phase == "observe" {
		if p.Action == "restart" {
			return provider.PowerResult{Outcome: "uncertain", Status: status, Error: "restart_unverifiable"}
		}
		return PowerObserved(p, status, "")
	}
	if p.Action == "delete" {
		lines, e := awsDeletionImpact(server)
		if e != nil {
			return PowerReadFailure(p.Phase)
		}
		if out := deletionReview(p, lines, status); out != nil {
			return *out
		}
	}
	if PowerStateMismatch(p, status) {
		return provider.PowerResult{Outcome: "failed", Status: status, Error: "state_changed"}
	}
	switch p.Action {
	case "delete":
		_, err = svc.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{p.NativeID}})
	case "start":
		_, err = svc.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{p.NativeID}})
	case "shutdown":
		_, err = svc.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{p.NativeID}})
	case "restart":
		_, err = svc.RebootInstances(ctx, &ec2.RebootInstancesInput{InstanceIds: []string{p.NativeID}})
	}
	if err != nil {
		return provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
	}
	return provider.PowerResult{Outcome: "accepted", Status: status}
}
func doPower(ctx context.Context, r provider.Request, client *http.Client) provider.PowerResult {
	p := r.Power
	id, err := strconv.Atoi(p.NativeID)
	if err != nil {
		return provider.PowerResult{Outcome: "failed", Error: "invalid_configuration"}
	}
	auth := &http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout}
	svc := godo.NewClient(auth) // Retries are opt-in in godo; leave them disabled.
	server, resp, err := svc.Droplets.Get(ctx, id)
	if p.Action == "delete" && p.Phase == "observe" && resp != nil && resp.StatusCode == http.StatusNotFound {
		return provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
	}
	if err != nil || server == nil || server.ID != id || server.Region == nil || server.Region.Slug != r.Region {
		return PowerReadFailure(p.Phase)
	}
	if p.Phase == "observe" {
		actionStatus := ""
		if p.ActionID != "" {
			actionID, e := strconv.Atoi(p.ActionID)
			if e != nil {
				return PowerReadFailure(p.Phase)
			}
			action, _, e := svc.DropletActions.Get(ctx, id, actionID)
			if e != nil || action == nil {
				return PowerReadFailure(p.Phase)
			}
			actionStatus = action.Status
		}
		if p.Action == "restart" && p.ActionID == "" {
			return provider.PowerResult{Outcome: "uncertain", Status: server.Status, Error: "restart_unverifiable"}
		}
		if p.Action == "resize" {
			return resizeObserved(p, server.Status, server.SizeSlug, actionStatus)
		}
		return PowerObserved(p, server.Status, actionStatus)
	}
	if p.Action == "delete" {
		if out := deletionReview(p, doDeletionImpact(server), server.Status); out != nil {
			return *out
		}
	}
	if PowerStateMismatch(p, server.Status) || (p.Action == "resize" && (server.SizeSlug != p.ExpectedSize || server.Locked)) {
		return provider.PowerResult{Outcome: "failed", Status: server.Status, Error: "state_changed"}
	}
	if p.Action == "delete" {
		_, err = svc.Droplets.Delete(ctx, id)
		if err != nil {
			return provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
		}
		return provider.PowerResult{Outcome: "accepted", Status: server.Status}
	}
	var action *godo.Action
	switch p.Action {
	case "resize":
		action, _, err = svc.DropletActions.Resize(ctx, id, p.TargetSize, false)
	case "start":
		action, _, err = svc.DropletActions.PowerOn(ctx, id)
	case "shutdown":
		action, _, err = svc.DropletActions.Shutdown(ctx, id)
	case "restart":
		action, _, err = svc.DropletActions.Reboot(ctx, id)
	}
	if err != nil || action == nil || action.ID <= 0 {
		return provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
	}
	return provider.PowerResult{Outcome: "accepted", ActionID: strconv.Itoa(action.ID), Status: server.Status}
}
func hetznerPower(ctx context.Context, r provider.Request, client *http.Client) provider.PowerResult {
	p := r.Power
	id, err := strconv.ParseInt(p.NativeID, 10, 64)
	if err != nil {
		return provider.PowerResult{Outcome: "failed", Error: "invalid_configuration"}
	}
	svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
	server, resp, err := svc.Server.GetByID(ctx, id)
	if p.Action == "delete" && p.Phase == "observe" && resp != nil && resp.StatusCode == http.StatusNotFound {
		return provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
	}
	if err != nil || server == nil || server.ID != id || server.Location == nil || server.Location.Name != r.Region {
		return PowerReadFailure(p.Phase)
	}
	status := string(server.Status)
	if p.Phase == "observe" {
		actionStatus := ""
		if p.ActionID != "" {
			actionID, e := strconv.ParseInt(p.ActionID, 10, 64)
			if e != nil {
				return PowerReadFailure(p.Phase)
			}
			action, _, e := svc.Action.GetByID(ctx, actionID)
			if e != nil || action == nil {
				return PowerReadFailure(p.Phase)
			}
			actionStatus = string(action.Status)
		}
		if p.Action == "restart" && p.ActionID == "" {
			return provider.PowerResult{Outcome: "uncertain", Status: status, Error: "restart_unverifiable"}
		}
		if p.Action == "resize" {
			if server.ServerType == nil {
				return PowerReadFailure(p.Phase)
			}
			return resizeObserved(p, status, server.ServerType.Name, actionStatus)
		}
		return PowerObserved(p, status, actionStatus)
	}
	if p.Action == "delete" {
		lines, e := hetznerDeletionImpact(ctx, svc, server)
		if e != nil {
			return PowerReadFailure(p.Phase)
		}
		if out := deletionReview(p, lines, status); out != nil {
			return *out
		}
	}
	if PowerStateMismatch(p, status) || (p.Action == "resize" && (server.ServerType == nil || server.ServerType.Name != p.ExpectedSize)) {
		return provider.PowerResult{Outcome: "failed", Status: status, Error: "state_changed"}
	}
	if p.Action == "delete" {
		_, _, err = svc.Server.DeleteWithResult(ctx, server)
		if err != nil {
			return provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
		}
		return provider.PowerResult{Outcome: "accepted", Status: status}
	}
	var action *hcloud.Action
	switch p.Action {
	case "resize":
		action, _, err = svc.Server.ChangeType(ctx, server, hcloud.ServerChangeTypeOpts{ServerType: &hcloud.ServerType{Name: p.TargetSize}, UpgradeDisk: false})
	case "start":
		action, _, err = svc.Server.Poweron(ctx, server)
	case "shutdown":
		action, _, err = svc.Server.Shutdown(ctx, server)
	case "restart":
		action, _, err = svc.Server.Reboot(ctx, server)
	default:
		err = errors.New("unsupported action")
	}
	if err != nil || action == nil || action.ID <= 0 {
		return provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
	}
	return provider.PowerResult{Outcome: "accepted", ActionID: strconv.FormatInt(action.ID, 10), Status: status}
}

func resizeObserved(p *provider.PowerRequest, status, size, actionStatus string) provider.PowerResult {
	out := provider.PowerResult{Outcome: "accepted", Status: status, Size: size, ActionID: p.ActionID}
	if actionStatus == "error" || actionStatus == "errored" {
		out.Outcome = "failed"
		out.Error = "provider_failed"
	} else if size == p.TargetSize && (p.ActionID == "" || actionStatus == "success" || actionStatus == "completed") {
		out.Outcome = "succeeded"
	}
	return out
}
