package providers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// ponytail: provider APIs offer no atomic compare-and-delete; dependency changes after the final read remain a provider-side race.
func deletionReview(p *provider.PowerRequest, lines []string, status string) *provider.PowerResult {
	slices.Sort(lines)
	impact := strings.Join(lines, "\n")
	if len(impact) > 16384 {
		return &provider.PowerResult{Outcome: "failed", Error: "preflight_failed"}
	}
	if p.Phase == "preview" {
		return &provider.PowerResult{Outcome: "preview", Status: status, DeletionImpact: impact}
	}
	if impact != p.DeletionImpact {
		return &provider.PowerResult{Outcome: "failed", Status: status, Error: "state_changed"}
	}
	return nil
}
func awsDeletionImpact(v types.Instance) ([]string, error) {
	lines := []string{fmt.Sprintf("Delete AWS server %s (%s).", aws.ToString(v.InstanceId), aws.ToString(v.PrivateDnsName)), "All instance-store data is lost. No automatic backup or undo is provided.", "External controllers may recreate this server. Review application dependencies before approving."}
	for _, b := range v.BlockDeviceMappings {
		if b.Ebs == nil || b.Ebs.VolumeId == nil || b.Ebs.DeleteOnTermination == nil {
			return nil, errors.New("incomplete volume impact")
		}
		verb := "Retain"
		if aws.ToBool(b.Ebs.DeleteOnTermination) {
			verb = "Delete"
		}
		lines = append(lines, fmt.Sprintf("%s EBS volume %s (%s).", verb, aws.ToString(b.Ebs.VolumeId), aws.ToString(b.DeviceName)))
	}
	for _, n := range v.NetworkInterfaces {
		if n.Attachment == nil || n.Attachment.DeleteOnTermination == nil || n.NetworkInterfaceId == nil {
			return nil, errors.New("incomplete interface impact")
		}
		verb := "Retain"
		if aws.ToBool(n.Attachment.DeleteOnTermination) {
			verb = "Delete"
		}
		lines = append(lines, fmt.Sprintf("%s network interface %s; connectivity is lost. Elastic IP allocations are retained.", verb, aws.ToString(n.NetworkInterfaceId)))
	}
	return lines, nil
}
func doDeletionImpact(v *godo.Droplet) []string {
	lines := []string{fmt.Sprintf("Delete DigitalOcean server %d (%s) and its local disk data.", v.ID, v.Name), "Automatic backups are deleted with the server. No automatic backup or undo is provided.", "Public server addresses are released. Associated-resource cascading deletion is not requested.", "External controllers may recreate this server. Review application dependencies before approving."}
	for _, id := range v.BackupIDs {
		lines = append(lines, fmt.Sprintf("Delete automatic backup %d.", id))
	}
	for _, id := range v.VolumeIDs {
		lines = append(lines, "Retain volume "+id+".")
	}
	for _, id := range v.SnapshotIDs {
		lines = append(lines, fmt.Sprintf("Retain snapshot %d.", id))
	}
	return lines
}
func hetznerDeletionImpact(ctx context.Context, svc *hcloud.Client, v *hcloud.Server) ([]string, error) {
	if (v.PublicNet.IPv4.IP != nil && v.PublicNet.IPv4.ID == 0) || ((v.PublicNet.IPv6.IP != nil || v.PublicNet.IPv6.Network != nil) && v.PublicNet.IPv6.ID == 0) {
		return nil, errors.New("missing primary IP identity")
	}
	if v.Protection.Delete {
		return nil, errors.New("server deletion protection enabled")
	}
	lines := []string{fmt.Sprintf("Delete Hetzner server %d (%s) and its local disk data.", v.ID, v.Name), "Automatic backups are deleted with the server; standalone snapshots are retained. No automatic backup or undo is provided.", "External controllers may recreate this server. Review application dependencies before approving."}
	for _, volume := range v.Volumes {
		lines = append(lines, fmt.Sprintf("Retain volume %d.", volume.ID))
	}
	for _, id := range []int64{v.PublicNet.IPv4.ID, v.PublicNet.IPv6.ID} {
		if id == 0 {
			continue
		}
		ip, _, err := svc.PrimaryIP.GetByID(ctx, id)
		if err != nil || ip == nil || ip.AssigneeID != v.ID {
			return nil, errors.New("cannot verify primary IP impact")
		}
		verb := "Retain"
		if ip.AutoDelete {
			verb = "Delete"
		}
		lines = append(lines, fmt.Sprintf("%s primary IP %d (%s).", verb, id, ip.IP))
	}
	for _, ip := range v.PublicNet.FloatingIPs {
		lines = append(lines, fmt.Sprintf("Retain floating IP %d.", ip.ID))
	}
	return lines, nil
}

func StorageDeletionResult(p *provider.PowerRequest, status string, lines []string, remove func() error) *provider.PowerResult {
	if p.Phase == "observe" {
		return &provider.PowerResult{Outcome: "accepted", Status: status}
	}
	if status != p.ExpectedStatus || !provider.ResourceActionAllowed(p.ResourceKind, "delete", status) {
		return &provider.PowerResult{Outcome: "failed", Status: status, Error: "state_changed"}
	}
	if review := deletionReview(p, lines, status); review != nil {
		return review
	}
	if err := remove(); err != nil {
		return &provider.PowerResult{Outcome: "uncertain", Error: "submission_uncertain"}
	}
	return &provider.PowerResult{Outcome: "accepted", Status: status}
}
