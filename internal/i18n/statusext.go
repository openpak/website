package i18n

// The outside checks on /status: everything status.openpak.org probes from another host.
func init() {
	for lang, c := range map[string]Catalogue{
		"en": {
			"status.ext.title":       "Checked from outside",
			"status.ext.sub":         "Every service, probed from a separate host the way a console, a browser or the app reaches it.",
			"status.ext.of":          "of",
			"status.ext.working":     "working",
			"status.ext.show":        "Show all",
			"status.ext.unavailable": "The outside checker could not be reached. That says nothing about whether OpenPak is working.",
			"status.ext.link":        "Full history on status.openpak.org",
		},
		"de": {
			"status.ext.title":       "Von außen geprüft",
			"status.ext.sub":         "Jeder Dienst, von einem getrennten Rechner so geprüft, wie ihn eine Konsole, ein Browser oder die App erreicht.",
			"status.ext.of":          "von",
			"status.ext.working":     "in Betrieb",
			"status.ext.show":        "Alle anzeigen",
			"status.ext.unavailable": "Der externe Prüfer war nicht erreichbar. Das sagt nichts darüber, ob OpenPak funktioniert.",
			"status.ext.link":        "Vollständiger Verlauf auf status.openpak.org",
		},
		"fr": {
			"status.ext.title":       "Vérifié de l'extérieur",
			"status.ext.sub":         "Chaque service, sondé depuis une machine séparée comme le fait une console, un navigateur ou l'application.",
			"status.ext.of":          "sur",
			"status.ext.working":     "fonctionnent",
			"status.ext.show":        "Tout afficher",
			"status.ext.unavailable": "Le vérificateur externe est injoignable. Cela ne dit rien sur le bon fonctionnement d'OpenPak.",
			"status.ext.link":        "Historique complet sur status.openpak.org",
		},
		"pt-BR": {
			"status.ext.title":       "Verificado de fora",
			"status.ext.sub":         "Cada serviço, testado de uma máquina separada como um console, um navegador ou o app o alcançam.",
			"status.ext.of":          "de",
			"status.ext.working":     "funcionando",
			"status.ext.show":        "Mostrar tudo",
			"status.ext.unavailable": "Não foi possível alcançar o verificador externo. Isso não diz nada sobre o funcionamento do OpenPak.",
			"status.ext.link":        "Histórico completo em status.openpak.org",
		},
		"it": {
			"status.ext.title":       "Verificato dall'esterno",
			"status.ext.sub":         "Ogni servizio, sondato da una macchina separata come lo raggiungono una console, un browser o l'app.",
			"status.ext.of":          "su",
			"status.ext.working":     "funzionano",
			"status.ext.show":        "Mostra tutto",
			"status.ext.unavailable": "Il verificatore esterno non è raggiungibile. Questo non dice nulla sul funzionamento di OpenPak.",
			"status.ext.link":        "Cronologia completa su status.openpak.org",
		},
		"es": {
			"status.ext.title":       "Comprobado desde fuera",
			"status.ext.sub":         "Cada servicio, sondeado desde una máquina aparte como lo alcanzan una consola, un navegador o la app.",
			"status.ext.of":          "de",
			"status.ext.working":     "funcionan",
			"status.ext.show":        "Mostrar todo",
			"status.ext.unavailable": "No se pudo contactar con el comprobador externo. Eso no dice nada sobre si OpenPak funciona.",
			"status.ext.link":        "Historial completo en status.openpak.org",
		},
	} {
		for k, v := range c {
			languages[lang][k] = v
		}
	}
}
