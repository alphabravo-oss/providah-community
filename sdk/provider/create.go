package provider

import (
	"errors"
	"regexp"
)

// ServerCreate contains only reviewed, non-secret inputs. Boot scripts and password delivery are not part of this contract.
type ServerCreate struct {
	Network       string `json:"network,omitempty"`
	Name          string `json:"name"`
	Image         string `json:"image"`
	Size          string `json:"size"`
	SSHKey        string `json:"ssh_key"`
	Subnet        string `json:"subnet,omitempty"`
	SecurityGroup string `json:"security_group,omitempty"`
}

func (c ServerCreate) Validate(cloud string) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`).MatchString(c.Name) {
		return errors.New("use a server name of 2–63 lowercase letters, digits, and hyphens")
	}
	if cloud == "aws" {
		if c.Network != "" {
			return errors.New("AWS uses subnet and security group selection")
		}
		if !regexp.MustCompile(`^ami-[a-f0-9]{8,17}$`).MatchString(c.Image) || !regexp.MustCompile(`^[a-z0-9-]+\.[a-z0-9]+$`).MatchString(c.Size) || len(c.Size) > 64 || !regexp.MustCompile(`^key-[a-f0-9]{8,17}$`).MatchString(c.SSHKey) || !regexp.MustCompile(`^subnet-[a-f0-9]{8,17}$`).MatchString(c.Subnet) || !regexp.MustCompile(`^sg-[a-f0-9]{8,17}$`).MatchString(c.SecurityGroup) {
			return errors.New("choose an AMI, instance type, key pair ID, subnet, and security group")
		}
	} else {
		id := regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
		if !id.MatchString(c.Image) || !id.MatchString(c.SSHKey) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`).MatchString(c.Size) || c.Subnet != "" || c.SecurityGroup != "" {
			return errors.New("choose an image ID, SSH key ID, and server size")
		}
	}
	if c.Network != "" {
		valid := cloud == "hetzner" && regexp.MustCompile(`^[1-9][0-9]{0,17}$`).MatchString(c.Network) || cloud == "digitalocean" && regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`).MatchString(c.Network)
		if !valid {
			return errors.New("choose a valid private network ID")
		}
	}
	return nil
}
