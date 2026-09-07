// Package i18n holds the single string catalogue (WEB-09). All UI copy is
// referenced by key so a second language is one new map.
package i18n

import "strings"

// Catalogue is the string catalogue for one language.
type Catalogue map[string]string

var languages = map[string]Catalogue{
	"en": {
		"site.name":                    "OpenPak",
		"site.tagline":                 "The community account network for open console play.",
		"nav.home":                     "Home",
		"nav.account":                  "Account",
		"nav.friends":                  "Friends",
		"nav.invitations":              "Invitations",
		"nav.status":                   "Status",
		"nav.signin":                   "Sign in",
		"nav.signout":                  "Sign out",
		"nav.register":                 "Create account",
		"nav.admin":                    "Admin",
		"landing.titles":               "Supported titles",
		"landing.titles.none":          "Titles will be announced here.",
		"landing.cta":                  "Create your account",
		"auth.register.title":          "Create your OpenPak account",
		"auth.login.title":             "Sign in",
		"auth.email":                   "Email address",
		"auth.password":                "Password",
		"auth.displayname":             "Display name",
		"auth.forgot":                  "Forgot your password?",
		"auth.remember":                "Use your strongest password; the account protects your console identity.",
		"auth.register.closed":         "Registrations are currently closed. Please check back soon.",
		"auth.register.sent":           "If the address is registerable, a verification email has been sent. Check your inbox.",
		"auth.verify.ok":               "Your email is verified. You can sign in now.",
		"auth.verify.bad":              "This verification link is invalid or expired.",
		"auth.login.failed":            "Invalid credentials.",
		"auth.login.banned":            "This account is suspended.",
		"auth.forgot.title":            "Reset your password",
		"auth.forgot.sent":             "If the address is registered, a reset email has been sent.",
		"auth.reset.title":             "Choose a new password",
		"auth.reset.ok":                "Password updated and old sessions signed out. You can sign in now.",
		"auth.reset.bad":               "This reset link is invalid or expired.",
		"account.title":                "Your account",
		"account.email":                "Email",
		"account.displayname":          "Display name",
		"account.friendcode":           "Friend code",
		"account.friendcode.none":      "Link a console to receive a friend code.",
		"account.links":                "Linked platforms",
		"account.links.none":           "No consoles linked. Link from your console or emulator; the link appears here.",
		"account.links.revoke":         "Revoke link",
		"account.avatar":               "Avatar",
		"account.avatar.upload":        "Upload avatar (PNG or JPEG)",
		"account.avatar.pending":       "Avatar sync to consoles is pending a core media contract (CR-2).",
		"account.sessions":             "Signed-in devices",
		"account.sessions.revoke":      "Revoke",
		"account.sessions.current":     "this device",
		"friends.title":                "Friends",
		"friends.add":                  "Add by friend code",
		"friends.code":                 "Friend code (e.g. SW-1234-5678-9012)",
		"friends.list.none":            "No friends yet. Add someone by their friend code.",
		"friends.incoming":             "Incoming requests",
		"friends.outgoing":             "Sent requests",
		"friends.accept":               "Accept",
		"friends.decline":              "Decline",
		"friends.remove":               "Remove",
		"friends.block":                "Block",
		"friends.unblock":              "Unblock",
		"friends.request.ok":           "Friend request sent.",
		"friends.request.unknown":      "No account found for that friend code.",
		"friends.request.self":         "You cannot add yourself.",
		"friends.blockednote":          "You blocked this account.",
		"invitations.title":            "Invitations",
		"invitations.pending":          "Pending invitations",
		"invitations.none":             "No pending invitations.",
		"invitations.pending.contract": "The invitations view needs a read-only core contract (CR-1) and is not available yet.",
		"status.title":                 "Service status",
		"status.core":                  "Account core",
		"status.db":                    "Website database",
		"status.storage":               "Media storage",
		"status.ok":                    "Operational",
		"status.down":                  "Degraded",
		"admin.title":                  "Administration",
		"admin.config":                 "Site configuration",
		"admin.events":                 "Audit log (core events)",
		"admin.users":                  "Users & moderation",
		"admin.registrations":          "Registrations open",
		"admin.banner":                 "Banner message (empty = hidden)",
		"admin.save":                   "Save",
		"admin.denied":                 "Your account is not an administrator.",
		"admin.pending.cr5":            "User search, ban and unban require an admin contract in the account core (CR-5). This page stays fail-closed until then.",
		"admin.pending.cr6":            "The reports queue requires a reports contract in the account core (CR-6).",
		"msg.saved":                    "Saved.",
		"msg.error":                    "Something went wrong. Please try again.",
		"footer.note":                  "OpenPak is an independent community network. Not affiliated with any console vendor.",
	},
}

// T translates a key in the given language, falling back to English and
// finally to the key itself (so missing strings are visible, not blank).
func T(lang, key string) string {
	if c, ok := languages[lang]; ok {
		if s, ok := c[key]; ok {
			return s
		}
	}
	if s, ok := languages["en"][key]; ok {
		return s
	}
	return key
}

// Lang resolves the UI language from an Accept-Language header. English is
// the only catalogue today; the negotiation point is here so more languages
// are a catalogue away.
func Lang(acceptLanguage string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(acceptLanguage)), "en") || acceptLanguage == "" {
		return "en"
	}
	return "en"
}
