package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

func deleteSecurityGroup(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	fail := func() *provider.PowerResult { out := PowerReadFailure(p.Phase); return &out }
	svc, err := AWSClient(r, client, 1)
	if err != nil {
		return fail()
	}
	result, err := svc.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{p.NativeID}})
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidGroup.NotFound" {
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
		}
		return fail()
	}
	if err != nil || len(result.SecurityGroups) != 1 || aws.ToString(result.SecurityGroups[0].GroupId) != p.NativeID {
		return fail()
	}
	v := result.SecurityGroups[0]
	if p.Phase == "observe" {
		return &provider.PowerResult{Outcome: "accepted", Status: "present"}
	}
	if aws.ToString(v.GroupName) == "" || aws.ToString(v.GroupName) == "default" || aws.ToString(v.VpcId) == "" || aws.ToString(v.OwnerId) == "" {
		return fail()
	}
	filter := func(name string) []types.Filter {
		return []types.Filter{{Name: aws.String(name), Values: []string{p.NativeID}}}
	}
	// Only an empty, complete dependency result allows deletion; no dependencies are removed.
	interfaces, err := svc.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{Filters: filter("group-id"), MaxResults: aws.Int32(5)})
	if err != nil || len(interfaces.NetworkInterfaces) != 0 || aws.ToString(interfaces.NextToken) != "" {
		return fail()
	}
	for _, name := range []string{"ip-permission.group-id", "egress.ip-permission.group-id"} {
		groups, err := svc.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: filter(name), MaxResults: aws.Int32(5)})
		if err != nil || aws.ToString(groups.NextToken) != "" {
			return fail()
		}
		for _, group := range groups.SecurityGroups {
			if aws.ToString(group.GroupId) != p.NativeID {
				return fail()
			}
		}
	}
	references, err := svc.DescribeSecurityGroupReferences(ctx, &ec2.DescribeSecurityGroupReferencesInput{GroupId: []string{p.NativeID}})
	if err != nil || len(references.SecurityGroupReferenceSet) != 0 {
		return fail()
	}
	associations, err := svc.DescribeSecurityGroupVpcAssociations(ctx, &ec2.DescribeSecurityGroupVpcAssociationsInput{Filters: filter("group-id"), MaxResults: aws.Int32(5)})
	if err != nil || len(associations.SecurityGroupVpcAssociations) != 0 || aws.ToString(associations.NextToken) != "" {
		return fail()
	}
	rules, err := json.Marshal(struct{ Inbound, Outbound []types.IpPermission }{v.IpPermissions, v.IpPermissionsEgress})
	if err != nil {
		return fail()
	}
	lines := []string{
		fmt.Sprintf("Delete AWS security group %s (%s), owner %s, VPC %s, region %s, and its rules.", p.NativeID, aws.ToString(v.GroupName), aws.ToString(v.OwnerId), aws.ToString(v.VpcId), r.Region),
		"Default groups, attached network interfaces, references from other groups and VPC associations block deletion. No dependency is detached or changed automatically.",
		"Future launches and external automation referring to this group may fail. No backup or undo is provided.",
		"Rules: " + string(rules),
	}
	return StorageDeletionResult(p, "present", lines, func() error {
		result, err := svc.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(p.NativeID)})
		if err == nil && !aws.ToBool(result.Return) {
			return errors.New("unconfirmed deletion")
		}
		return err
	})
}
