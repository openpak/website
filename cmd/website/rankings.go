package main

// rankingServers maps a catalogue title (name and console exactly as the catalogue has them) to its
// game server's internal rankings endpoint (docs/rankings-contract.md). The NEX titles are container
// names on openpak.network; Mario Kart 8 Deluxe runs on the host network, so its endpoint is
// reached through host.containers.internal and fenced off from the public side by a firewall rule. None of them has a Traefik route or a published port. A server
// that does not answer, or has no scored board yet, gives its title no Rankings tab and no hub entry.
//
// x360-xstats and the leaderboards core keep boards for many games in one service and serve each
// title at /t/<title>/..., so a title with a native id in the catalogue (title_ids) maps to its own URL
// and a title with no boards yet simply has no tab. The Xbox 360 rows match by id as well as by name.
// Still unmapped: the other Xbox 360 titles and every PS3 title, until the catalogue carries their ids
// (docs/rankings-contract.md).
var rankingServers = []struct {
	Names    []string
	Console  string
	URL      string
	TitleIDs []string
}{
	{[]string{"Mario Kart Wii"}, "Wii", "http://openpak-wfc-race:9090", nil},
	{[]string{"Mario Kart 8 Deluxe"}, "Switch", "http://host.containers.internal:22855", nil},
	{[]string{"Mario Kart 8"}, "Wii U", "http://openpak-mario-kart-8:9090", nil},
	{[]string{"Mario Kart 7"}, "3DS", "http://openpak-mario-kart-7:9090", nil},
	{[]string{"Pikmin 3"}, "Wii U", "http://openpak-pikmin-3:9090", nil},
	{[]string{"Splatoon"}, "Wii U", "http://openpak-splatoon:9090", nil},
	{[]string{"Puyo Puyo Tetris"}, "Wii U", "http://openpak-puyo-puyo-tetris:9090", nil},
	{[]string{"Sonic & All-Stars Racing Transformed"}, "Wii U", "http://openpak-sonic-all-stars-racing-transformed-wiiu:9090", nil},
	{[]string{"Terraria"}, "Wii U", "http://openpak-terraria-wiiu:9090", nil},
	{[]string{"Dr. Luigi"}, "Wii U", "http://openpak-dr-luigi:9090", nil},
	{[]string{"Dr. Mario: Miracle Cure"}, "3DS", "http://openpak-miracle-cure:9090", nil},
	// nex v1.5.0 (Wii U and 3DS), one container per title.
	{[]string{"Puyo Puyo!! 20th Anniversary"}, "3DS", "http://openpak-puyo-puyo-20th:9090", nil},
	{[]string{"Pokken Tournament"}, "Wii U", "http://openpak-pokken-tournament:9090", nil},
	{[]string{"Super Smash Bros. for Nintendo 3DS"}, "3DS", "http://openpak-super-smash-bros-3ds:9090", nil},
	{[]string{"Super Smash Bros. for Wii U"}, "Wii U", "http://openpak-super-smash-bros-wiiu:9090", nil},
	{[]string{"Yo-kai Watch 2"}, "3DS", "http://openpak-yo-kai-watch-2:9090", nil},
	{[]string{"Monster Hunter 4 Ultimate"}, "3DS", "http://openpak-monster-hunter-4-ultimate:9090", nil},
	{[]string{"Monster Hunter 4"}, "3DS", "http://openpak-monster-hunter-4:9090", nil},
	{[]string{"Monster Hunter Stories"}, "3DS", "http://openpak-monster-hunter-stories:9090", nil},
	{[]string{"Monster Hunter XX"}, "3DS", "http://openpak-monster-hunter-xx:9090", nil},
	{[]string{"Ice Station Z"}, "3DS", "http://openpak-ice-station-z:9090", nil},
	{[]string{"Resident Evil: The Mercenaries 3D"}, "3DS", "http://openpak-resident-evil-the-mercenaries-3d:9090", nil},
	{[]string{"Super Mario Maker"}, "Wii U", "http://openpak-super-mario-maker:9090", nil},
	// switch-nex v0.10.0: host network, one port per title (RANKINGS_ADDR=auto), fenced like the MK8D one.
	{[]string{"Mario Golf: Super Rush"}, "Switch", "http://host.containers.internal:22135", nil},
	{[]string{"Super Mario Bros. 35"}, "Switch", "http://host.containers.internal:22165", nil},
	{[]string{"Mario Tennis Aces"}, "Switch", "http://host.containers.internal:22175", nil},
	{[]string{"Splatoon 2"}, "Switch", "http://host.containers.internal:22325", nil},
	{[]string{"Kirby's Dream Buffet"}, "Switch", "http://host.containers.internal:22385", nil},
	{[]string{"Super Smash Bros. Ultimate"}, "Switch", "http://host.containers.internal:22355", nil},
	{[]string{"Clubhouse Games: 51 Worldwide Classics"}, "Switch", "http://host.containers.internal:22875", nil},
	{[]string{"Risk of Rain"}, "Switch", "http://host.containers.internal:22895", nil},
	{[]string{"Risk of Rain Returns"}, "Switch", "http://host.containers.internal:23005", nil},
	{[]string{"Risk of Rain 2"}, "Switch", "http://host.containers.internal:23045", nil},
	{[]string{"Mario Strikers: Battle League"}, "Switch", "http://host.containers.internal:22305", nil},
	{[]string{"Pac-Man 99"}, "Switch", "http://host.containers.internal:23145", nil},
	{[]string{"Teenage Mutant Ninja Turtles: Shredder's Revenge"}, "Switch", "http://host.containers.internal:23135", nil},
	// spelunky2 v0.9.0 (Switch): boards stay empty until SPELUNKY2_SCORE_FIELD is set on the server.
	{[]string{"Spelunky 2"}, "Switch", "http://openpak-spelunky2:9090", nil},
	// x360-xstats v0.5.0: one URL per Xbox 360 title id (hex), matched by catalogue title_ids.
	{[]string{"Halo 3"}, "Xbox 360", "http://openpak-x360-xstats:9090/t/4D5307E6", []string{"4D5307E6"}},
	{[]string{"Halo: Reach"}, "Xbox 360", "http://openpak-x360-xstats:9090/t/4D53085B", []string{"4D53085B"}},
	{[]string{"Halo 4"}, "Xbox 360", "http://openpak-x360-xstats:9090/t/4D530919", []string{"4D530919"}},
	{[]string{"Call of Duty 4: Modern Warfare"}, "Xbox 360", "http://openpak-x360-xstats:9090/t/415607E6", []string{"415607E6"}},
}
