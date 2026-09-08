package pages

// titleNames maps the title IDs this network has actually seen to the names a
// person would recognise. It is deliberately short: a name appears here when a
// title has been played against OpenPak, not because a catalogue was imported.
// An unknown id shows as its id rather than as a guess.
//
// ponytail: a map, not a table. It moves when a title is added, which is
// rarely, and a migration to change a label would be ceremony.
var titleNames = map[string]string{
	"0100ED9024EB8000": "Outbound",
	"0100E65002BB8000": "Stardew Valley",
	"0100152000022000": "Mario Kart 8 Deluxe",
	"0100C2500FC20000": "Splatoon 3",
	"01006A800016E000": "Super Smash Bros. Ultimate",
	"0100000000010000": "Super Mario Odyssey",
}

// titleName is what to print for a title id.
func titleName(id string) string {
	if n, ok := titleNames[id]; ok {
		return n
	}
	return id
}
