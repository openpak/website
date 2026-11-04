package main

// rankingServers maps a catalogue title (name and console exactly as the catalogue has them) to its
// game server's internal rankings endpoint (docs/rankings-contract.md). The NEX titles are container
// names on openpak.network; the switch-nex titles run on the host network, so its endpoint is
// reached through host.containers.internal and fenced off from the public side by a firewall rule. None of them has a Traefik route or a published port. A server
// that does not answer, or has no scored board yet, gives its title no Rankings tab and no hub entry.
//
// The leaderboards core keeps boards for many games in one service and serve each
// title at /t/<title>/..., so a title with a native id in the catalogue (title_ids) maps to its own URL
// and a title with no boards yet simply has no tab. The Xbox 360 rows match by id as well as by name.
// PS3 rows match by NPWR communication id the same way, through /n/np/t/<NPWR>.
var rankingServers = []struct {
	Names    []string
	Console  string
	URL      string
	TitleIDs []string
}{
	{[]string{"Mario Kart Wii"}, "Wii", "http://openpak-leaderboards:20210/t/mario-kart-wii", nil},
	{[]string{"Mario Kart 8 Deluxe"}, "Switch", "http://openpak-leaderboards:20210/t/mario-kart-8-deluxe", nil}, // core leaderboards since switch-nex v0.12.0 (card #63)
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
	// switch-nex v0.13.0: every Ranking/Ranking2 title files its scores in the core leaderboards (card #63).
	{[]string{"Mario Golf: Super Rush"}, "Switch", "http://openpak-leaderboards:20210/t/mario-golf-super-rush", nil},
	{[]string{"Super Mario Bros. 35"}, "Switch", "http://openpak-leaderboards:20210/t/super-mario-bros-35", nil},
	{[]string{"Mario Tennis Aces"}, "Switch", "http://openpak-leaderboards:20210/t/mario-tennis-aces", nil},
	{[]string{"Splatoon 2"}, "Switch", "http://openpak-leaderboards:20210/t/splatoon-2", nil},
	{[]string{"Kirby's Dream Buffet"}, "Switch", "http://openpak-leaderboards:20210/t/kirbys-dream-buffet", nil},
	{[]string{"Super Smash Bros. Ultimate"}, "Switch", "http://openpak-leaderboards:20210/t/super-smash-bros-ultimate", nil},
	{[]string{"Clubhouse Games: 51 Worldwide Classics"}, "Switch", "http://openpak-leaderboards:20210/t/clubhouse-games-51", nil},
	{[]string{"Risk of Rain"}, "Switch", "http://openpak-leaderboards:20210/t/risk-of-rain", nil},
	{[]string{"Risk of Rain Returns"}, "Switch", "http://openpak-leaderboards:20210/t/risk-of-rain-returns", nil},
	{[]string{"Risk of Rain 2"}, "Switch", "http://openpak-leaderboards:20210/t/risk-of-rain-2", nil},
	{[]string{"Mario Strikers: Battle League"}, "Switch", "http://openpak-leaderboards:20210/t/mario-strikers-battle-league", nil},
	{[]string{"Pac-Man 99"}, "Switch", "http://openpak-leaderboards:20210/t/pac-man-99", nil},
	{[]string{"Teenage Mutant Ninja Turtles: Shredder's Revenge"}, "Switch", "http://openpak-leaderboards:20210/t/tmnt-shredders-revenge", nil},
	// The other switch-nex Ranking/Ranking2 titles on the core leaderboards (switch-nex v0.13.0, card #63); a title shows once it has scores.
	{[]string{"Castle Crashers Remastered"}, "Switch", "http://openpak-leaderboards:20210/t/castle-crashers-remastered", nil},
	{[]string{"Monopoly for Nintendo Switch"}, "Switch", "http://openpak-leaderboards:20210/t/monopoly", nil},
	{[]string{"Mario Party Superstars"}, "Switch", "http://openpak-leaderboards:20210/t/mario-party-superstars", nil},
	{[]string{"ARMS"}, "Switch", "http://openpak-leaderboards:20210/t/arms", nil},
	{[]string{"Animal Crossing: New Horizons"}, "Switch", "http://openpak-leaderboards:20210/t/animal-crossing-new-horizons", nil},
	{[]string{"Luigi's Mansion 3"}, "Switch", "http://openpak-leaderboards:20210/t/luigis-mansion-3", nil},
	{[]string{"Minecraft"}, "Switch", "http://openpak-leaderboards:20210/t/minecraft", nil},
	{[]string{"Super Mario Maker 2"}, "Switch", "http://openpak-leaderboards:20210/t/super-mario-maker-2", nil},
	{[]string{"Super Mario Odyssey"}, "Switch", "http://openpak-leaderboards:20210/t/super-mario-odyssey", nil},
	{[]string{"Super Mario 3D World + Bowser's Fury"}, "Switch", "http://openpak-leaderboards:20210/t/super-mario-3d-world-bowsers-fury", nil},
	{[]string{"Don't Starve Together"}, "Switch", "http://openpak-leaderboards:20210/t/dont-starve-together", nil},
	// spelunky2 v0.9.0 (Switch): boards stay empty until SPELUNKY2_SCORE_FIELD is set on the server.
	{[]string{"Spelunky 2"}, "Switch", "http://openpak-spelunky2:9090", nil},
	// Xbox 360: x360-xstats v0.7.0 feeds the leaderboards core, which keys an unmapped title as x360:<title id in hex>, served at /t/x360:<id>; matched by catalogue title_ids.
	{[]string{"Halo 3"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4D5307E6", []string{"4D5307E6"}},
	{[]string{"Halo: Reach"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4D53085B", []string{"4D53085B"}},
	{[]string{"Halo 4"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4D530919", []string{"4D530919"}},
	{[]string{"Call of Duty 4: Modern Warfare"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:415607E6", []string{"415607E6"}},
	// The other 26 Xbox 360 rows (catalogue migration 0025); boards appear once a title has scores.
	{[]string{"Gears of War 2"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4D53082D", []string{"4D53082D"}},
	{[]string{"Gears of War 3"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4D5308AB", []string{"4D5308AB"}},
	{[]string{"Call of Duty: Modern Warfare 2"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:41560817", []string{"41560817"}},
	{[]string{"Call of Duty: Black Ops"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:41560855", []string{"41560855"}},
	{[]string{"Call of Duty: World at War"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4156081C", []string{"4156081C"}},
	{[]string{"Minecraft: Xbox 360 Edition"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:584111F7", []string{"584111F7"}},
	{[]string{"Left 4 Dead 2"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:454108D4", []string{"454108D4"}},
	{[]string{"Borderlands 2"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:5454087C", []string{"5454087C"}},
	{[]string{"Castle Crashers"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:584108B7", []string{"584108B7"}},
	{[]string{"Forza Motorsport 4"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4D530910", []string{"4D530910"}},
	{[]string{"Blur"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:41560845", []string{"41560845"}},
	{[]string{"Trials Evolution"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:58410AAD", []string{"58410AAD"}},
	{[]string{"Red Dead Redemption"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:5454082B", []string{"5454082B"}},
	{[]string{"Grand Theft Auto IV"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:545407F2", []string{"545407F2"}},
	{[]string{"Saints Row: The Third"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:5451086D", []string{"5451086D"}},
	{[]string{"Dead Island"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4B4D07DF", []string{"4B4D07DF"}},
	{[]string{"Resident Evil 5"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:434307D4", []string{"434307D4"}},
	{[]string{"Dark Souls"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4E4D083A", []string{"4E4D083A"}},
	{[]string{"Portal 2"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:45410912", []string{"45410912"}},
	{[]string{"Chromehounds"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:534507D4", []string{"534507D4"}},
	{[]string{"Armored Core 4"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:465307D6", []string{"465307D6"}},
	{[]string{"Lost Planet 2"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:434307ED", []string{"434307ED"}},
	{[]string{"Rock Band 3"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:45410914", []string{"45410914"}},
	{[]string{"Halo Wars"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:4D530808", []string{"4D530808"}},
	{[]string{"Titanfall"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:454109D0", []string{"454109D0"}},
	{[]string{"Battlefield 3"}, "Xbox 360", "http://openpak-leaderboards:20210/t/x360:45410950", []string{"45410950"}},
	// PS3: np-score feeds the leaderboards core, which keys boards by NP communication id; /n/np/t/<NPWR> resolves it whether or not it is mapped to a neutral key.
	{[]string{"Demon's Souls"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00881_00", []string{"NPWR00881_00"}},
	{[]string{"LittleBigPlanet"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00160_00", []string{"NPWR00160_00"}},
	{[]string{"LittleBigPlanet 2"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR01210_00", []string{"NPWR01210_00"}},
	{[]string{"LittleBigPlanet 3"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR03509_00", []string{"NPWR03509_00"}},
	{[]string{"Soulcalibur V"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR01738_00", []string{"NPWR01738_00"}},
	{[]string{"Metal Gear Solid: Peace Walker HD"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR01848_00", []string{"NPWR01848_00"}},
	{[]string{"Blur"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00567_00", []string{"NPWR00567_00"}},
	{[]string{"Tekken 6"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00482_00", []string{"NPWR00482_00"}},
	{[]string{"Tekken Tag Tournament 2"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR02973_00", []string{"NPWR02973_00"}},
	{[]string{"Ultimate Marvel vs. Capcom 3"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR01796_00", []string{"NPWR01796_00"}},
	{[]string{"Persona 4 Arena Ultimax"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR06200_00", []string{"NPWR06200_00"}},
	{[]string{"Guilty Gear Xrd -SIGN-"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR07017_00", []string{"NPWR07017_00"}},
	{[]string{"Gran Turismo 6"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR04712_00", []string{"NPWR04712_00"}},
	{[]string{"Uncharted 2: Among Thieves"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00462_00", []string{"NPWR00462_00"}},
	{[]string{"Uncharted 3: Drake's Deception"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR01216_00", []string{"NPWR01216_00"}},
	{[]string{"Killzone 2"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00153_00", []string{"NPWR00153_00"}},
	{[]string{"Killzone 3"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR01569_00", []string{"NPWR01569_00"}},
	{[]string{"Resistance 2"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00291_00", []string{"NPWR00291_00"}},
	{[]string{"Resistance 3"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR01841_00", []string{"NPWR01841_00"}},
	{[]string{"Warhawk"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00163_00", []string{"NPWR00163_00"}},
	{[]string{"Starhawk"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR02261_00", []string{"NPWR02261_00"}},
	{[]string{"MotorStorm"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00012_00", []string{"NPWR00012_00"}},
	{[]string{"Journey"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00714_00", []string{"NPWR00714_00"}},
	{[]string{"Minecraft: PlayStation 3 Edition"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR05636_00", []string{"NPWR05636_00"}},
	{[]string{"Red Dead Redemption"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR00867_00", []string{"NPWR00867_00"}},
	{[]string{"Call of Duty: Black Ops II"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR02059_00", []string{"NPWR02059_00"}},
	{[]string{"Battlefield 3"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR02011_00", []string{"NPWR02011_00"}},
	{[]string{"PlayStation All-Stars Battle Royale"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR02621_00", []string{"NPWR02621_00"}},
	{[]string{"Armored Core V"}, "PS3", "http://openpak-leaderboards:20210/n/np/t/NPWR03050_00", []string{"NPWR03050_00"}},
}

// rankingRegions lists, for a title whose game keeps one set of boards per region, each regional
// server. A PS3 game has one NP communication id per region, so US, EU and JP scores are separate
// boards in the leaderboards core. The Rankings tab lists them side by side as "<region>: <board>"
// (companion.Multi) rather than merged, because ranks are counted per server. Key is
// "<console>|<first name>" of the rankingServers line; the first region is that line's URL.
var rankingRegions = map[string][]struct{ Label, URL string }{
	"PS3|Demon's Souls": {
		{"US", "http://openpak-leaderboards:20210/n/np/t/NPWR00881_00"},
		{"EU", "http://openpak-leaderboards:20210/n/np/t/NPWR01249_00"},
		{"JP/Asia", "http://openpak-leaderboards:20210/n/np/t/NPWR00245_00"},
	},
	"PS3|Armored Core V": {
		{"US", "http://openpak-leaderboards:20210/n/np/t/NPWR03050_00"},
		{"EU", "http://openpak-leaderboards:20210/n/np/t/NPWR03067_00"},
		{"JP", "http://openpak-leaderboards:20210/n/np/t/NPWR02037_00"},
	},
}
