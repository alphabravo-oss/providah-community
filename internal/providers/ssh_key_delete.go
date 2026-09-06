package providers

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/oauth2"
	"net/http"
	"strconv"
)

func deleteSSHKey(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	fail := func() *provider.PowerResult { out := PowerReadFailure(p.Phase); return &out }
	gone := func() *provider.PowerResult {
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
		}
		return fail()
	}
	name, fingerprint := "", ""
	var remove func() error
	switch r.Provider {
	case "aws":
		svc, e := AWSClient(r, client, 1)
		if e != nil {
			return fail()
		}
		result, e := svc.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{Filters: []types.Filter{{Name: aws.String("key-pair-id"), Values: []string{p.NativeID}}}})
		if e != nil {
			return fail()
		}
		if len(result.KeyPairs) == 0 {
			return gone()
		}
		if len(result.KeyPairs) != 1 || aws.ToString(result.KeyPairs[0].KeyPairId) != p.NativeID {
			return fail()
		}
		name, fingerprint = aws.ToString(result.KeyPairs[0].KeyName), aws.ToString(result.KeyPairs[0].KeyFingerprint)
		remove = func() error {
			_, e := svc.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{KeyPairId: aws.String(p.NativeID)})
			return e
		}
	case "digitalocean":
		if r.Region != "global" {
			return fail()
		}
		svc := godo.NewClient(&http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout})
		id, e := strconv.Atoi(p.NativeID)
		if e != nil {
			return fail()
		}
		key, response, e := svc.Keys.GetByID(ctx, id)
		if response != nil && response.StatusCode == 404 {
			return gone()
		}
		if e != nil || key == nil || key.ID != id {
			return fail()
		}
		name, fingerprint = key.Name, key.Fingerprint
		remove = func() error { _, e := svc.Keys.DeleteByID(ctx, id); return e }
	case "hetzner":
		if r.Region != "global" {
			return fail()
		}
		svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
		id, e := strconv.ParseInt(p.NativeID, 10, 64)
		if e != nil {
			return fail()
		}
		key, response, e := svc.SSHKey.GetByID(ctx, id)
		if response != nil && response.StatusCode == 404 {
			return gone()
		}
		if e != nil || key == nil || key.ID != id {
			return fail()
		}
		name, fingerprint = key.Name, key.Fingerprint
		remove = func() error { _, e := svc.SSHKey.Delete(ctx, key); return e }
	default:
		return fail()
	}
	if name == "" || fingerprint == "" {
		return fail()
	}
	lines := []string{
		fmt.Sprintf("Delete %s SSH key record %s (%s), fingerprint %s, scope %s.", r.Provider, p.NativeID, name, fingerprint, r.Region),
		"This removes the provider's key record. It does not revoke existing server access or remove private keys. Change authorized keys on servers separately to revoke access.",
		"Future launches or automation referring to this key may fail. Update launch templates, schedules and external automation before approving. No automatic reference update, backup or undo is provided.",
	}
	return StorageDeletionResult(p, "present", lines, remove)
}
