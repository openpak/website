package pages

import (
	"reflect"
	"testing"

	"github.com/openpak/website/internal/store"
)

func TestFeatureGroupsContainOnlyServicesForEachConsole(t *testing.T) {
	groups := featureGroups([]store.Request{
		{Name: "Account core", Console: "All networks", Category: "Core service"},
		{Name: "Xbox network", Console: "Xbox", Category: "Console network"},
		{Name: "DS network", Console: "DS"},
		{Name: "Friends", Console: "Xbox"},
		{Name: "Accounts", Console: "Xbox"},
		{Name: "SpotPass", Console: "Wii U / 3DS"},
	})
	var got []string
	for _, group := range groups {
		for _, service := range group.Features {
			if service.Name == "SpotPass" && service.Console != "Wii U / 3DS" {
				t.Fatal("shared service lost its network labels")
			}
			got = append(got, group.Console+":"+service.Name)
		}
	}
	want := []string{"Core services:Account core", "Wii U:SpotPass", "3DS:SpotPass", "Xbox:Accounts", "Xbox:Friends"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
}
