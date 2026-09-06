package providers

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/oauth2"
)

func createServer(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	var out provider.PowerResult
	switch r.Provider {
	case "aws":
		out = awsCreate(ctx, r, client)
	case "digitalocean":
		out = doCreate(ctx, r, client)
	case "hetzner":
		out = hetznerCreate(ctx, r, client)
	}
	return &out
}
func createAccepted(id, status string) provider.PowerResult {
	return provider.PowerResult{Outcome: "accepted", NativeID: id, Status: status}
}
func createUncertain() provider.PowerResult {
	return provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
}
func createObserved(p *provider.PowerRequest, id, status string) provider.PowerResult {
	if id == "" || p.NativeID != "" && p.NativeID != id {
		return createUncertain()
	}
	out := createAccepted(id, status)
	if status == "running" || status == "active" {
		out.Outcome = "succeeded"
	}
	return out
}

func awsCreate(ctx context.Context, r provider.Request, client *http.Client) provider.PowerResult {
	p, c := r.Power, r.Power.Create
	svc, err := AWSClient(r, client, 1)
	if err != nil {
		return provider.PowerResult{Outcome: "failed", Error: "invalid_configuration"}
	}
	if p.Phase == "observe" {
		result, e := svc.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: []types.Filter{{Name: aws.String("tag:providah-operation"), Values: []string{p.OperationID}}}})
		if e != nil {
			return PowerReadFailure(p.Phase)
		}
		found := []types.Instance{}
		for _, reservation := range result.Reservations {
			found = append(found, reservation.Instances...)
		}
		if len(found) != 1 || result.NextToken != nil || found[0].State == nil {
			return createUncertain()
		}
		marker := false
		for _, tag := range found[0].Tags {
			if aws.ToString(tag.Key) == "providah-operation" && aws.ToString(tag.Value) == p.OperationID {
				marker = true
			}
		}
		if !marker {
			return createUncertain()
		}
		return createObserved(p, aws.ToString(found[0].InstanceId), string(found[0].State.Name))
	}
	image, e := svc.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{c.Image}, Owners: []string{"self", "amazon"}})
	if e != nil || len(image.Images) != 1 || aws.ToString(image.Images[0].ImageId) != c.Image || image.Images[0].State != types.ImageStateAvailable || image.Images[0].RootDeviceType != types.DeviceTypeEbs || aws.ToString(image.Images[0].RootDeviceName) == "" {
		return PowerReadFailure(p.Phase)
	}
	size, e := svc.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{InstanceTypes: []types.InstanceType{types.InstanceType(c.Size)}})
	if e != nil || len(size.InstanceTypes) != 1 || string(size.InstanceTypes[0].InstanceType) != c.Size || size.InstanceTypes[0].ProcessorInfo == nil || !slices.Contains(size.InstanceTypes[0].ProcessorInfo.SupportedArchitectures, types.ArchitectureType(image.Images[0].Architecture)) {
		return PowerReadFailure(p.Phase)
	}
	key, e := svc.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{KeyPairIds: []string{c.SSHKey}})
	if e != nil || len(key.KeyPairs) != 1 || aws.ToString(key.KeyPairs[0].KeyPairId) != c.SSHKey || aws.ToString(key.KeyPairs[0].KeyName) == "" {
		return PowerReadFailure(p.Phase)
	}
	subnet, e := svc.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{c.Subnet}})
	if e != nil || len(subnet.Subnets) != 1 || aws.ToString(subnet.Subnets[0].SubnetId) != c.Subnet || subnet.Subnets[0].State != types.SubnetStateAvailable || aws.ToInt32(subnet.Subnets[0].AvailableIpAddressCount) < 1 {
		return PowerReadFailure(p.Phase)
	}
	group, e := svc.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{c.SecurityGroup}})
	if e != nil || len(group.SecurityGroups) != 1 || aws.ToString(group.SecurityGroups[0].GroupId) != c.SecurityGroup || aws.ToString(group.SecurityGroups[0].VpcId) == "" || aws.ToString(group.SecurityGroups[0].VpcId) != aws.ToString(subnet.Subnets[0].VpcId) {
		return PowerReadFailure(p.Phase)
	}
	offering, e := svc.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{LocationType: types.LocationTypeAvailabilityZone, Filters: []types.Filter{{Name: aws.String("location"), Values: []string{aws.ToString(subnet.Subnets[0].AvailabilityZone)}}, {Name: aws.String("instance-type"), Values: []string{c.Size}}}})
	if e != nil || len(offering.InstanceTypeOfferings) == 0 {
		return PowerReadFailure(p.Phase)
	}
	result, e := svc.RunInstances(ctx, &ec2.RunInstancesInput{ImageId: aws.String(c.Image), InstanceType: types.InstanceType(c.Size), KeyName: key.KeyPairs[0].KeyName, MinCount: aws.Int32(1), MaxCount: aws.Int32(1), ClientToken: aws.String(p.OperationID), NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{{DeviceIndex: aws.Int32(0), SubnetId: aws.String(c.Subnet), Groups: []string{c.SecurityGroup}, AssociatePublicIpAddress: aws.Bool(false), DeleteOnTermination: aws.Bool(true)}}, MetadataOptions: &types.InstanceMetadataOptionsRequest{HttpTokens: types.HttpTokensStateRequired}, BlockDeviceMappings: []types.BlockDeviceMapping{{DeviceName: image.Images[0].RootDeviceName, Ebs: &types.EbsBlockDevice{Encrypted: aws.Bool(true), DeleteOnTermination: aws.Bool(true)}}}, TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeInstance, Tags: []types.Tag{{Key: aws.String("Name"), Value: aws.String(c.Name)}, {Key: aws.String("providah-operation"), Value: aws.String(p.OperationID)}}}}})
	if e != nil || len(result.Instances) != 1 || aws.ToString(result.Instances[0].InstanceId) == "" {
		return createUncertain()
	}
	return createAccepted(aws.ToString(result.Instances[0].InstanceId), "pending")
}

func doCreate(ctx context.Context, r provider.Request, client *http.Client) provider.PowerResult {
	p, c := r.Power, r.Power.Create
	auth := &http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout}
	svc := godo.NewClient(auth)
	tag := "providah-operation-" + p.OperationID
	if p.Phase == "observe" {
		found, resp, e := svc.Droplets.ListByTag(ctx, tag, &godo.ListOptions{PerPage: 2})
		if e != nil {
			return PowerReadFailure(p.Phase)
		}
		if len(found) != 1 || resp != nil && resp.Links != nil && !resp.Links.IsLastPage() || found[0].Region == nil || found[0].Region.Slug != r.Region || !slices.Contains(found[0].Tags, tag) {
			return createUncertain()
		}
		if p.NativeID != "" && p.NativeID != strconv.Itoa(found[0].ID) {
			return createUncertain()
		}
		if c.Network != "" && found[0].VPCUUID != c.Network {
			return createAccepted(strconv.Itoa(found[0].ID), found[0].Status)
		}
		return createObserved(p, strconv.Itoa(found[0].ID), found[0].Status)
	}
	imageID, _ := strconv.Atoi(c.Image)
	keyID, _ := strconv.Atoi(c.SSHKey)
	image, _, e := svc.Images.GetByID(ctx, imageID)
	if e != nil || image == nil || image.ID != imageID || image.Status != "available" || !slices.Contains(image.Regions, r.Region) {
		return PowerReadFailure(p.Phase)
	}
	key, _, e := svc.Keys.GetByID(ctx, keyID)
	if e != nil || key == nil || key.ID != keyID {
		return PowerReadFailure(p.Phase)
	}
	eligible := false
	for page := 1; page <= 100; page++ {
		sizes, resp, e := svc.Sizes.List(ctx, &godo.ListOptions{Page: page, PerPage: 200})
		if e != nil {
			return PowerReadFailure(p.Phase)
		}
		for _, size := range sizes {
			if size.Slug == c.Size && size.Available && size.Disk >= image.MinDiskSize && slices.Contains(size.Regions, r.Region) {
				eligible = true
			}
		}
		if eligible || resp == nil || resp.Links == nil || resp.Links.IsLastPage() {
			break
		}
	}
	if !eligible {
		return PowerReadFailure(p.Phase)
	}
	if c.Network != "" {
		network, _, err := svc.VPCs.Get(ctx, c.Network)
		if err != nil || network == nil || network.ID != c.Network || network.RegionSlug != r.Region {
			return PowerReadFailure(p.Phase)
		}
	}
	server, _, e := svc.Droplets.Create(ctx, &godo.DropletCreateRequest{VPCUUID: c.Network, Name: c.Name, Region: r.Region, Size: c.Size, Image: godo.DropletCreateImage{ID: imageID}, SSHKeys: []godo.DropletCreateSSHKey{{ID: keyID}}, Tags: []string{tag}, IPv6: true})
	if e != nil || server == nil || server.ID <= 0 {
		return createUncertain()
	}
	return createAccepted(strconv.Itoa(server.ID), "new")
}

func hetznerCreate(ctx context.Context, r provider.Request, client *http.Client) provider.PowerResult {
	p, c := r.Power, r.Power.Create
	svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
	labels := map[string]string{"providah-operation": p.OperationID[:32], "providah-request": p.OperationID[32:]}
	if p.Phase == "observe" {
		found, resp, e := svc.Server.List(ctx, hcloud.ServerListOpts{ListOpts: hcloud.ListOpts{PerPage: 2, LabelSelector: "providah-operation=" + p.OperationID[:32] + ",providah-request=" + p.OperationID[32:]}})
		if e != nil {
			return PowerReadFailure(p.Phase)
		}
		if len(found) != 1 || resp != nil && resp.Meta.Pagination != nil && resp.Meta.Pagination.NextPage != 0 || found[0].Location == nil || found[0].Location.Name != r.Region || found[0].Labels["providah-operation"] != labels["providah-operation"] || found[0].Labels["providah-request"] != labels["providah-request"] {
			return createUncertain()
		}
		if p.NativeID != "" && p.NativeID != strconv.FormatInt(found[0].ID, 10) {
			return createUncertain()
		}
		if c.Network != "" {
			id, _ := strconv.ParseInt(c.Network, 10, 64)
			if found[0].PrivateNetFor(&hcloud.Network{ID: id}) == nil {
				return createAccepted(strconv.FormatInt(found[0].ID, 10), string(found[0].Status))
			}
		}
		return createObserved(p, strconv.FormatInt(found[0].ID, 10), string(found[0].Status))
	}
	imageID, _ := strconv.ParseInt(c.Image, 10, 64)
	keyID, _ := strconv.ParseInt(c.SSHKey, 10, 64)
	image, _, e := svc.Image.GetByID(ctx, imageID)
	if e != nil || image == nil || image.ID != imageID || image.IsDeleted() || image.Status != hcloud.ImageStatusAvailable {
		return PowerReadFailure(p.Phase)
	}
	size, _, e := svc.ServerType.GetByName(ctx, c.Size)
	if e != nil || size == nil || size.Name != c.Size || size.Architecture != image.Architecture || float32(size.Disk) < image.DiskSize {
		return PowerReadFailure(p.Phase)
	}
	location, _, e := svc.Location.GetByName(ctx, r.Region)
	if e != nil || location == nil || location.Name != r.Region {
		return PowerReadFailure(p.Phase)
	}
	available := false
	for _, entry := range size.Locations {
		if entry.Location != nil && entry.Location.ID == location.ID && entry.Available {
			available = true
		}
	}
	if !available {
		return PowerReadFailure(p.Phase)
	}
	key, _, e := svc.SSHKey.GetByID(ctx, keyID)
	if e != nil || key == nil || key.ID != keyID {
		return PowerReadFailure(p.Phase)
	}
	var networks []*hcloud.Network
	if c.Network != "" {
		id, _ := strconv.ParseInt(c.Network, 10, 64)
		network, _, err := svc.Network.GetByID(ctx, id)
		if err != nil || network == nil || network.ID != id || location.NetworkZone == "" {
			return PowerReadFailure(p.Phase)
		}
		eligible := false
		for _, subnet := range network.Subnets {
			if subnet.Type == hcloud.NetworkSubnetTypeCloud && subnet.NetworkZone == location.NetworkZone {
				eligible = true
			}
		}
		if !eligible {
			return PowerReadFailure(p.Phase)
		}
		networks = []*hcloud.Network{network}
	}
	server, _, e := svc.Server.Create(ctx, hcloud.ServerCreateOpts{Networks: networks, Name: c.Name, ServerType: size, Image: image, SSHKeys: []*hcloud.SSHKey{key}, Location: location, Labels: labels, StartAfterCreate: hcloud.Ptr(true)})
	if e != nil || server.Server == nil || server.Server.ID <= 0 {
		return createUncertain()
	}
	return createAccepted(strconv.FormatInt(server.Server.ID, 10), "initializing")
}
