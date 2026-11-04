package netorder

import (
	"os"
	"testing"
)

func TestOrderIsFamilyThenGeneration(t *testing.T) {
	var ids []string
	for _, n := range All {
		ids = append(ids, n.ID)
	}
	want := []string{"wii-ds", "wiiu-3ds", "switch", "ps3", "psp", "vita", "ps4", "ps5", "xbox", "xbox360", "xbox-one", "shared", "websites"}
	if len(ids) != len(want) {
		t.Fatalf("%v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order %v, want %v", ids, want)
		}
	}
	if !(ConsoleRank("wii") < ConsoleRank("ds") && ConsoleRank("ds") < ConsoleRank("wiiu") && ConsoleRank("3ds") < ConsoleRank("switch") &&
		ConsoleRank("ps3") < ConsoleRank("ps4") && ConsoleRank("switch") < ConsoleRank("ps3") && ConsoleRank("xbox") < ConsoleRank("xbox360")) {
		t.Error("console ranks out of order")
	}
}

func TestCopyMatchesTheStatusRepo(t *testing.T) {
	src, err := os.ReadFile("../../../status/shared/networks.json")
	if err != nil {
		t.Skip("status repository not checked out beside this one")
	}
	if string(src) != string(raw) {
		t.Error("internal/netorder/networks.json differs from status/shared/networks.json; run scripts/sync-status-shared.sh")
	}
}
