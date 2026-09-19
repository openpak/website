// Package accounts holds the account actions the website and the mobile API
// both perform. Each surface keeps its own request parsing and its own way of
// answering; what lives here is the part that has to be the same on both, so
// the two cannot drift apart.
package accounts

import (
	"context"
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
