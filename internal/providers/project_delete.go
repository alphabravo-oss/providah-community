package providers

import (
	"context"
	"errors"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/digitalocean/godo"
	"golang.org/x/oauth2"
	"net/http"
)

func deleteCloudProject(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	fail := func() *provider.PowerResult { v := PowerReadFailure(p.Phase); return &v }
	svc := godo.NewClient(&http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout})
	v, response, err := svc.Projects.Get(ctx, p.NativeID)
	if response != nil && response.StatusCode == 404 && p.Phase == "observe" {
		return &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
	}
	if err != nil || v == nil || v.ID != p.NativeID {
		return fail()
	}
	if p.Phase == "observe" {
		return &provider.PowerResult{Outcome: "accepted", Status: "present"}
	}
	if v.IsDefault || v.Name == "" || v.CreatedAt == "" || v.UpdatedAt == "" {
		return fail()
	}
	defaultProject, _, err := svc.Projects.GetDefault(ctx)
	if err != nil || defaultProject == nil || defaultProject.ID == "" || defaultProject.ID == v.ID {
		return fail()
	}
	resources, response, err := svc.Projects.ListResources(ctx, v.ID, &godo.ListOptions{Page: 1, PerPage: 200})
	if err != nil || response == nil || len(resources) > 0 || (response.Links != nil && !response.Links.IsLastPage()) || (response.Meta != nil && response.Meta.Total != 0) {
		return fail()
	}
	lines := []string{
		fmt.Sprintf("Delete empty DigitalOcean project %s (%s): environment %s; created %s; updated %s.", v.ID, v.Name, v.Environment, v.CreatedAt, v.UpdatedAt),
		"Only the empty project is removed. No cloud resource is deleted, moved or reassigned. The default project cannot be removed.",
		"External automation referencing this project ID will need updating. No automatic undo is provided.",
	}
	return StorageDeletionResult(p, "present", lines, func() error {
		response, err := svc.Projects.Delete(ctx, v.ID)
		if err != nil {
			return err
		}
		if response == nil || response.StatusCode != http.StatusNoContent {
			return errors.New("missing project deletion acknowledgement")
		}
		return nil
	})
}
