package provider

import (
	"strings"
	"testing"
)

func TestCreationContract(t *testing.T) {
	c := ServerCreate{Name: "my-server", Image: "11", Size: "cx23", SSHKey: "12"}
	p := PowerRequest{OperationID: strings.Repeat("a", 64), Phase: "submit", Action: "create", Create: &c}
	if p.Validate("hetzner") != nil {
		t.Fatal("valid creation rejected")
	}
	for _, field := range []*string{&c.Name, &c.Image, &c.Size, &c.SSHKey} {
		old := *field
		*field = ""
		if p.Validate("hetzner") == nil {
			t.Fatal("missing input accepted")
		}
		*field = old
	}
	p.NativeID = "123"
	if p.Validate("hetzner") == nil {
		t.Fatal("create submit accepted an existing target")
	}
	p.Phase = "observe"
	if p.Validate("hetzner") != nil {
		t.Fatal("bound creation observation rejected")
	}
	p.Action = "start"
	if p.Validate("hetzner") == nil {
		t.Fatal("creation input accepted for another action")
	}
	c.Subnet = "subnet-12345678"
	if c.Validate("hetzner") == nil {
		t.Fatal("foreign provider option accepted")
	}
	c = ServerCreate{Name: "my-server", Image: "ami-12345678", Size: "t3.micro", SSHKey: "key-12345678", Subnet: "subnet-12345678", SecurityGroup: "sg-12345678"} // gitleaks:allow -- synthetic cloud identifier/credential fixture
	if c.Validate("aws") != nil {
		t.Fatal("valid AWS input rejected")
	}
	c.SecurityGroup = ""
	if c.Validate("aws") == nil {
		t.Fatal("implicit AWS security group allowed")
	}
}

func TestPrivateNetworkSelection(t *testing.T) {
	c := ServerCreate{Name: "my-server", Image: "11", Size: "cx23", SSHKey: "12", Network: "44"}
	if c.Validate("hetzner") != nil || c.Validate("digitalocean") == nil {
		t.Fatal("network ID scope")
	}
	c.Network = "11111111-2222-3333-4444-555555555555"
	if c.Validate("digitalocean") != nil || c.Validate("hetzner") == nil {
		t.Fatal("VPC ID scope")
	}
	c.Network = "../network"
	if c.Validate("hetzner") == nil || c.Validate("digitalocean") == nil {
		t.Fatal("invalid network accepted")
	}
}
