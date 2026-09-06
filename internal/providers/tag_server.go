package providers

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/oauth2"
)

// Tag writes use the persisted, independently approved expected and target sets.
func tagServer(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	var current *provider.Tags
	var status string
	var write func() error
	switch r.Provider {
	case "aws":
		svc, e := AWSClient(r, client, 1)
		if e != nil {
			return &provider.PowerResult{Outcome: "failed", Error: "invalid_configuration"}
		}
		result, e := svc.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{p.NativeID}})
		if e != nil || len(result.Reservations) != 1 || len(result.Reservations[0].Instances) != 1 {
			return tagReadFailure(p.Phase)
		}
		instance := result.Reservations[0].Instances[0]
		if aws.ToString(instance.InstanceId) != p.NativeID || instance.State == nil {
			return tagReadFailure(p.Phase)
		}
		status = string(instance.State.Name)
		current = ec2Labels(instance.Tags)
		write = func() error {
			add, remove := []types.Tag{}, []types.Tag{}
			keys := make([]string, 0, len(p.TargetTags.Labels))
			for k := range p.TargetTags.Labels {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				v := p.TargetTags.Labels[k]
				if old, ok := current.Labels[k]; !ok || old != v {
					add = append(add, types.Tag{Key: aws.String(k), Value: aws.String(v)})
				}
			}
			keys = keys[:0]
			for k := range current.Labels {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if _, ok := p.TargetTags.Labels[k]; !ok {
					remove = append(remove, types.Tag{Key: aws.String(k), Value: aws.String(current.Labels[k])})
				}
			}
			if len(remove) > 0 {
				if _, e := svc.DeleteTags(ctx, &ec2.DeleteTagsInput{Resources: []string{p.NativeID}, Tags: remove}); e != nil {
					return e
				}
			}
			if len(add) > 0 {
				if _, e := svc.CreateTags(ctx, &ec2.CreateTagsInput{Resources: []string{p.NativeID}, Tags: add}); e != nil {
					return e
				}
			}

			return nil
		}
	case "hetzner":
		svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
		id, _ := strconv.ParseInt(p.NativeID, 10, 64)
		server, _, e := svc.Server.GetByID(ctx, id)
		if e != nil || server == nil || server.ID != id {
			return tagReadFailure(p.Phase)
		}
		status = string(server.Status)
		current = &provider.Tags{Labels: server.Labels}
		write = func() error {
			labels := maps.Clone(p.TargetTags.Labels)
			if labels == nil {
				labels = map[string]string{}
			}
			_, _, e := svc.Server.Update(ctx, server, hcloud.ServerUpdateOpts{Labels: labels})
			return e
		}
	case "digitalocean":
		svc := godo.NewClient(&http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout})
		id, _ := strconv.Atoi(p.NativeID)
		server, _, e := svc.Droplets.Get(ctx, id)
		if e != nil || server == nil || server.ID != id {
			return tagReadFailure(p.Phase)
		}
		status = server.Status
		current = &provider.Tags{Names: server.Tags}
		write = func() error {
			resources := []godo.Resource{{ID: p.NativeID, Type: godo.DropletResourceType}}
			names := slices.Clone(p.TargetTags.Names)
			slices.Sort(names)
			for _, name := range names {
				if !slices.Contains(current.Names, name) {
					tag, _, e := svc.Tags.Create(ctx, &godo.TagCreateRequest{Name: name})
					if e != nil {
						return e
					}
					if tag == nil || !strings.EqualFold(tag.Name, name) {
						return fmt.Errorf("tag identity changed")
					}
					if _, e := svc.Tags.TagResources(ctx, name, &godo.TagResourcesRequest{Resources: resources}); e != nil {
						return e
					}
				}
			}
			names = slices.Clone(current.Names)
			slices.Sort(names)
			for _, name := range names {
				if !slices.Contains(p.TargetTags.Names, name) {
					if _, e := svc.Tags.UntagResources(ctx, name, &godo.UntagResourcesRequest{Resources: resources}); e != nil {
						return e
					}
				}
			}
			return nil
		}
	}
	if current == nil || current.Validate() != nil || status == "" {
		return tagReadFailure(p.Phase)
	}
	if p.Phase == "observe" {
		outcome := "accepted"
		if provider.TagsEqual(current, p.TargetTags) {
			outcome = "succeeded"
		}
		return &provider.PowerResult{Outcome: outcome, Status: status, Tags: current}
	}
	if !provider.TagsEqual(current, p.ExpectedTags) || PowerStateMismatch(p, status) {
		return &provider.PowerResult{Outcome: "failed", Error: "state_changed"}
	}
	if provider.TagsEqual(current, p.TargetTags) {
		return &provider.PowerResult{Outcome: "succeeded", Status: status, Tags: current}
	}
	if e := write(); e != nil {
		return &provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
	}
	return &provider.PowerResult{Outcome: "accepted", Status: status}
}
func tagReadFailure(phase string) *provider.PowerResult { r := PowerReadFailure(phase); return &r }

func ec2Labels(tags []types.Tag) *provider.Tags {
	labels := map[string]string{}
	for _, tag := range tags {
		labels[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return &provider.Tags{Labels: labels}
}
