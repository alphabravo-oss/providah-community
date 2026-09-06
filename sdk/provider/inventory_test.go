package provider

import "testing"

func TestInventoryScopes(t *testing.T) {
	r := Response{Version: Protocol, Complete: true, InventoryKinds: []string{"compute.server", "storage.volume"}, Resources: []Resource{{NativeID: "123", Region: "fsn1"}, {Kind: "storage.volume", NativeID: "123", Region: "fsn1"}}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.InventoryKinds = nil
	if r.Validate() == nil {
		t.Fatal("legacy scope accepted volume")
	}
	r.Resources = r.Resources[:1]
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.InventoryKinds = []string{"storage.volume", "storage.volume"}
	if r.Validate() == nil {
		t.Fatal("duplicate scope accepted")
	}
}
