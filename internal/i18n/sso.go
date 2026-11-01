package i18n

// Sign-in for sites on other openpak.org subdomains (internal/sso).
func init() {
	for lang, c := range map[string]Catalogue{
		"en": {
			"sso.title":          "Sign in",
			"sso.bad":            "This sign-in link is not valid. Go back to the site you came from and try again.",
			"sso.logout.title":   "Sign out of OpenPak",
			"sso.logout.body":    "This signs you out of openpak.org. The site you came from signs you out too.",
			"sso.logout.confirm": "Sign out",
			"sso.logout.cancel":  "Stay signed in",
		},
		"de": {
			"sso.title":          "Anmelden",
			"sso.bad":            "Dieser Anmeldelink ist ungültig. Geh zur Seite zurück, von der du kommst, und versuche es erneut.",
			"sso.logout.title":   "Von OpenPak abmelden",
			"sso.logout.body":    "Du wirst von openpak.org abgemeldet. Die Seite, von der du kommst, meldet dich ebenfalls ab.",
			"sso.logout.confirm": "Abmelden",
			"sso.logout.cancel":  "Angemeldet bleiben",
		},
		"fr": {
			"sso.title":          "Se connecter",
			"sso.bad":            "Ce lien de connexion n’est pas valide. Retourne sur le site d’où tu viens et réessaie.",
			"sso.logout.title":   "Se déconnecter d’OpenPak",
			"sso.logout.body":    "Tu seras déconnecté d’openpak.org. Le site d’où tu viens te déconnecte aussi.",
			"sso.logout.confirm": "Se déconnecter",
			"sso.logout.cancel":  "Rester connecté",
		},
		"pt-BR": {
			"sso.title":          "Entrar",
			"sso.bad":            "Este link de entrada não é válido. Volte ao site de onde você veio e tente de novo.",
			"sso.logout.title":   "Sair do OpenPak",
			"sso.logout.body":    "Isso encerra sua sessão em openpak.org. O site de onde você veio também encerra a sua.",
			"sso.logout.confirm": "Sair",
			"sso.logout.cancel":  "Continuar conectado",
		},
		"it": {
			"sso.title":          "Accedi",
			"sso.bad":            "Questo link di accesso non è valido. Torna al sito da cui vieni e riprova.",
			"sso.logout.title":   "Esci da OpenPak",
			"sso.logout.body":    "Verrai disconnesso da openpak.org. Anche il sito da cui vieni ti disconnette.",
			"sso.logout.confirm": "Esci",
			"sso.logout.cancel":  "Resta connesso",
		},
		"es": {
			"sso.title":          "Iniciar sesión",
			"sso.bad":            "Este enlace de inicio de sesión no es válido. Vuelve al sitio del que vienes e inténtalo de nuevo.",
			"sso.logout.title":   "Cerrar sesión en OpenPak",
			"sso.logout.body":    "Se cerrará tu sesión en openpak.org. El sitio del que vienes también cierra la tuya.",
			"sso.logout.confirm": "Cerrar sesión",
			"sso.logout.cancel":  "Seguir con la sesión iniciada",
		},
	} {
		for k, v := range c {
			languages[lang][k] = v
		}
	}
}
