package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/smithy-go"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/oauth2"
)

func deleteVolume(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	fail := func() *provider.PowerResult { out := PowerReadFailure(p.Phase); return &out }
	gone := func() *provider.PowerResult {
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
		}
		return fail()
	}
	lines := []string{"Delete this detached volume and all data stored on it. No server is detached automatically.", "Existing standalone snapshots are retained. No new snapshot or undo is provided. Review external application and retention dependencies before approval."}
	status := ""
	var remove func() error
	switch r.Provider {
	case "aws":
		svc, err := AWSClient(r, client, 1)
		if err != nil {
			return fail()
		}
		result, err := svc.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{p.NativeID}})
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidVolume.NotFound" {
			return gone()
		}
		if err != nil || len(result.Volumes) != 1 || aws.ToString(result.Volumes[0].VolumeId) != p.NativeID {
			return fail()
		}
		v := result.Volumes[0]
		status = string(v.State)
		if !strings.HasPrefix(aws.ToString(v.AvailabilityZone), r.Region) {
			return fail()
		}
		if p.Phase != "observe" && len(v.Attachments) > 0 {
			return fail()
		}
		lines = append(lines, fmt.Sprintf("AWS volume %s: %d GiB, type %s, zone %s, encrypted=%t, snapshot origin %s.", p.NativeID, aws.ToInt32(v.Size), v.VolumeType, aws.ToString(v.AvailabilityZone), aws.ToBool(v.Encrypted), aws.ToString(v.SnapshotId)))
		remove = func() error {
			_, e := svc.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(p.NativeID)})
			return e
		}
	case "digitalocean":
		svc := godo.NewClient(&http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout})
		v, response, err := svc.Storage.GetVolume(ctx, p.NativeID)
		if response != nil && response.StatusCode == 404 {
			return gone()
		}
		if err != nil || v == nil || v.ID != p.NativeID || v.Region == nil || v.Region.Slug != r.Region {
			return fail()
		}
		status = "available"
		if len(v.DropletIDs) > 0 {
			status = "attached"
		}
		if p.Phase != "observe" && len(v.DropletIDs) > 0 {
			return fail()
		}
		lines = append(lines, fmt.Sprintf("DigitalOcean volume %s (%s): %d GiB in %s; filesystem %s.", v.ID, v.Name, v.SizeGigaBytes, v.Region.Slug, v.FilesystemType))
		remove = func() error { _, e := svc.Storage.DeleteVolume(ctx, p.NativeID); return e }
	case "hetzner":
		svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
		id, err := strconv.ParseInt(p.NativeID, 10, 64)
		if err != nil {
			return fail()
		}
		v, response, err := svc.Volume.GetByID(ctx, id)
		if response != nil && response.StatusCode == 404 {
			return gone()
		}
		if err != nil || v == nil || v.ID != id || v.Location == nil || v.Location.Name != r.Region {
			return fail()
		}
		status = string(v.Status)
		if v.Server != nil {
			status = "attached"
		}
		if p.Phase != "observe" && (v.Server != nil || v.Protection.Delete) {
			return fail()
		}
		format := ""
		if v.Format != nil {
			format = *v.Format
		}
		lines = append(lines, fmt.Sprintf("Hetzner volume %d (%s): %d GiB in %s; format %s.", v.ID, v.Name, v.Size, v.Location.Name, format))
		remove = func() error { _, e := svc.Volume.Delete(ctx, v); return e }
	default:
		return fail()
	}
	return StorageDeletionResult(p, status, lines, remove)
}
