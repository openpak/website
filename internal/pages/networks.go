package pages

// The console networks OpenPak replaces, and how far each one actually is.
//
// This is a declaration, not a probe, because most of it cannot be probed: a
// network nobody has written yet has nothing to answer with. Only the ones with
// a namespace carry live numbers; the rest are amber or grey on the page, and
// a status page that showed planned work as green would be worthless.
type network struct {
	Name string
	// Namespace is the adapter namespace the core knows this network by.
	// Empty means nothing is deployed, so there is nothing to count.
	Namespace string
	State     string // live | beta | building | planned
}

// networks reads top to bottom on the status page, most finished first.
var networks = []network{
	{Name: "Switch", Namespace: "switch", State: "live"},
	{Name: "Wii U", Namespace: "wiiu", State: "live"},
	{Name: "3DS", Namespace: "3ds", State: "live"},
	// ponytail: Wii and DS share the wfc namespace, so both rows show the combined count.
	{Name: "Wii", Namespace: "wfc", State: "live"},
	{Name: "DS", Namespace: "wfc", State: "live"},
	{Name: "Xbox", Namespace: "xbox", State: "live"},
	{Name: "Xbox 360", Namespace: "x360", State: "live"},
	{Name: "PlayStation 3", Namespace: "np", State: "live"},
	{Name: "PSP", State: "planned"},
}
