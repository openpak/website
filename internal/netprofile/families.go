package netprofile

// Universe is the ceiling on any family a platform map may list: the domain
// families a real console is allowed to be redirected with — nn-sssl-dns's
// compiled-in list, which was the second source of this answer until NP-4
// made the DNS server read the platform map instead.
//
// A family outside it cannot be served to a console whose only knob is a DNS
// setting, and an emulator's compiled-in allowlist is the ceiling on what the
// profile may ask of it in any case. The map is validated against this at
// load; a family outside it is a startup error, not a warning.
//
// Ryujinx's compiled-in ceiling also carries `.nintendo.co.jp` and
// `.nintendo-europe.com`; nothing this server routes lives there, so they are
// narrowed away rather than listed. Should a title ever need one, it joins
// the DNS server's list first and this file second — in that order, because
// the DNS server answers consoles that have nowhere else to go.
var Universe = []string{
	// Nintendo's own, plus the connection test's family: a console on OpenPak
	// reaches OpenPak for everything it would have asked Nintendo, conntest
	// included, because OpenPak answers that probe as a service rather than
	// leaving it to measure somebody else's internet.
	".nintendo.net",
	".nintendowifi.net",
	".nintendo.com",
	".openpak.org",
	".gamespy.com",
	// Third parties a console reaches directly, without a Nintendo host in the
	// path. OpenPak answers on some of these (Battle.net, Photon, Among Us) and
	// on none of the rest yet, which is the deliberate trade: a title whose
	// service we do not run fails to connect instead of connecting and being
	// refused. On this project's audience that refusal was already certain --
	// these services ask Nintendo to vouch for a token the console does not
	// have -- so nothing that worked stops working.
	".battle.net",
	".among.us",
	".photonengine.io",
	".exitgames.com",
	".demonware.net",
	".epicgames.dev",
	".ea.com",
	".xboxlive.com",
	".live.com",
	".mojang.com",
	".microsoft.com",
}
