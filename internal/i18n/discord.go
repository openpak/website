package i18n

// Linking a Discord account to the OpenPak account (pages/discord.go).
func init() {
	for lang, c := range map[string]Catalogue{
		"en": {
			"discord.title":       "Discord",
			"discord.help":        "Link your Discord account so Pakka, the assistant on the OpenPak Discord server, knows which OpenPak account it is talking to and can apply the same rules as on the website. OpenPak only reads your Discord user id and name.",
			"discord.link":        "Link Discord",
			"discord.unlink":      "Unlink Discord",
			"discord.linked.help": "Pakka on Discord uses this link to apply your account's rules. Unlink it and Pakka no longer answers you there.",
			"discord.ok":          "Discord linked.",
			"discord.removed":     "Discord unlinked.",
			"discord.taken":       "That Discord account is already linked to another OpenPak account. Unlink it there first.",
			"discord.cancelled":   "Linking was cancelled. Nothing changed.",
			"discord.failed":      "Could not link Discord. Try again in a moment.",
		},
		"de": {
			"discord.title":       "Discord",
			"discord.help":        "Verknüpfe dein Discord-Konto, damit Pakka, der Assistent auf dem OpenPak-Discord-Server, weiß, mit welchem OpenPak-Konto er spricht, und dieselben Regeln wie auf der Website anwenden kann. OpenPak liest nur deine Discord-Nutzer-ID und deinen Namen.",
			"discord.link":        "Discord verknüpfen",
			"discord.unlink":      "Discord-Verknüpfung lösen",
			"discord.linked.help": "Pakka auf Discord nutzt diese Verknüpfung, um die Regeln deines Kontos anzuwenden. Löst du sie, antwortet Pakka dir dort nicht mehr.",
			"discord.ok":          "Discord verknüpft.",
			"discord.removed":     "Discord-Verknüpfung gelöst.",
			"discord.taken":       "Dieses Discord-Konto ist bereits mit einem anderen OpenPak-Konto verknüpft. Löse die Verknüpfung dort zuerst.",
			"discord.cancelled":   "Das Verknüpfen wurde abgebrochen. Nichts wurde geändert.",
			"discord.failed":      "Discord konnte nicht verknüpft werden. Versuche es gleich noch einmal.",
		},
		"es": {
			"discord.title":       "Discord",
			"discord.help":        "Vincula tu cuenta de Discord para que Pakka, el asistente del servidor de Discord de OpenPak, sepa con qué cuenta de OpenPak habla y aplique las mismas reglas que en el sitio web. OpenPak solo lee tu ID de usuario y tu nombre de Discord.",
			"discord.link":        "Vincular Discord",
			"discord.unlink":      "Desvincular Discord",
			"discord.linked.help": "Pakka en Discord usa este vínculo para aplicar las reglas de tu cuenta. Si lo quitas, Pakka deja de responderte allí.",
			"discord.ok":          "Discord vinculado.",
			"discord.removed":     "Discord desvinculado.",
			"discord.taken":       "Esa cuenta de Discord ya está vinculada a otra cuenta de OpenPak. Desvincúlala allí primero.",
			"discord.cancelled":   "Se canceló la vinculación. No cambió nada.",
			"discord.failed":      "No se pudo vincular Discord. Inténtalo de nuevo en un momento.",
		},
		"fr": {
			"discord.title":       "Discord",
			"discord.help":        "Associez votre compte Discord pour que Pakka, l’assistant du serveur Discord d’OpenPak, sache à quel compte OpenPak il parle et applique les mêmes règles que sur le site. OpenPak lit seulement votre identifiant et votre nom Discord.",
			"discord.link":        "Associer Discord",
			"discord.unlink":      "Dissocier Discord",
			"discord.linked.help": "Pakka sur Discord utilise cette association pour appliquer les règles de votre compte. Si vous la retirez, Pakka ne vous répond plus là-bas.",
			"discord.ok":          "Discord associé.",
			"discord.removed":     "Discord dissocié.",
			"discord.taken":       "Ce compte Discord est déjà associé à un autre compte OpenPak. Dissociez-le d’abord là-bas.",
			"discord.cancelled":   "L’association a été annulée. Rien n’a changé.",
			"discord.failed":      "Impossible d’associer Discord. Réessayez dans un instant.",
		},
		"it": {
			"discord.title":       "Discord",
			"discord.help":        "Collega il tuo account Discord così Pakka, l’assistente del server Discord di OpenPak, sa con quale account OpenPak sta parlando e applica le stesse regole del sito. OpenPak legge solo il tuo ID utente e il tuo nome Discord.",
			"discord.link":        "Collega Discord",
			"discord.unlink":      "Scollega Discord",
			"discord.linked.help": "Pakka su Discord usa questo collegamento per applicare le regole del tuo account. Se lo scolleghi, Pakka non ti risponde più lì.",
			"discord.ok":          "Discord collegato.",
			"discord.removed":     "Discord scollegato.",
			"discord.taken":       "Quell’account Discord è già collegato a un altro account OpenPak. Scollegalo prima da lì.",
			"discord.cancelled":   "Il collegamento è stato annullato. Non è cambiato nulla.",
			"discord.failed":      "Impossibile collegare Discord. Riprova tra un attimo.",
		},
		"pt-BR": {
			"discord.title":       "Discord",
			"discord.help":        "Vincule sua conta do Discord para que a Pakka, a assistente do servidor de Discord do OpenPak, saiba com qual conta do OpenPak está falando e aplique as mesmas regras do site. O OpenPak lê apenas seu ID de usuário e seu nome no Discord.",
			"discord.link":        "Vincular Discord",
			"discord.unlink":      "Desvincular Discord",
			"discord.linked.help": "A Pakka no Discord usa este vínculo para aplicar as regras da sua conta. Se você o remover, a Pakka deixa de responder você lá.",
			"discord.ok":          "Discord vinculado.",
			"discord.removed":     "Discord desvinculado.",
			"discord.taken":       "Essa conta do Discord já está vinculada a outra conta do OpenPak. Desvincule-a lá primeiro.",
			"discord.cancelled":   "A vinculação foi cancelada. Nada mudou.",
			"discord.failed":      "Não foi possível vincular o Discord. Tente novamente em instantes.",
		},
	} {
		for k, v := range c {
			languages[lang][k] = v
		}
	}
}
