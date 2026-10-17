package pages

// The console networks OpenPak replaces, and how far each one actually is.
//
// This is a declaration, not a probe, because most of it cannot be probed: a
// network nobody has written yet has nothing to answer with. Only the ones with
// a namespace carry live numbers; the rest are amber or grey on the page, and
// a status page that showed planned work as green would be worthless.
type network struct {
	Name string
	// Namespaces are the adapter namespaces the core knows this network by,
	// summed for the count. None means nothing is deployed, so nothing to count.
	Namespaces []string
	State      string // live | beta | building | planned
}

// networks reads top to bottom on the status page, most finished first.
var networks = []network{
	{Name: "Switch", Namespaces: []string{"switch"}, State: "live"},
	{Name: "Wii U / 3DS", Namespaces: []string{"wiiu", "3ds"}, State: "live"},
	{Name: "Wii / DS", Namespaces: []string{"wfc"}, State: "live"},
	{Name: "Xbox", Namespaces: []string{"xbox"}, State: "live"},
	{Name: "Xbox 360", Namespaces: []string{"x360"}, State: "live"},
	{Name: "PlayStation 3", Namespaces: []string{"np"}, State: "live"},
	{Name: "PSP", State: "planned"},
}
