package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// Deletion is available only through explicit runtime capability and shared approval.
func deletePlacementGroup(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	fail := func() *provider.PowerResult { v := PowerReadFailure(p.Phase); return &v }
	gone := func() *provider.PowerResult {
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
		}
		return fail()
	}
	lines := []string{"Only an empty placement group is removed. No servers are removed or detached. External launch templates and automation referencing the group may fail; no automatic undo is available."}
	if r.Provider == "hetzner" {
		if r.Region != "global" {
			return fail()
		}
		id, err := strconv.ParseInt(p.NativeID, 10, 64)
		if err != nil {
			return fail()
		}
		svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
		v, response, err := svc.PlacementGroup.GetByID(ctx, id)
		if response != nil && response.StatusCode == 404 {
			return gone()
		}
		if err != nil || v == nil || v.ID != id {
			return fail()
		}
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "accepted", Status: "present"}
		}
		if len(v.Servers) != 0 || v.Type != hcloud.PlacementGroupTypeSpread || v.Name == "" || v.Created.IsZero() {
			return fail()
		}
		lines = append(lines, fmt.Sprintf("Delete Hetzner placement group %d (%s), type %s, created %s.", v.ID, v.Name, v.Type, v.Created.UTC().Format("2006-01-02T15:04:05Z")))
		return StorageDeletionResult(p, "present", lines, func() error {
			response, err := svc.PlacementGroup.Delete(ctx, v)
			if err != nil {
				return err
			}
			if response == nil || response.StatusCode != http.StatusNoContent {
				return errors.New("missing placement deletion acknowledgement")
			}
			return nil
		})
	}
	if r.Provider != "aws" {
		return fail()
	}
	svc, err := AWSClient(r, client, 1)
	if err != nil {
		return fail()
	}
	groups, err := svc.DescribePlacementGroups(ctx, &ec2.DescribePlacementGroupsInput{GroupIds: []string{p.NativeID}})
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidPlacementGroup.Unknown" {
		return gone()
	}
	if err != nil || groups == nil {
		return fail()
	}
	if len(groups.PlacementGroups) == 0 {
		return gone()
	}
	if len(groups.PlacementGroups) != 1 || aws.ToString(groups.PlacementGroups[0].GroupId) != p.NativeID {
		return fail()
	}
	v := groups.PlacementGroups[0]
	if v.State == types.PlacementGroupStateDeleted {
		return gone()
	}
	if p.Phase == "observe" {
		return &provider.PowerResult{Outcome: "accepted", Status: string(v.State)}
	}
	if v.State != types.PlacementGroupStateAvailable || aws.ToString(v.GroupName) == "" || aws.ToString(v.GroupArn) == "" {
		return fail()
	}
	instances, err := svc.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: []types.Filter{{Name: aws.String("placement-group-id"), Values: []string{p.NativeID}}}, MaxResults: aws.Int32(5)})
	if err != nil || instances == nil || aws.ToString(instances.NextToken) != "" {
		return fail()
	}
	for _, reservation := range instances.Reservations {
		if len(reservation.Instances) != 0 {
			return fail()
		}
	}
	lines = append(lines, fmt.Sprintf("Delete AWS placement group %s (%s), ARN %s, strategy %s, partitions %d, spread level %s.", p.NativeID, aws.ToString(v.GroupName), aws.ToString(v.GroupArn), v.Strategy, aws.ToInt32(v.PartitionCount), v.SpreadLevel), "AWS deletes by group name after identity inspection. Concurrent external changes can race this check; provider in-use checks still apply.")
	return StorageDeletionResult(p, "available", lines, func() error {
		result, err := svc.DeletePlacementGroup(ctx, &ec2.DeletePlacementGroupInput{GroupName: v.GroupName})
		if err != nil {
			return err
		}
		if result == nil {
			return errors.New("missing placement deletion acknowledgement")
		}
		response, ok := middleware.GetRawResponse(result.ResultMetadata).(*smithyhttp.Response)
		if !ok || response.Response == nil || response.StatusCode != http.StatusOK {
			return errors.New("missing placement deletion acknowledgement")
		}
		return nil
	})
}
