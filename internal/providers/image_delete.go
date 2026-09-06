package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

func deleteImage(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	fail := func() *provider.PowerResult { v := PowerReadFailure(p.Phase); return &v }
	gone := func() *provider.PowerResult {
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
		}
		return fail()
	}
	svc, err := AWSClient(r, client, 1)
	if err != nil {
		return fail()
	}
	// Read without an owner filter while observing: a hidden owned-list result is not proof of deletion.
	input := &ec2.DescribeImagesInput{ImageIds: []string{p.NativeID}, IncludeDisabled: aws.Bool(true), IncludeDeprecated: aws.Bool(true)}
	if p.Phase != "observe" {
		input.Owners = []string{"self"}
	}
	result, err := svc.DescribeImages(ctx, input)
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidAMIID.NotFound" {
		return gone()
	}
	if err != nil || result == nil || aws.ToString(result.NextToken) != "" {
		return fail()
	}
	if len(result.Images) == 0 {
		return gone()
	}
	if len(result.Images) != 1 || aws.ToString(result.Images[0].ImageId) != p.NativeID {
		return fail()
	}
	v := result.Images[0]
	if v.State == types.ImageStateDeregistered {
		return gone()
	}
	if p.Phase == "observe" {
		return &provider.PowerResult{Outcome: "accepted", Status: string(v.State)}
	}
	if aws.ToString(v.DeregistrationProtection) != "disabled" || aws.ToString(v.OwnerId) == "" || aws.ToString(v.CreationDate) == "" {
		return fail()
	}
	permissions, err := svc.DescribeImageAttribute(ctx, &ec2.DescribeImageAttributeInput{ImageId: aws.String(p.NativeID), Attribute: types.ImageAttributeNameLaunchPermission})
	if err != nil || permissions == nil || aws.ToString(permissions.ImageId) != p.NativeID {
		return fail()
	}
	lines := []string{
		fmt.Sprintf("Deregister AWS image %s (%s): owner %s; created %s; architecture %s; root device %s.", p.NativeID, aws.ToString(v.Name), aws.ToString(v.OwnerId), aws.ToString(v.CreationDate), v.Architecture, v.RootDeviceType),
		"New launches from this image will fail, including launch templates, scaling groups and external automation that still reference it. Review those dependencies before approval.",
		"Existing servers, all associated EBS snapshots and instance-store backing S3 files are retained. Their usage and storage charges continue.",
		"No automatic backup or undo is provided. AWS Recycle Bin retention applies only if a matching rule exists.",
	}
	for _, b := range v.BlockDeviceMappings {
		if b.Ebs != nil {
			if aws.ToString(b.Ebs.SnapshotId) == "" {
				return fail()
			}
			lines = append(lines, fmt.Sprintf("Retain snapshot %s for device %s.", aws.ToString(b.Ebs.SnapshotId), aws.ToString(b.DeviceName)))
		}
	}
	for _, grant := range permissions.LaunchPermissions {
		lines = append(lines, fmt.Sprintf("Remove image launch access: account %s; group %s; organization %s; organizational unit %s.", aws.ToString(grant.UserId), grant.Group, aws.ToString(grant.OrganizationArn), aws.ToString(grant.OrganizationalUnitArn)))
	}
	return StorageDeletionResult(p, string(v.State), lines, func() error {
		out, err := svc.DeregisterImage(ctx, &ec2.DeregisterImageInput{ImageId: aws.String(p.NativeID), DeleteAssociatedSnapshots: aws.Bool(false)})
		if err != nil {
			return err
		}
		if out == nil || !aws.ToBool(out.Return) {
			return errors.New("missing deregistration acknowledgement")
		}
		return nil
	})
}
