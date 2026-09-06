package providers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/crypto/ssh"
	"golang.org/x/oauth2"
)

func createSSHKey(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p, c := r.Power, r.Power.KeyCreate
	uncertain := func() *provider.PowerResult { v := createUncertain(); return &v }
	fail := func() *provider.PowerResult { v := PowerReadFailure(p.Phase); return &v }
	// The full operation suffix allows reconciliation after a lost create response.
	name := c.Name + "-" + p.OperationID
	observed := func(id, actualName, key string) *provider.PowerResult {
		if id == "" || (p.NativeID != "" && p.NativeID != id) || actualName != name || !provider.PublicKeysEqual(c.PublicKey, key) {
			return uncertain()
		}
		return &provider.PowerResult{Outcome: "succeeded", NativeID: id, Status: "present"}
	}
	accepted := func(id string) *provider.PowerResult {
		if id == "" {
			return uncertain()
		}
		return &provider.PowerResult{Outcome: "accepted", NativeID: id, Status: "present"}
	}
	switch r.Provider {
	case "aws":
		svc, err := AWSClient(r, client, 1)
		if err != nil {
			return fail()
		}
		if p.Phase == "observe" {
			result, err := svc.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{KeyNames: []string{name}, IncludePublicKey: aws.Bool(true)})
			if err != nil {
				return fail()
			}
			if result == nil || len(result.KeyPairs) != 1 {
				return uncertain()
			}
			key := result.KeyPairs[0]
			return observed(aws.ToString(key.KeyPairId), aws.ToString(key.KeyName), aws.ToString(key.PublicKey))
		}
		key, err := svc.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{KeyName: aws.String(name), PublicKeyMaterial: []byte(strings.TrimSpace(c.PublicKey))})
		if err != nil || key == nil || aws.ToString(key.KeyName) != name {
			return uncertain()
		}
		return accepted(aws.ToString(key.KeyPairId))
	case "hetzner":
		if r.Region != "global" {
			return fail()
		}
		svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
		if p.Phase == "observe" {
			key, _, err := svc.SSHKey.GetByName(ctx, name)
			if err != nil {
				return fail()
			}
			if key == nil {
				return uncertain()
			}
			return observed(strconv.FormatInt(key.ID, 10), key.Name, key.PublicKey)
		}
		key, _, err := svc.SSHKey.Create(ctx, hcloud.SSHKeyCreateOpts{Name: name, PublicKey: c.PublicKey})
		if err != nil || key == nil || key.ID <= 0 || key.Name != name {
			return uncertain()
		}
		return accepted(strconv.FormatInt(key.ID, 10))
	case "digitalocean":
		if r.Region != "global" {
			return fail()
		}
		svc := godo.NewClient(&http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout})
		if p.Phase == "observe" {
			parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.PublicKey))
			if err != nil {
				return fail()
			}
			key, _, err := svc.Keys.GetByFingerprint(ctx, ssh.FingerprintLegacyMD5(parsed))
			if err != nil {
				return fail()
			}
			if key == nil {
				return uncertain()
			}
			return observed(strconv.Itoa(key.ID), key.Name, key.PublicKey)
		}
		key, _, err := svc.Keys.Create(ctx, &godo.KeyCreateRequest{Name: name, PublicKey: c.PublicKey})
		if err != nil || key == nil || key.ID <= 0 || key.Name != name {
			return uncertain()
		}
		return accepted(strconv.Itoa(key.ID))
	}
	return fail()
}
