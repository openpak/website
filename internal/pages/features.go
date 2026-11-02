package pages

import (
	"sort"
	"strings"

	"github.com/openpak/website/internal/store"
)

type featureGroup struct {
	Console  string
	Features []store.Request
}

// Shared services appear under each console they serve. Network overview
// records remain in the catalogue for other consumers, outside this service list.
func featureGroups(rows []store.Request) []featureGroup {
	byConsole := make(map[string][]store.Request)
	for _, row := range rows {
		if row.Category == "Console network" || strings.HasSuffix(row.Name, " network") {
			continue
		}
		if row.Category == "Core service" {
			byConsole["Core services"] = append(byConsole["Core services"], row)
			continue
		}
		for _, console := range strings.Split(row.Console, " / ") {
			byConsole[console] = append(byConsole[console], row)
		}
	}
	var groups []featureGroup
	for console, services := range byConsole {
		sort.SliceStable(services, func(i, j int) bool {
			return strings.ToLower(services[i].Name) < strings.ToLower(services[j].Name)
		})
		groups = append(groups, featureGroup{console, services})
	}
	order := map[string]int{"Core services": -1, "Switch": 0, "Wii U": 1, "3DS": 2, "Wii": 3, "DS": 4, "Xbox": 5, "Xbox 360": 6, "PS3": 7}
	sort.Slice(groups, func(i, j int) bool {
		a, aok := order[groups[i].Console]
		b, bok := order[groups[j].Console]
		if aok != bok {
			return aok
		}
		if aok && a != b {
			return a < b
		}
		return groups[i].Console < groups[j].Console
	})
	return groups
}

// featureIcon describes what the service does, independent of its console.
func featureIcon(feature store.Request) string {
	name := strings.ToLower(feature.Name)
	for _, rule := range []struct {
		words []string
		icon  string
	}{
		{[]string{"troph", "achievement"}, "trophy"},
		{[]string{"auth", "token", "dauth", "membership"}, "lock"},
		{[]string{"security", "parental", "terms", "country"}, "shield"},
		{[]string{"friend", "invitation", "matchmaking", "federation"}, "users"},
		{[]string{"voice", "chat", "messaging", "miiverse"}, "chat"},
		{[]string{"save", "progress", "history"}, "save"},
		{[]string{"account", "profile", "avatar", "gamertag"}, "user"},
		{[]string{"link", "enrol", "nat", "relay"}, "link"},
		{[]string{"news", "monitoring", "bcat", "presence", "connection"}, "pulse"},
		{[]string{"shop", "store", "catalog", "registry", "download", "spotpass", "boss"}, "dl"},
		{[]string{"dashboard", "home"}, "home"},
		{[]string{"dns", "discovery", "configuration"}, "globe"},
		{[]string{"patch", "client"}, "code"},
	} {
		for _, word := range rule.words {
			if strings.Contains(name, word) {
				return rule.icon
			}
		}
	}
	return "grid"
}
