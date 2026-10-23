package erasure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrModsUnavailable is the mods step's answer when the service is not
// configured: the account waits, closed, until it is.
var ErrModsUnavailable = errors.New("mods service not configured")

// ModsStep asks the mods service to erase an account (DELETE
// /internal/accounts/{a}/mods, bearer MODS_INTERNAL_KEY). The service keeps
// published mods, credited to "deleted user", and deletes unpublished
// submissions and favourites; it is idempotent, so a retry is harmless.
func ModsStep(baseURL, key string, hc *http.Client) Step {
	baseURL = strings.TrimRight(baseURL, "/")
	if hc == nil {
		hc = http.DefaultClient
	}
	return Step{Name: "mods", Erase: func(ctx context.Context, id string) error {
		if baseURL == "" || key == "" {
			return ErrModsUnavailable
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+"/internal/accounts/"+url.PathEscape(id)+"/mods", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("mods service: %s: %s", resp.Status, strings.TrimSpace(string(detail)))
		}
		return nil
	}}
}
