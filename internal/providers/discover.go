package providers

import (
	"context"
	"net/http"

	"github.com/alphabravo-oss/ab-provider-modules/inventory"
	"github.com/alphabravo-oss/providah-community/internal/awsauth"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
)

// Discover keeps the product protocol and validation around shared collectors.
func Discover(ctx context.Context, r provider.Request, client *http.Client) provider.Response {
	out := provider.Response{Version: provider.Protocol}
	if r.Validate() != nil {
		out.Error = "invalid_configuration"
		return out
	}
	kinds := r.InventoryKinds
	if len(kinds) == 0 {
		kinds = []string{"compute.server"}
	} else {
		out.InventoryKinds = kinds
	}
	request := inventory.Request{Provider: r.Provider, Region: r.Region, Credential: r.Credential}
	if r.Provider == "aws" {
		secret, err := awsauth.Parse(r.Credential)
		if err != nil || secret.RoleARN != "" || secret.AccountID != "" || secret.ExternalID != "" {
			out.Error = "discovery_failed"
			return out
		}
		request.AWS = aws.Credentials{AccessKeyID: secret.AccessKeyID, SecretAccessKey: secret.SecretAccessKey, SessionToken: secret.SessionToken}
	}
	rows, err := inventory.Collect(ctx, request, client, kinds)
	if err != nil {
		out.Error = "discovery_failed"
		return out
	}
	for _, row := range rows {
		resource := provider.Resource{ProviderIdentity: row.ProviderIdentity, Kind: row.Kind, NativeID: row.NativeID, Name: row.Name, Region: row.Region, Status: row.Status, PublicIP: row.PublicIP, PrivateIP: row.PrivateIP, Size: row.Size}
		if row.Tags != nil {
			resource.Tags = &provider.Tags{Labels: row.Tags.Labels, Names: row.Tags.Names}
		}
		out.Resources = append(out.Resources, resource)
	}
	out.Complete = true
	if out.Validate() != nil {
		return provider.Response{Version: provider.Protocol, Error: "invalid_provider_response"}
	}
	return out
}
