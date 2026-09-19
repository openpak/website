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
// password is core.ErrInvalidCredentials. The core revokes every core session itself; the
// website's own session rows and bearers are dropped here so none of them outlives the account
// until its next use.
func Delete(ctx context.Context, id core.Identity, db store.Store, accountID, password string) error {
	if err := id.VerifyPasswordByID(ctx, accountID, password); err != nil {
		return err
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
