package pages

// The console networks OpenPak replaces, and how far each one actually is.
//
// This is a declaration, not a probe, because most of it cannot be probed: a
// network nobody has written yet has nothing to answer with. Only the ones with
// a namespace carry live numbers, and the rest say plainly where they stand.
// A status page that showed "planned" work as green would be worthless.
type network struct {
	Name string
	// Namespace is the adapter namespace the core knows this network by.
	// Empty means nothing is deployed, so there is nothing to count.
	Namespace string
	State     string // live | building | planned
	Note      string
}

// networks reads top to bottom on the status page, most finished first.
var networks = []network{
	{
		Name: "Switch", Namespace: "switch", State: "live",
		Note: "Accounts, friends, invitations and play. Consoles link and play against it today.",
	},
	{
		Name: "Wii U", State: "building",
		Note: "The account and friends servers are being written; nothing is deployed yet.",
	},
	{
		Name: "3DS", State: "building",
		Note: "Shares the Wii U work: the same account and friends services serve both.",
	},
	{Name: "Wii", State: "planned"},
	{Name: "Xbox 360", State: "planned"},
	{Name: "PlayStation 3", State: "planned"},
	{Name: "PSP", State: "planned"},
}
