package store

import (
	"fmt"
	"strings"
)

// catalogTitleIDs is every title id of the catalogue's Wii U and 3DS games, one per region
// (and for a pair like Pokémon X / Y, both games), so a friend's presence names the game
// whichever copy they run. The primary (title_id) is the first: USA, then EUR, JPN, the rest.
//
// Sources, each id matched on product code and name, never guessed:
//   - Wii U: the WiiUBrew title database (wiiubrew.org/wiki/Title_database, 00050000 and
//     00050010 sections), cross-checked where it has them against Cemu's game profiles and
//     the ids in servers/nex (Mario Kart 8, Dr. Luigi, 3D World, Tipping Stars).
//   - 3DS: Nintendo's eShop listings as mirrored by hax0kartik/3dsdb (base ids, plus the
//     base of every 0004000E update id), and 3dsdb.com's retail cartridge list, cross-
//     checked against Azahar's compatibility list.
//   - Switch: see the note above the Switch rows.
//
// Mario vs. Donkey Kong: Tipping Stars is cross-buy with one server, so the Wii U row holds
// the 3DS ids too. "Street Fighter IV: 3D Edition" has none: the only 3DS game of that name is
// Super Street Fighter IV: 3D Edition, which holds them.
//
// Applied once by the 0008 migration, only to rows whose title_ids are still empty, so an id
// an admin set through the API is never overwritten. Add ids later through
// PATCH /api/v1/admin/titles/{id} ({"title_ids": [...]}), not here.
var catalogTitleIDs = []struct {
	Console, Name string
	IDs           []string
}{
	{"Wii U", "Mario Kart 8", []string{
		"000500001010EC00", // USA (AMK)
		"000500001010ED00", // EUR (AMK)
		"000500001010EB00", // JPN (AMK)
	}},
	{"Wii U", "Splatoon", []string{
		"0005000010176900", // USA (AGM)
		"0005000010176A00", // EUR (AGM)
		"0005000010162B00", // JPN (AGM)
	}},
	{"Wii U", "Super Smash Bros. for Wii U", []string{
		"0005000010144F00", // USA (AXF)
		"0005000010145000", // EUR (AXF)
		"0005000010110E00", // JPN (AXF)
	}},
	{"Wii U", "Super Mario Maker", []string{
		"000500001018DC00", // USA (AMA)
		"000500001018DD00", // EUR (AMA)
		"000500001018DB00", // JPN (AMA)
	}},
	{"Wii U", "Pokken Tournament", []string{
		"00050000101DF400", // USA (APK)
		"00050000101DF500", // EUR (APK)
		"00050000101C5800", // JPN (APK)
	}},
	{"Wii U", "Mario Tennis: Ultra Smash", []string{
		"00050000101A3500", // USA (AVX)
		"00050000101A3600", // EUR (AVX)
		"0005000010199000", // JPN (AVX)
	}},
	{"Wii U", "Pikmin 3", []string{
		"000500001012BD00", // USA (AC3)
		"000500001012BE00", // EUR (AC3)
		"000500001012BC00", // JPN (AC3)
	}},
	{"Wii U", "Wii Sports Club", []string{
		"0005000010144D00", // USA (AWS)
		"0005000010144E00", // EUR (AWS)
		"000500001012F100", // JPN (AWS)
	}},
	{"Wii U", "Monster Hunter 3 Ultimate", []string{
		"0005000010118300", // USA (AHD)
		"0005000010117200", // EUR (AHD)
		"0005000010104D00", // JPN (AHD)
	}},
	{"Wii U", "Bayonetta 2", []string{
		"0005000010172600", // USA (AQU)
		"0005000010172700", // EUR (AQU)
		"000500001011B900", // JPN (AQU)
	}},
	{"Wii U", "FAST Racing NEO", []string{
		"000500001012F000", // USA (WFS)
		"00050000101D6000", // EUR (WFS)
		"00050000101FED00", // EUR (BR5)
		"00050000101E4100", // JPN (WFS)
	}},
	{"Wii U", "Devil's Third", []string{
		"0005000010177600", // USA (ADN)
		"0005000010177700", // EUR (ADN)
		"0005000010138F00", // JPN (ADN)
	}},
	{"Wii U", "Tekken Tag Tournament 2 Wii U Edition", []string{
		"0005000010110000", // USA (AKN)
		"000500001010F800", // EUR (AKN)
		"0005000010100600", // JPN (AKN)
	}},
	{"Wii U", "Injustice: Gods Among Us", []string{
		"0005000010111700", // USA (AJS)
		"0005000010111A00", // EUR (AJS)
		"0005000010140700", // JPN (AJS)
	}},
	{"Wii U", "Call of Duty: Black Ops II", []string{
		"000500001010CF00", // USA (AEC)
		"0005000010113400", // EUR (AEC)
		"000500001011B400", // JPN (AEC)
		"0005000010113700", // EUR, German (AEC)
		"0005000010113500", // EUR, Spanish/Italian (AEC)
	}},
	{"Wii U", "Call of Duty: Ghosts", []string{
		"0005000010146800", // USA (ACP)
		"0005000010156100", // EUR (ACP)
		"0005000010157900", // JPN (ACP)
		"0005000010157600", // EUR, Spanish/Italian (ACP)
	}},
	{"Wii U", "Assassin's Creed III", []string{
		"0005000010106B00", // USA (ASS)
		"000500001010F600", // EUR (ASS)
		"0005000010112400", // JPN (ASS)
	}},
	{"Wii U", "Sonic & All-Stars Racing Transformed", []string{
		"000500001010B300", // USA (AS2)
		"0005000010111F00", // EUR (AS2)
		"000500001015B400", // JPN (AS2)
	}},
	{"Wii U", "Rayman Legends", []string{
		"000500001010B100", // USA (ARM)
		"0005000010111400", // EUR (ARM)
		"0005000010112600", // JPN (ARM)
	}},
	{"Wii U", "Need for Speed: Most Wanted U", []string{
		"0005000010128800", // USA (ANS)
		"0005000010128400", // EUR (ANS)
		"000500001012B700", // JPN (ANS)
	}},
	{"Wii U", "Minecraft: Wii U Edition", []string{
		"00050000101D9D00", // USA (AUM)
		"00050000101D7500", // EUR (AUM)
		"00050000101DBE00", // JPN (AUM)
	}},
	{"Wii U", "Dragon Quest X", []string{
		"0005000010115E00", // JPN (ADQ)
	}},
	{"Wii U", "Super Mario 3D World", []string{
		"0005000010145C00", // USA (ARD)
		"0005000010145D00", // EUR (ARD)
		"0005000010106100", // JPN (ARD)
	}},
	{"Wii U", "Dr. Luigi", []string{
		"0005000010152A00", // USA (WAQ)
		"0005000010152B00", // EUR (WAQ)
		"0005000010150300", // JPN (WAQ)
	}},
	{"Wii U", "Puyo Puyo Tetris", []string{
		"000500001014D900", // JPN (APT)
	}},
	{"Wii U", "Wii U Chat", []string{
		"000500101005A100", // USA (HAF)
		"000500101005A200", // EUR (HAF)
		"000500101005A000", // JPN (HAF)
	}},
	{"3DS", "Mario Kart 7", []string{
		"0004000000030800", // USA (AMK)
		"0004000000030700", // EUR (AMK)
		"0004000000030600", // JPN (AMK)
		"0004000000030A00", // KOR (AMK)
		"000400000008B400", // TWN (AMK)
	}},
	{"3DS", "Super Smash Bros. for Nintendo 3DS", []string{
		"00040000000EDF00", // USA (AXC)
		"00040000000EE000", // EUR (AXC)
		"00040000000B8B00", // JPN (AXC)
		"0004000000167C00", // KOR (AXC)
		"0004000000168600", // TWN (AXC)
	}},
	{"3DS", "Pokemon X / Y", []string{
		"0004000000055D00", // all regions (EKJ)
		"0004000000055E00", // all regions (EK2)
	}},
	{"3DS", "Pokemon Omega Ruby / Alpha Sapphire", []string{
		"000400000011C400", // all regions (ECR)
		"000400000011C500", // all regions (ECL)
	}},
	{"3DS", "Pokemon Sun / Moon", []string{
		"0004000000164800", // all regions (BND)
		"0004000000175E00", // all regions (BNE)
	}},
	{"3DS", "Pokemon Ultra Sun / Ultra Moon", []string{
		"00040000001B5000", // all regions (A2A)
		"00040000001B5100", // all regions (A2B)
	}},
	{"3DS", "Animal Crossing: New Leaf", []string{
		"0004000000086300", // USA (EGD)
		"0004000000198E00", // USA (EAA)
		"0004000000086400", // EUR (EGD)
		"0004000000198F00", // EUR (EAA)
		"0004000000086200", // JPN (EGD)
		"0004000000198D00", // JPN (EAA)
		"0004000000086500", // KOR (EGD)
		"0004000000199000", // KOR (EAA)
	}},
	{"3DS", "Monster Hunter 4 Ultimate", []string{
		"0004000000126300", // USA (BFG)
		"0004000000126100", // EUR (BFG)
		"000400000011D700", // JPN (BFG)
		"0004000000153200", // KOR (BFG)
		"0004000000141A00", // TWN (BFG)
	}},
	{"3DS", "Monster Hunter Generations", []string{
		"0004000000187000", // USA (BXX)
		"0004000000185B00", // EUR (BXX)
		"0004000000155400", // JPN (BXX)
		"000400000017EC00", // TWN (BXX)
	}},
	{"3DS", "Monster Hunter XX", []string{
		"0004000000197100", // JPN (AGQ)
		"00040000001B8100", // TWN (AGQ)
	}},
	{"3DS", "Monster Hunter 3 Ultimate", []string{
		"00040000000AE400", // USA (AMH)
		"00040000000B1D00", // EUR (AMH)
		"0004000000048100", // JPN (AMH)
		"00040000000D2E00", // KOR (AMH)
	}},
	{"3DS", "Kid Icarus: Uprising", []string{
		"0004000000030100", // USA (AKD)
		"0004000000030200", // EUR (AKD)
		"0004000000030000", // JPN (AKD)
	}},
	{"3DS", "Steel Diver: Sub Wars", []string{
		"00040000000D7D00", // USA (JNU)
		"00040000000D7E00", // EUR (JNU)
		"00040000000D7C00", // JPN (JNU)
	}},
	{"3DS", "IRONFALL Invasion", []string{
		"000400000015D800", // USA (KFL)
		"000400000015B100", // EUR (KFL)
		"000400000017D000", // JPN (KFL)
		"000400000017BF00", // TWN (KFL)
	}},
	{"3DS", "The Legend of Zelda: Tri Force Heroes", []string{
		"0004000000176F00", // USA (EA3)
		"0004000000177000", // EUR (EA3)
		"0004000000176E00", // JPN (EA3)
		"0004000000182B00", // KOR (EA3)
		"0004000000182C00", // TWN (EA3)
	}},
	{"3DS", "Team Kirby Clash Deluxe", []string{
		"00040000001AB800", // USA (JLK)
		"00040000001AB900", // EUR (JLK)
		"00040000001A0000", // JPN (JLK)
	}},
	{"3DS", "Pokemon Rumble World", []string{
		"0004000000164600", // all regions (KCF)
		"0004000000185A00", // all regions (ECF)
	}},
	{"3DS", "Super Mario Maker for Nintendo 3DS", []string{
		"00040000001A0400", // USA (AJH)
		"00040000001A0500", // EUR (AJH)
		"00040000001A0300", // JPN (AJH)
		"00040000001BB800", // KOR (AJH)
	}},
	{"3DS", "Yo-kai Watch 2", []string{
		"000400000019A900", // USA (BYG)
		"000400000019AA00", // USA (BYH)
		"00040000001B2700", // USA (BYS)
		"000400000019AB00", // EUR (BYG)
		"000400000019AC00", // EUR (BYH)
		"00040000001B2900", // EUR (BYS)
		"000400000012F800", // JPN (BYH)
		"000400000012F900", // JPN (BYG)
		"0004000000155100", // JPN (BYS)
		"000400000019AE00", // KOR (BYG)
		"000400000019AF00", // KOR (BYH)
		"00040000001B2A00", // KOR (BYS)
		"000400000019B000", // EUR, Z variant (BYG)
		"000400000019B100", // EUR, Z variant (BYH)
		"00040000001B2800", // EUR, Z variant (BYS)
	}},
	{"3DS", "Fire Emblem Fates", []string{
		"0004000000179400", // USA (BFX)
		"0004000000179600", // USA (BFY)
		"0004000000179800", // USA (BFZ)
		"0004000000179500", // EUR (BFX)
		"0004000000179700", // EUR (BFY)
		"000400000017A400", // EUR (BFW)
		"000400000017A800", // EUR (BFZ)
		"000400000012DC00", // JPN (BFX)
		"000400000012DD00", // JPN (BFY)
		"000400000012DE00", // JPN (BFZ)
		"000400000016B200", // JPN (BFW)
		"0004000000188E00", // KOR (BFZ)
		"000400000017B800", // TWN (BFW)
	}},
	{"3DS", "Mario Golf: World Tour", []string{
		"00040000000DCD00", // USA (AJ3)
		"00040000000DCE00", // EUR (AJ3)
		"00040000000A5300", // JPN (AJ3)
		"0004000000106300", // KOR (AJ3)
	}},
	{"3DS", "Mario Tennis Open", []string{
		"000400000007C700", // USA (AGA)
		"000400000007C800", // EUR (AGA)
		"0004000000064D00", // JPN (AGA)
		"00040000000B8800", // KOR (AGA)
		"00040000000B9100", // TWN (AGA)
	}},
	{"3DS", "Luigi's Mansion: Dark Moon", []string{
		"0004000000055F00", // USA (AGG)
		"0004000000076500", // EUR (AGG)
		"0004000000076400", // JPN (AGG)
		"00040000000CFF00", // KOR (AGG)
		"00040000000D0000", // TWN (AGG)
	}},
	{"3DS", "Code Name: S.T.E.A.M.", []string{
		"0004000000132500", // all regions (AY6)
	}},
	{"3DS", "Metroid Prime: Federation Force", []string{
		"000400000016E300", // USA (BCA)
		"0004000000175200", // EUR (BCA)
		"000400000016CE00", // JPN (BCA)
	}},
	{"3DS", "Resident Evil: Revelations", []string{
		"0004000000060200", // USA (ABR)
		"000400000005EE00", // EUR (ABR)
		"0004000000053B00", // JPN (ABR)
		"000400000009FF00", // KOR (ABR)
		"00040000000A0000", // TWN (ABR)
	}},
	{"3DS", "Dead or Alive Dimensions", []string{
		"0004000000034F00", // USA (ADD)
		"0004000000038A00", // EUR (ADD)
		"0004000000032000", // JPN (ADD)
	}},
	{"3DS", "Tetris Ultimate", []string{
		"000400000012EC00", // USA (BTL)
		"0004000000132400", // EUR (BTL)
	}},
	{"3DS", "Pokemon Bank", []string{
		"00040000000C9B00", // all regions (JTT)
	}},
	{"3DS", "F1 2011", []string{
		"000400000004DA00", // USA (AF4)
		"000400000006A600", // EUR (AF4)
		"0004000000067B00", // JPN (AF4)
	}},
	{"3DS", "Dr. Mario: Miracle Cure", []string{
		"000400000013BB00", // all regions (AX8)
	}},
	{"3DS", "Super Street Fighter IV 3D Edition", []string{
		"0004000000032D00", // USA (ASS)
		"0004000000033C00", // EUR (ASS)
		"0004000000030500", // JPN (ASS)
	}},
	{"3DS", "Ice Station Z", []string{
		"0004000000190300", // USA (KZS)
		"0004000000199700", // EUR (KZS)
		"00040000001BEA00", // JPN (KZS)
	}},
	{"3DS", "Monster Hunter 4", []string{
		"000400000004B500", // JPN (AH4)
		"0004000000105000", // KOR (AH4)
		"0004000000100900", // TWN (AH4)
	}},
	{"3DS", "Monster Hunter Stories", []string{
		"00040000001BC500", // USA (AAH)
		"00040000001BC600", // EUR (AAH)
		"000400000016E100", // JPN (AAH)
		"0004000000199C00", // TWN (AAH)
	}},
	{"3DS", "Puyo Puyo!! 20th Anniversary", []string{
		"0004000000056600", // JPN (AP2)
	}},
	{"3DS", "Resident Evil: The Mercenaries 3D", []string{
		"0004000000035900", // USA (ABM)
		"0004000000038B00", // EUR (ABM)
		"0004000000043E00", // JPN (ABM)
	}},
	{"3DS", "Yo-kai Watch Blasters", []string{
		"00040000001CEB00", // USA (BYA)
		"00040000001CEF00", // USA (BYB)
		"00040000001CEC00", // EUR (BYA)
		"00040000001CF000", // EUR (BYB)
		"000400000016C600", // JPN (BYB)
		"000400000016C700", // JPN (BYA)
		"00040000001CED00", // KOR (BYA)
		"00040000001CF200", // KOR (BYB)
	}},
	{"3DS", "Tetris: Axis", []string{
		"0004000000043800", // USA (ATL)
		"000400000004C100", // EUR (ATL)
		"0004000000039E00", // JPN (ATL)
	}},
	{"Wii U", "Mario vs. Donkey Kong: Tipping Stars", []string{
		"000400000012C800", // USA (JYL)
		"0005000010149300", // USA (WAF)
		"000400000012CA00", // EUR (JYL)
		"0005000010178E00", // EUR (WAF)
		"000400000012C900", // JPN (JYL)
		"0005000010179200", // JPN (WAF)
	}},

	// Switch titles that had no id yet. A Switch game usually has one id worldwide; the extra
	// ones are separate Japanese or Asian releases. From blawar/titledb (US, GB, JP, HK, KR),
	// each confirmed as a base program of that name on nlib (api.nlib.cc) except where noted.
	{"Switch", "Crash Team Racing Nitro-Fueled", []string{
		"0100F9F00C696000", // Crash™ Team Racing Nitro-Fueled
		"010040700E22E000", // クラッシュ・バンディクー レーシング ブッとびニトロ！
	}},
	{"Switch", "Minecraft Dungeons", []string{
		"01006C100EC08000", // Minecraft Dungeons
	}},
	{"Switch", "Pokemon Sword / Shield", []string{
		"0100ABF008968000", // Pokémon™ Sword
		"01008DB008C2C000", // Pokémon™ Shield
	}},
	{"Switch", "Pokemon Brilliant Diamond / Shining Pearl", []string{
		"0100000011D90000", // Pokémon™ Brilliant Diamond
		"010018E011D92000", // Pokémon™ Shining Pearl
	}},
	{"Switch", "Tetris 99", []string{
		"010040600C5CE000", // Tetris® 99
	}},
	{"Switch", "Super Mario Party", []string{
		"010036B0034E4000", // Super Mario Party™
	}},
	{"Switch", "Nintendo Switch Sports", []string{
		"0100D2F00D5C0000", // Nintendo Switch™ Sports
	}},
	{"Switch", "Pokken Tournament DX", []string{
		"0100B3F000BE2000", // Pokkén Tournament™ DX
	}},
	{"Switch", "Rocket League", []string{
		"01005EE0036EC000", // Rocket League®
	}},
	{"Switch", "Warframe", []string{
		"0100FEE00A64E000", // Warframe
	}},
	{"Switch", "Dragon Ball FighterZ", []string{
		"0100A250097F0000", // DRAGON BALL FighterZ
	}},
	{"Switch", "Ultra Street Fighter II: The Final Challengers", []string{
		"01007330027EE000", // Ultra Street Fighter® II: The Final Challengers
		"0100BBF0027EC000", // ULTRA STREET FIGHTER™ II The Final Challengers
		"0100C86000B58000", // ウルトラストリートファイターII ザ・ファイナルチャレンジャーズ
	}},
	{"Switch", "Mortal Kombat 11", []string{
		"0100F2200C984000", // Mortal Kombat 11
	}},
	{"Switch", "Mortal Kombat 1", []string{
		"01006560184E6000", // Mortal Kombat™ 1
	}},
	{"Switch", "Brawlhalla", []string{
		"0100C6800B934000", // Brawlhalla
	}},
	{"Switch", "Nickelodeon All-Star Brawl 2", []string{
		"010010701AFB2000", // Nickelodeon All-Star Brawl 2
	}},
	{"Switch", "BlazBlue: Cross Tag Battle", []string{
		"0100B61008208000", // BLAZBLUE CROSS TAG BATTLE
		"010090F00A76A000", // BLAZBLUE CROSS TAG BATTLE
	}},
	{"Switch", "Power Rangers: Battle for the Grid", []string{
		"0100E1E00CF1A000", // Power Rangers: Battle for the Grid
	}},
	{"Switch", "Samurai Shodown", []string{
		"01002DF00F76C000", // SAMURAI SHODOWN
		"010066501069C000", // SAMURAI SHODOWN
		"01008FC00D964000", // SAMURAI SPIRITS
	}},
	{"Switch", "Naruto x Boruto Ultimate Ninja Storm Connections", []string{
		"0100D2D0190A4000", // NARUTO X BORUTO Ultimate Ninja STORM CONNECTIONS
		"01005A20190A6000", // NARUTO X BORUTO ULTIMATE NINJA STORM CONNECTIONS
		"0100FA10190A0000", // NARUTO X BORUTO ナルティメットストームコネクションズ
		"0100D010190A8000", // 나루토 X 보루토 나루티밋 스톰 커넥션즈
	}},
	{"Switch", "Rivals of Aether", []string{
		"0100D2700DE7C000", // Rivals of Aether
	}},
	{"Switch", "Under Night In-Birth Exe:Late[cl-r]", []string{
		"010038C00EC34000", // Under Night In-Birth Exe:Late[cl-r]
		"0100B2900DF06000", // UNDER NIGHT IN-BIRTH Exe:Late[cl-r]
		"0100D3200C52A000", // UNDER NIGHT IN-BIRTH Exe:Late[cl-r]
	}},
	{"Switch", "Dragon Ball Xenoverse 2", []string{
		"010078D000F88000", // DRAGON BALL XENOVERSE 2 for Nintendo Switch
	}},
	{"Switch", "Team Sonic Racing", []string{
		"010092B0091D0000", // Team Sonic Racing
		"010084B00B36E000", // Team Sonic Racing™
		"010029D00B72A000", // チームソニックレーシング 新価格版
	}},
	{"Switch", "Fast RMX", []string{
		"01009510001CA000", // FAST RMX
	}},
	{"Switch", "Cruis'n Blast", []string{
		"0100B41013C82000", // Cruis'n Blast
	}},
	{"Switch", "Disney Speedstorm", []string{
		"0100F0401435E000", // Disney Speedstorm
	}},
	{"Switch", "Nickelodeon Kart Racers 3: Slime Speedway", []string{
		"01003BA01575E000", // Nickelodeon Kart Racers 3: Slime Speedway
	}},
	{"Switch", "Hotshot Racing", []string{
		"0100BDE008218000", // Hotshot Racing
	}},
	{"Switch", "Chocobo GP", []string{
		"01006A30124CA000", // nlib: "Chocobo GP Lite"
	}},
	{"Switch", "Redout 2", []string{
		"0100664016D5C000", // Redout 2
		"01009D80189C6000", // Redout 2 (レッドアウト２)
	}},
	{"Switch", "Fortnite", []string{
		"010025400AECE000", // Fortnite
	}},
	{"Switch", "Apex Legends", []string{
		"0100D9500FC66000", // Apex Legends™
	}},
	{"Switch", "Overwatch 2", []string{
		"0100F8600E21E000", // Overwatch® 2
	}},
	{"Switch", "Paladins", []string{
		"010023900AEE0000", // Paladins
	}},
	{"Switch", "Rogue Company", []string{
		"0100D0200E97A000", // Rogue Company
	}},
	{"Switch", "Warface: Clutch", []string{
		"0100D1E00E972000", // Warface: Clutch
	}},
	{"Switch", "DOOM Eternal", []string{
		"0100B1A00D8CE000", // DOOM® Eternal
	}},
	{"Switch", "Borderlands 3", []string{
		"01009970122E4000", // Borderlands 3 Ultimate Edition
	}},
	{"Switch", "Wolfenstein: Youngblood", []string{
		"01003BD00CAAE000", // Wolfenstein: Youngblood
		"0100D1F00EB72000", // Wolfenstein®: Youngblood™
	}},
	{"Switch", "Payday 2", []string{
		"0100274004052000", // PAYDAY 2
	}},
	{"Switch", "Pac-Man 99", []string{
		"0100AD9012510000", // PAC-MAN™ 99
	}},
	{"Switch", "F-Zero 99", []string{
		"0100CCF019C8C000", // F-ZERO™ 99
	}},
	{"Switch", "WarioWare: Get It Together!", []string{
		"0100563010E0C000", // WarioWare™: Get It Together!
	}},
	{"Switch", "Jackbox Party Pack (series)", []string{
		"0100AE5003EE6000", // The Jackbox Party Pack
		"010015D003EE4000", // The Jackbox Party Pack 2
		"0100CC80013D6000", // The Jackbox Party Pack 3
		"0100E1F003EE8000", // The Jackbox Party Pack 4
		"01006FE0096AC000", // The Jackbox Party Pack 5
		"01005A400DB52000", // The Jackbox Party Pack 6
		"01007F30113A6000", // The Jackbox Party Pack 7
		"010050F015280000", // The Jackbox Party Pack 8
		"0100BFB018134000", // The Jackbox Party Pack 9
		"0100C9F01B526000", // The Jackbox Party Pack 10
		"0100E5B022310000", // The Jackbox Party Pack 11
	}},
	{"Switch", "Uno", []string{
		"01005AA00372A000", // UNO® for Nintendo Switch
		"0100B6E012EBE000", // UNO®
	}},
	{"Switch", "Monopoly Madness", []string{
		"01005FF013DC2000", // MONOPOLY® Madness
	}},
	{"Switch", "Yu-Gi-Oh! Master Duel", []string{
		"010039E012F40000", // Yu-Gi-Oh! Master Duel
	}},
	{"Switch", "Yu-Gi-Oh! Legacy of the Duelist: Link Evolution", []string{
		"010022400BE5A000", // Yu-Gi-Oh! Legacy of the Duelist : Link Evolution
	}},
	{"Switch", "Trivial Pursuit Live!", []string{
		"010020700A5E0000", // TRIVIAL PURSUIT® Live!
	}},
	{"Switch", "Wheel of Fortune / Jeopardy!", []string{
		"010033600ADE6000", // Wheel of Fortune®
		"01006E400AE2A000", // Jeopardy!®
	}},
	{"Switch", "EA Sports FC 24", []string{
		"0100BDB01A0E6000", // EA SPORTS FC™ 24
	}},
	{"Switch", "NBA 2K24", []string{
		"010006501A8D8000", // NBA 2K24 Kobe Bryant Edition
	}},
	{"Switch", "Super Mega Baseball 4", []string{
		"0100D43017114000", // Super Mega Baseball™ 4
	}},
	{"Switch", "Windjammers 2", []string{
		"0100AF500EF0C000", // Windjammers 2
		"0100ACB016FA0000", // Windjammers 2 (야외 원반 2)
	}},
	{"Switch", "Captain Tsubasa: Rise of New Champions", []string{
		"0100EAE010560000", // Captain Tsubasa: Rise of New Champions
		"0100CCA00DAE6000", // Captain Tsubasa: Rise of New Champions
		"010090B00CA4E000", // キャプテン翼 RISE OF NEW CHAMPIONS
	}},
	{"Switch", "Golf With Your Friends", []string{
		"01006FB00EBE0000", // Golf With Your Friends
	}},
	{"Switch", "Diablo III: Eternal Collection", []string{
		"01001B300B9BE000", // Diablo III: Eternal Collection
		"010032F00C04A000", // ディアブロ III エターナルコレクション
	}},
	{"Switch", "Monster Hunter Stories 2: Wings of Ruin", []string{
		"0100E21011446000", // Monster Hunter Stories 2: Wings of Ruin
		"0100CB700D438000", // モンスターハンターストーリーズ２ ～破滅の翼～
	}},
	{"Switch", "Dragon Quest X Offline / Online", []string{
		"0100E2E0152E4000", // ドラゴンクエストX 目覚めし五つの種族 オフライン
		"0100647003750000", // ドラゴンクエストX オンライン
	}},
	{"Switch", "Final Fantasy Crystal Chronicles Remastered", []string{
		"0100CE4010AAC000", // FINAL FANTASY® CRYSTAL CHRONICLES™ Remastered Edition
		"0100BE0010AAE000", // FINAL FANTASY® CRYSTAL CHRONICLES™ Remastered Edition
		"0100BE900EB8A000", // ファイナルファンタジー・クリスタルクロニクル リマスター
	}},
	{"Switch", "Divinity: Original Sin 2 — Definitive Edition", []string{
		"010027400CDC6000", // Divinity: Original Sin 2 - Definitive Edition
		"010016F00E952000", // ディヴィニティ：オリジナル・シン 2 ディフィニティブエディション
	}},
	{"Switch", "Temtem", []string{
		"0100C8B012DEA000", // Temtem
	}},
	{"Switch", "Palia", []string{
		"01007D8018FE2000", // Palia
	}},
	{"Switch", "Pokemon UNITE", []string{
		"0100939011ED4000", // Pokémon UNITE
	}},
	{"Switch", "Torchlight III", []string{
		"010075400DDB8000", // Torchlight III
	}},
	{"Switch", "Portal Knights", []string{
		"0100437004170000", // Portal Knights
	}},
	{"Switch", "Dragon Marked for Death", []string{
		"010089700150E000", // Dragon Marked for Death: Frontline Fighters
	}},
	{"Switch", "Bloodstained: Ritual of the Night", []string{
		"0100BF500207C000", // Bloodstained: Ritual of the Night
	}},
	{"Switch", "Overcooked! 2", []string{
		"01006FD0080B2000", // Overcooked! 2
	}},
	{"Switch", "Moving Out 2", []string{
		"010065D018172000", // Moving Out 2
	}},
	{"Switch", "It Takes Two", []string{
		"010092A0172E4000", // It Takes Two
	}},
	{"Switch", "Gang Beasts", []string{
		"0100D58012E12000", // Gang Beasts
	}},
	{"Switch", "Streets of Rage 4", []string{
		"0100EC9010258000", // Streets of Rage 4
		"01003D80117F0000", // ベア・ナックルIV（Bare Knuckle IV）
	}},
	{"Switch", "Teenage Mutant Ninja Turtles: Shredder's Revenge", []string{
		"0100FE701475A000", // Teenage Mutant Ninja Turtles: Shredder's Revenge
		"0100BB8017F24000", // 돌연변이 닌자 거북이: 슈레더의 복수 (Teenage Mutant Ninja Turtles: Shredder's Revenge)
	}},
	{"Switch", "River City Girls 2", []string{
		"0100026016320000", // River City Girls 2
		"01002E80168F4000", // 熱血硬派くにおくん外伝 リバーシティガールズ2
	}},
	{"Switch", "Castle Crashers Remastered", []string{
		"010001300D14A000", // Castle Crashers Remastered
	}},
	{"Switch", "Risk of Rain 2", []string{
		"010076D00E4BA000", // Risk of Rain 2
	}},
	{"Switch", "Risk of Rain Returns", []string{
		"0100D1801A0F4000", // Risk of Rain Returns
	}},
	{"Switch", "Nine Parchments", []string{
		"0100D03003F0E000", // Nine Parchments
	}},
	{"Switch", "Trine 4: The Nightmare Prince", []string{
		"010055E00CA68000", // Trine 4: The Nightmare Prince
		"0100157010ECE000", // Trine 4 (HK), titledb only: nlib does not know it
	}},
	{"Switch", "Ultimate Chicken Horse", []string{
		"0100FCF002A58000", // Ultimate Chicken Horse
	}},
	{"Switch", "Duck Game", []string{
		"0100750001C70000", // Duck Game
	}},
	{"Switch", "Boomerang Fu", []string{
		"010081A00EE62000", // Boomerang Fu
		"01005AA011A4E000", // ブーメランヒュー
	}},
	{"Switch", "Pico Park", []string{
		"01004EA00DF70000", // PICO PARK
	}},
	{"Switch", "Unrailed!", []string{
		"010042C011476000", // Unrailed!
		"010037E0131D0000", // Unrailed!
	}},
	{"Switch", "Killer Queen Black", []string{
		"0100F2900B3E2000", // Killer Queen Black
	}},
	{"Switch", "Cake Bash", []string{
		"010014A0115BE000", // Cake Bash
	}},
	{"Switch", "Spelunky 2", []string{
		"01007EC013ABC000", // Spelunky 2
	}},
	{"Switch", "Terraria", []string{
		"0100E46006708000", // Terraria
		"010024B00F9D0000", // テラリア
		"0100E990104DC000", // 테라리아 (Terraria)
	}},
	{"Switch", "Don't Starve Together", []string{
		"010090100E334000", // Don't Starve Together
	}},
	{"Switch", "ARK: Survival Evolved", []string{
		"0100D4A00B284000", // ARK: Survival Evolved
		"01005C100C056000", // ARK: Survival Evolved
	}},
	{"Switch", "No Man's Sky", []string{
		"0100853015E86000", // No Man's Sky
	}},
	{"Switch", "Dragon Quest Builders 2", []string{
		"010042000A986000", // DRAGON QUEST BUILDERS™ 2
		"010050000705E000", // ドラゴンクエストビルダーズ２ 破壊神シドーとからっぽの島
		"01001C500E72E000", // 드래곤 퀘스트 빌더즈 2 파괴신 시도와 텅 빈 섬
	}},
	{"Switch", "The Survivalists", []string{
		"0100EF200DA60000", // The Survivalists
	}},
	{"Switch", "Farming Simulator 23", []string{
		"01001E3017A10000", // Farming Simulator 23; Nintendo Switch™ Edition
	}},
	{"Switch", "PowerWash Simulator", []string{
		"0100926016012000", // PowerWash Simulator
	}},
	{"Switch", "Disney Dreamlight Valley", []string{
		"0100D39012C1A000", // Disney Dreamlight Valley
	}},
	{"Switch", "Sid Meier's Civilization VI", []string{
		"010044500C182000", // Sid Meier’s Civilization VI
	}},
	{"Switch", "Wargroove 2", []string{
		"0100AD401A2F0000", // Wargroove 2
	}},
	{"Switch", "Advance Wars 1+2: Re-Boot Camp", []string{
		"0100300012F2A000", // Advance Wars™ 1+2: Re-Boot Camp
	}},
	{"Switch", "Arena of Valor", []string{
		"01008D8006A6A000", // Arena of Valor
		"0100944009646000", // Arena of Valor
	}},
	{"Switch", "Worms W.M.D", []string{
		"01001AE005166000", // Worms W.M.D
	}},
	{"Switch", "Taiko no Tatsujin: Rhythm Festival", []string{
		"01006B0016AD6000", // Taiko no Tatsujin: Rhythm Festival
		"0100BCA0135A0000", // 太鼓の達人 ドンダフルフェスティバル
		"01002460135A4000", // 태고의 달인 쿵딱! 원더풀 페스티벌
	}},
}

// titleIDsMigration sets the ids above on their rows and logs each change like the API does
// (actor "migration"). A row that is missing, already has ids, or would collide with another
// row's ids is skipped rather than failing the boot.
func titleIDsMigration() string {
	var vals []string
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	for _, t := range catalogTitleIDs {
		ids := make([]string, len(t.IDs))
		for i, id := range t.IDs {
			ids[i] = q(id)
		}
		vals = append(vals, fmt.Sprintf("(%s, %s, ARRAY[%s]::text[])", q(t.Console), q(t.Name), strings.Join(ids, ",")))
	}
	return `WITH seed(console, name, ids) AS (VALUES
	` + strings.Join(vals, ",\n\t") + `
), upd AS (
	UPDATE requests r SET title_ids = s.ids, title_id = s.ids[1], last_updated = now()
	FROM seed s
	WHERE r.kind = 'title' AND r.console = s.console AND r.name = s.name AND r.title_ids = '{}'
		AND NOT EXISTS (SELECT 1 FROM requests o WHERE o.id <> r.id AND o.title_ids && s.ids)
	RETURNING r.id, s.ids
)
INSERT INTO request_changes(request_id, actor, action, changes)
SELECT id, 'migration', 'update', jsonb_build_object(
	'title_id', jsonb_build_object('old', '', 'new', ids[1]),
	'title_ids', jsonb_build_object('old', '', 'new', array_to_string(ids, ',')))
FROM upd;`
}

func init() { migrations["0008_catalog_title_ids"] = titleIDsMigration() }
