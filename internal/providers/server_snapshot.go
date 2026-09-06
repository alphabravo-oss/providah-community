package providers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/oauth2"
)

func snapshotObserved(id, status string) *provider.PowerResult {
	out := &provider.PowerResult{Outcome: "accepted", ActionID: id, Status: "snapshot_pending"}
	switch status {
	case "available", "completed":
		out.Outcome = "succeeded"
		out.Status = "snapshot_available"
	case "failed", "error", "errored":
		out.Outcome = "failed"
		out.Error = "provider_failed"
	}
	return out
}

// Snapshot submissions are never retried; observations use the returned image/action ID.
func snapshotServer(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	name := "providah-" + p.OperationID
	readFailure := func() *provider.PowerResult { out := PowerReadFailure(p.Phase); return &out }
	uncertain := func() *provider.PowerResult {
		return &provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
	}
	changed := func() *provider.PowerResult { return &provider.PowerResult{Outcome: "failed", Error: "state_changed"} }
	if p.Phase == "observe" && p.ActionID == "" {
		return readFailure()
	}
	switch r.Provider {
	case "aws":
		svc, e := AWSClient(r, client, 1)
		if e != nil {
			return readFailure()
		}
		if p.Phase == "observe" {
			result, e := svc.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{p.ActionID}, Owners: []string{"self"}})
			if e != nil || len(result.Images) != 1 {
				return readFailure()
			}
			v := result.Images[0]
			if aws.ToString(v.ImageId) != p.ActionID || aws.ToString(v.Name) != name || aws.ToString(v.SourceInstanceId) != p.NativeID {
				return readFailure()
			}
			return snapshotObserved(p.ActionID, string(v.State))
		}
		result, e := svc.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{p.NativeID}})
		if e != nil || len(result.Reservations) != 1 || len(result.Reservations[0].Instances) != 1 {
			return readFailure()
		}
		v := result.Reservations[0].Instances[0]
		if aws.ToString(v.InstanceId) != p.NativeID || v.State == nil {
			return readFailure()
		}
		if v.RootDeviceType != types.DeviceTypeEbs || PowerStateMismatch(p, string(v.State.Name)) {
			return changed()
		}
		image, e := svc.CreateImage(ctx, &ec2.CreateImageInput{InstanceId: aws.String(p.NativeID), Name: aws.String(name), NoReboot: aws.Bool(true)})
		if e != nil || image.ImageId == nil || *image.ImageId == "" {
			return uncertain()
		}
		return snapshotObserved(*image.ImageId, "pending")
	case "digitalocean":
		id, e := strconv.Atoi(p.NativeID)
		if e != nil {
			return readFailure()
		}
		auth := &http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout}
		svc := godo.NewClient(auth)
		if p.Phase == "observe" {
			actionID, e := strconv.Atoi(p.ActionID)
			if e != nil {
				return readFailure()
			}
			action, _, e := svc.DropletActions.Get(ctx, id, actionID)
			if e != nil || action == nil || action.ID != actionID || action.ResourceID != id || action.Type != "snapshot" || action.ResourceType != "droplet" {
				return readFailure()
			}
			return snapshotObserved(p.ActionID, action.Status)
		}
		v, _, e := svc.Droplets.Get(ctx, id)
		if e != nil || v == nil || v.ID != id || v.Region == nil || v.Region.Slug != r.Region {
			return readFailure()
		}
		if v.Locked || PowerStateMismatch(p, v.Status) {
			return changed()
		}
		action, _, e := svc.DropletActions.Snapshot(ctx, id, name)
		if e != nil || action == nil || action.ID <= 0 {
			return uncertain()
		}
		return snapshotObserved(strconv.Itoa(action.ID), "pending")
	case "hetzner":
		id, e := strconv.ParseInt(p.NativeID, 10, 64)
		if e != nil {
			return readFailure()
		}
		svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
		if p.Phase == "observe" {
			imageID, e := strconv.ParseInt(p.ActionID, 10, 64)
			if e != nil {
				return readFailure()
			}
			v, _, e := svc.Image.GetByID(ctx, imageID)
			if e != nil || v == nil || v.ID != imageID || v.Description != name || v.Type != hcloud.ImageTypeSnapshot || v.CreatedFrom == nil || v.CreatedFrom.ID != id {
				return readFailure()
			}
			return snapshotObserved(p.ActionID, string(v.Status))
		}
		v, _, e := svc.Server.GetByID(ctx, id)
		if e != nil || v == nil || v.ID != id || v.Location == nil || v.Location.Name != r.Region {
			return readFailure()
		}
		if PowerStateMismatch(p, string(v.Status)) {
			return changed()
		}
		image, _, e := svc.Server.CreateImage(ctx, v, &hcloud.ServerCreateImageOpts{Type: hcloud.ImageTypeSnapshot, Description: &name})
		if e != nil || image.Image == nil || image.Image.ID <= 0 {
			return uncertain()
		}
		return snapshotObserved(strconv.FormatInt(image.Image.ID, 10), "pending")
	}
	return &provider.PowerResult{Outcome: "failed", Error: "invalid_configuration"}
}
