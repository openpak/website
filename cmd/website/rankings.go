package main

// rankingServers maps a catalogue title (name and console exactly as the catalogue has them) to its
// game server's internal rankings endpoint (docs/rankings-contract.md). The NEX titles are container
// names on openpak.network; Mario Kart 8 Deluxe runs on the host network, so its endpoint is
// reached through host.containers.internal and fenced off from the public side by a firewall rule. None of them has a Traefik route or a published port. A server
// that does not answer, or has no scored board yet, gives its title no Rankings tab and no hub entry.
//
// Servers that implement the contract but are not listed: x360-xstats and np-score keep boards for
// many games in one service, so a title needs its game id (not stored in this catalogue) to pick
// its boards; they join here once a title carries one.
var rankingServers = []struct {
	Names   []string
	Console string
	URL     string
}{
	{[]string{"Mario Kart Wii"}, "Wii", "http://openpak-wfc-race:9090"},
	{[]string{"Mario Kart 8 Deluxe"}, "Switch", "http://host.containers.internal:22855"},
	{[]string{"Mario Kart 8"}, "Wii U", "http://openpak-mario-kart-8:9090"},
	{[]string{"Mario Kart 7"}, "3DS", "http://openpak-mario-kart-7:9090"},
	{[]string{"Pikmin 3"}, "Wii U", "http://openpak-pikmin-3:9090"},
	{[]string{"Splatoon"}, "Wii U", "http://openpak-splatoon:9090"},
	{[]string{"Puyo Puyo Tetris"}, "Wii U", "http://openpak-puyo-puyo-tetris:9090"},
	{[]string{"Sonic & All-Stars Racing Transformed"}, "Wii U", "http://openpak-sonic-all-stars-racing-transformed-wiiu:9090"},
	{[]string{"Terraria"}, "Wii U", "http://openpak-terraria-wiiu:9090"},
	{[]string{"Dr. Luigi"}, "Wii U", "http://openpak-dr-luigi:9090"},
	{[]string{"Dr. Mario: Miracle Cure"}, "3DS", "http://openpak-miracle-cure:9090"},
}
