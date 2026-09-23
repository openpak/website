// Package accounts holds the account actions the website and the mobile API
// both perform. Each surface keeps its own request parsing and its own way of
// answering; what lives here is the part that has to be the same on both, so
// the two cannot drift apart.
package accounts

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/adapter"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/wfc"
)

// ErrWFCUnreachable is a friend code that could not be looked up at all, as
// opposed to one nobody answers to (wfc.ErrNotFound).
var ErrWFCUnreachable = errors.New("accounts: the Wii/DS server did not answer")

// LinkWFC links a Wii or DS from a friend code the player read off a game. WFC has no login,
// so this is the only link path; the core holds the link ("wfc", console user id) and nn-wfc
// stores nothing. The errors a caller tells apart: wfc.ErrNotFound, ErrWFCUnreachable and
// core.ErrConflict (the console is linked to another account).
func LinkWFC(ctx context.Context, w *wfc.Client, links core.Links, accountID, friendCode string) error {
	profile, err := w.Resolve(ctx, friendCode)
	switch {
	case errors.Is(err, wfc.ErrNotFound):
		return err
	case err != nil:
		return fmt.Errorf("%w: %v", ErrWFCUnreachable, err)
	}
	subject := strconv.FormatUint(profile.UserID, 10)
	return links.Link(ctx, accountID, "wfc", subject, "wfc:"+accountID+":"+subject)
}

// StorageOAuthState binds a storage consent round trip to the account without a database row:
// the account id plus an HMAC the callback recomputes. The website session secret signs it, so
// a state forged elsewhere never verifies. The app asks for the consent URL and the website's
// callback finishes it, so both have to sign the same way.
func StorageOAuthState(sess *session.Manager, accountID string) string {
	return accountID + "." + sess.HashToken("oauth:"+accountID)
}

// AccountFromStorageOAuthState is the other half of StorageOAuthState.
func AccountFromStorageOAuthState(sess *session.Manager, state string) (string, bool) {
	i := strings.LastIndexByte(state, '.')
	if i <= 0 {
		return "", false
	}
	acct := state[:i]
	if sess.HashToken("oauth:"+acct) != state[i+1:] {
		return "", false
	}
	return acct, true
}

// StorageOAuthCallback is the website page a provider returns the player to. The redirect
// URI has to be identical when the consent starts and when the code is exchanged, whichever
// surface started it.
func StorageOAuthCallback(scheme, host, provider string) string {
	return scheme + "://" + host + "/account/saves/storage/oauth/" + provider + "/callback"
}

// Delete starts deleting an account once its password has been given again: a signed-in
// session alone, or a bearer lifted off a phone, must not be enough to end an account. A wrong
// password is core.ErrInvalidCredentials. An account with two-factor authentication also needs
// a current code or a recovery code (secondFactor): none is core.ErrSecondFactorRequired, a
// wrong one core.ErrInvalidCode. The core revokes every core session itself; the website's own
// session rows and bearers are dropped here so none of them outlives the account until its
// next use.
func Delete(ctx context.Context, id core.Identity, db store.Store, accountID, password, secondFactor string) error {
	twoFactor, err := id.VerifyPasswordByID(ctx, accountID, password)
	if err != nil && !errors.Is(err, core.ErrSecondFactorRequired) {
		return err
	}
	if twoFactor {
		if strings.TrimSpace(secondFactor) == "" {
			return core.ErrSecondFactorRequired
		}
		code, recovery := SplitSecondFactor(secondFactor)
		if err := id.VerifySecondFactor(ctx, accountID, code, recovery); err != nil {
			return err
		}
	}
	if err := id.RequestDeletion(ctx, accountID); err != nil {
		return err
	}
	// Best effort: the core sessions behind these are already gone, so a row that
	// fails to delete here dies on its next use anyway.
	_ = db.DeleteWebSessionsForAccount(ctx, accountID)
	_ = db.RevokeAPITokensForAccount(ctx, accountID)
	return nil
}

// AccountByFriendCode resolves a Switch friend code as typed. The code the
// adapter published on the link (the core's public code) is asked first; the
// adapter itself second, because adapters do not publish codes yet; and last
// the link subject, for someone who pastes that verbatim. "" is nobody.
func AccountByFriendCode(ctx context.Context, links core.Links, ad *adapter.Client, typed, subject string) string {
	if id, err := links.AccountByPublicCode(ctx, "switch", typed); err == nil && id != "" {
		return id
	}
	if id := ad.AccountByFriendCode(ctx, typed); id != "" {
		return id
	}
	if subject == "" {
		return ""
	}
	if id, err := links.AccountBySubject(ctx, "switch", subject); err == nil {
		return id
	}
	return ""
}

// Friend-code kinds a request can name. An empty kind is the old form and
// the old API: an OpenPak code if it has that shape, else a Switch code.
const (
	CodeOpenPak = "openpak"
	CodeSwitch  = "switch"
	Code3DS     = "3ds"
)

// ResolveFriendCode finds the account behind a code of the given kind; ""
// when nobody has it.
func ResolveFriendCode(ctx context.Context, codes store.OpenPakCodeStore, links core.Links, ad *adapter.Client, kind, typed, switchSubject string) string {
	if kind == CodeOpenPak || (kind == "" && store.NormalizeOpenPakCode(typed) != "") {
		id, _ := codes.AccountByOpenPakCode(ctx, typed)
		return id
	}
	if kind == Code3DS {
		if pid, ok := PIDFrom3DSCode(typed); ok {
			if id, err := links.AccountBySubject(ctx, "3ds", strconv.FormatInt(pid, 10)); err == nil {
				return id
			}
		}
		return ""
	}
	return AccountByFriendCode(ctx, links, ad, typed, switchSubject)
}

// FriendCode3DS is the 3DS friend code for a principal id. It is
// nn-account's FriendCodeForPID (a port of the upstream derivation) kept
// byte-for-byte, so the code the site shows is the one the console shows: the
// checksum's hex digits, then the id's, read back as one number.
func FriendCode3DS(pid int64) string {
	sum := sha1.Sum([]byte{byte(pid), byte(pid >> 8), byte(pid >> 16), byte(pid >> 24)})
	v, _ := strconv.ParseInt(strconv.FormatInt(int64(sum[0]>>1), 16)+strconv.FormatInt(pid, 16), 16, 64)
	d := fmt.Sprintf("%012d", v)
	return d[0:4] + "-" + d[4:8] + "-" + d[8:12]
}

// PIDFrom3DSCode undoes FriendCode3DS. The id's hex width is not fixed, so
// each width is tried and kept only if its checksum matches: a mistyped code
// finds nobody instead of a stranger.
func PIDFrom3DSCode(typed string) (int64, bool) {
	digits := strings.NewReplacer("-", "", " ", "").Replace(typed)
	v, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || len(digits) != 12 {
		return 0, false
	}
	want := digits[0:4] + "-" + digits[4:8] + "-" + digits[8:12]
	for w := 8; w >= 1; w-- {
		pid := v & (1<<(4*w) - 1)
		if pid > 0 && FriendCode3DS(pid) == want {
			return pid, true
		}
	}
	return 0, false
}

// Code is one way to be added as a friend: its kind ("openpak", "switch",
// "wiiu", "3ds"), the console it works on, a label and the code itself.
type Code struct {
	Kind    string `json:"kind"`
	Console string `json:"console"`
	Label   string `json:"label"`
	Code    string `json:"code"`
}

// FriendCodes is every code the account can be added by, in the order the
// profile shows them. The website's banner and the API's /me both read this,
// so the app and the site never list different codes.
func FriendCodes(ctx context.Context, codes store.OpenPakCodeStore, links core.Links, ad *adapter.Client, accountID string) []Code {
	var out []Code
	// The OpenPak code first: it is the one that works for everybody.
	if c, err := codes.OpenPakCode(ctx, accountID); err == nil {
		out = append(out, Code{CodeOpenPak, "OpenPak", "OpenPak", c})
	}
	if link, err := links.ActiveLinkByAccount(ctx, "switch", accountID); err == nil {
		if c := FriendCode(ctx, link, ad, accountID); c != "" {
			out = append(out, Code{CodeSwitch, "Switch", "Switch", c})
		}
	}
	// Wii U friends add each other by Nintendo Network ID, which nn-account
	// owns; it appears once nn-account publishes it on the link.
	if link, err := links.ActiveLinkByAccount(ctx, "wiiu", accountID); err == nil && link.PublicCode != "" {
		out = append(out, Code{"wiiu", "Wii U", "Wii U · NNID", link.PublicCode})
	}
	// A 3DS code is derived from the console's principal id, the link's
	// subject, so it needs no call to nn-account.
	if link, err := links.ActiveLinkByAccount(ctx, "3ds", accountID); err == nil {
		c := link.PublicCode
		if pid, err := strconv.ParseInt(link.SubjectID, 10, 64); c == "" && err == nil {
			c = FriendCode3DS(pid)
		}
		if c != "" {
			out = append(out, Code{Code3DS, "3DS", "3DS", c})
		}
	}
	// Wii and DS have none: a Wii friend code belongs to a game, not the console.
	return out
}

// FriendCode is the account's Switch friend code for display: the one the
// adapter published on the link, else the adapter's answer. "" is none.
func FriendCode(ctx context.Context, link core.Link, ad *adapter.Client, accountID string) string {
	if link.PublicCode != "" {
		return link.PublicCode
	}
	return ad.FriendCode(ctx, accountID)
}

// SplitSecondFactor tells a typed code apart: six digits (spaces allowed) are
// an authenticator code, anything else a recovery code ("xxxxx-xxxxx"). One
// field is simpler for a person than two, and the shapes never overlap.
func SplitSecondFactor(typed string) (code, recoveryCode string) {
	digits := strings.ReplaceAll(strings.TrimSpace(typed), " ", "")
	if len(digits) == 6 && strings.Trim(digits, "0123456789") == "" {
		return digits, ""
	}
	return "", strings.TrimSpace(typed)
}
