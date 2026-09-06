package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
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

func deleteSnapshot(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	fail := func() *provider.PowerResult { v := PowerReadFailure(p.Phase); return &v }
	gone := func() *provider.PowerResult {
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
		}
		return fail()
	}
	lines := []string{"Delete this standalone snapshot. Its recovery copy is lost; existing source resources are retained.", "No automatic backup or undo is provided. External automation or retention policies may depend on this snapshot; review those dependencies before approval."}
	status := ""
	var remove func() error
	switch r.Provider {
	case "aws":
		svc, err := AWSClient(r, client, 1)
		if err != nil {
			return fail()
		}
		result, err := svc.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{SnapshotIds: []string{p.NativeID}})
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidSnapshot.NotFound" {
			return gone()
		}
		if err != nil || len(result.Snapshots) != 1 || aws.ToString(result.Snapshots[0].SnapshotId) != p.NativeID {
			return fail()
		}
		v := result.Snapshots[0]
		status = string(v.State)
		if p.Phase != "observe" {
			// The provider enforces deletion ownership. Also require the exact snapshot to appear in owned inventory.
			owned, e := svc.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{SnapshotIds: []string{p.NativeID}, OwnerIds: []string{"self"}})
			if e != nil || len(owned.Snapshots) != 1 || aws.ToString(owned.Snapshots[0].SnapshotId) != p.NativeID {
				return fail()
			}
			images, e := svc.DescribeImages(ctx, &ec2.DescribeImagesInput{Owners: []string{"self"}, IncludeDisabled: aws.Bool(true), IncludeDeprecated: aws.Bool(true), Filters: []types.Filter{{Name: aws.String("block-device-mapping.snapshot-id"), Values: []string{p.NativeID}}}, MaxResults: aws.Int32(1000)})
			if e != nil || len(images.Images) > 0 || aws.ToString(images.NextToken) != "" {
				return fail()
			}
			permissions, e := svc.DescribeSnapshotAttribute(ctx, &ec2.DescribeSnapshotAttributeInput{SnapshotId: aws.String(p.NativeID), Attribute: types.SnapshotAttributeNameCreateVolumePermission})
			if e != nil {
				return fail()
			}
			for _, grant := range permissions.CreateVolumePermissions {
				lines = append(lines, fmt.Sprintf("Remove access for shared account %s / group %s.", aws.ToString(grant.UserId), grant.Group))
			}
		}
		lines = append(lines, fmt.Sprintf("AWS snapshot %s: %s; source volume %s; %d GiB; encrypted=%t.", p.NativeID, aws.ToString(v.Description), aws.ToString(v.VolumeId), aws.ToInt32(v.VolumeSize), aws.ToBool(v.Encrypted)))
		remove = func() error {
			_, e := svc.DeleteSnapshot(ctx, &ec2.DeleteSnapshotInput{SnapshotId: aws.String(p.NativeID)})
			return e
		}
	case "digitalocean":
		svc := godo.NewClient(&http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout})
		v, response, err := svc.Snapshots.Get(ctx, p.NativeID)
		if response != nil && response.StatusCode == 404 {
			return gone()
		}
		if err != nil || v == nil || v.ID != p.NativeID || !slices.Contains(v.Regions, r.Region) || !slices.Contains([]string{"droplet", "volume"}, v.ResourceType) || v.ResourceID == "" {
			return fail()
		}
		status = "present"
		regions := slices.Clone(v.Regions)
		slices.Sort(regions)
		lines = append(lines, fmt.Sprintf("DigitalOcean snapshot %s (%s): source %s %s; created %s; regions %v. Delete the snapshot across all regions listed.", v.ID, v.Name, v.ResourceType, v.ResourceID, v.Created, regions))
		remove = func() error { _, e := svc.Snapshots.Delete(ctx, p.NativeID); return e }
	case "hetzner":
		if r.Region != "global" {
			return fail()
		}
		svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
		id, err := strconv.ParseInt(p.NativeID, 10, 64)
		if err != nil {
			return fail()
		}
		v, response, err := svc.Image.GetByID(ctx, id)
		if response != nil && response.StatusCode == 404 {
			return gone()
		}
		if err != nil || v == nil || v.ID != id || v.Type != hcloud.ImageTypeSnapshot {
			return fail()
		}
		if v.IsDeleted() {
			return gone()
		}
		status = string(v.Status)
		if p.Phase != "observe" && (v.Protection.Delete || v.BoundTo != nil) {
			return fail()
		}
		lines = append(lines, fmt.Sprintf("Hetzner snapshot %d (%s): created %s; architecture %s.", v.ID, v.Description, v.Created.UTC().Format("2006-01-02T15:04:05Z"), v.Architecture))
		remove = func() error { _, e := svc.Image.Delete(ctx, v); return e }
	default:
		return fail()
	}
	return StorageDeletionResult(p, status, lines, remove)
}
