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
	State     string // live | building | planned
}

// networks reads top to bottom on the status page, most finished first.
var networks = []network{
	{Name: "Switch", Namespace: "switch", State: "live"},
	{Name: "Wii U", State: "building"},
	{Name: "3DS", State: "building"},
	{Name: "Wii", State: "planned"},
	{Name: "Xbox 360", State: "planned"},
	{Name: "PlayStation 3", State: "planned"},
	{Name: "PSP", State: "planned"},
}
