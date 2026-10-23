package main

// `openpak-policy ceiling` — the owner's side of the signed redirect ceiling
// (docs/signed-ceiling.md). It runs on the owner's machine, where the
// Ed25519 private key lives; the box never sees the key and only serves the
// envelope this writes. Every edit starts from the current verified
// envelope, bumps version, stamps issued, validates, signs, writes, and
// prints the scp line that installs it.

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/openpak/website/internal/netprofile"
)

const (
	ceilingURL        = "https://openpak.org/api/v1/network/ceiling"
	ceilingDefaultKey = "~/REPOS/Openpak/.secrets/ceiling/ceiling-signing.pem"
	ceilingBoxTarget  = "<prod-host>:~/openpak/deploy/netprofile-ceiling.json"
)

// ceilingKeys is what an envelope is verified under, and what a signing key
// must be one of; a var so the tests pin a throwaway key.
var ceilingKeys = netprofile.PinnedKeys

// ceilingNow stamps issued; a var so the tests are deterministic.
var ceilingNow = func() time.Time { return time.Now().UTC() }

func cmdCeiling(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("ceiling: want show, add, remove or sign")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("ceiling "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := fs.String("file", "", "read the current envelope from this file instead of fetching it")
	url := fs.String("url", ceilingURL, "where the current envelope is fetched from")
	keyPath := fs.String("key", ceilingDefaultKey, "PKCS#8 PEM Ed25519 signing key")
	out := fs.String("out", "", "where the signed envelope is written (default netprofile-ceiling.v<N>.json)")
	in := fs.String("in", "", "sign: the payload JSON to sign")
	noCurrent := fs.Bool("no-current", false, "sign: there is no current envelope (the first signing)")

	// Positionals may come before or after the flags.
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("ceiling %s: %w", sub, err)
	}
	var pos []string
	for fs.NArg() > 0 {
		pos = append(pos, fs.Arg(0))
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return fmt.Errorf("ceiling %s: %w", sub, err)
		}
	}
	if strings.HasPrefix(*keyPath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			*keyPath = filepath.Join(home, (*keyPath)[2:])
		}
	}

	current := func() (*netprofile.Ceiling, error) {
		body, err := readEnvelope(*file, *url)
		if err != nil {
			return nil, err
		}
		c, err := netprofile.VerifyCeiling(body, ceilingKeys)
		if err != nil {
			return nil, fmt.Errorf("current envelope does not verify: %w", err)
		}
		return c, nil
	}

	switch sub {
	case "show":
		if len(pos) != 0 {
			return errors.New("ceiling show takes no arguments")
		}
		c, err := current()
		if err != nil {
			return err
		}
		printCeiling(stdout, c)
		return nil

	case "add", "remove":
		if len(pos) != 2 {
			return fmt.Errorf("usage: openpak-policy ceiling %s <platform> <.family>", sub)
		}
		platform, family := pos[0], pos[1]
		if !slices.Contains(netprofile.PlatformsAllowed, platform) {
			return fmt.Errorf("platform %q is not one of %s", platform, strings.Join(netprofile.PlatformsAllowed, ", "))
		}
		if err := netprofile.ValidFamily(family); err != nil {
			return err
		}
		c, err := current()
		if err != nil {
			return err
		}
		fams := slices.Clone(c.Platforms[platform])
		i := slices.Index(fams, family)
		if sub == "add" {
			if i >= 0 {
				return fmt.Errorf("%s already lists %s (version %d)", platform, family, c.Version)
			}
			fams = append(fams, family)
		} else {
			if i < 0 {
				return fmt.Errorf("%s does not list %s (version %d)", platform, family, c.Version)
			}
			fams = slices.Delete(fams, i, i+1)
		}
		next := cloneCeiling(c)
		if len(fams) == 0 {
			delete(next.Platforms, platform)
		} else {
			next.Platforms[platform] = fams
		}
		next.Version = c.Version + 1
		return signAndWrite(next, *keyPath, *out, stdout)

	case "sign":
		if len(pos) != 0 || *in == "" {
			return errors.New("usage: openpak-policy ceiling sign --in payload.json [--no-current]")
		}
		raw, err := os.ReadFile(*in)
		if err != nil {
			return err
		}
		var next netprofile.Ceiling
		if err := json.Unmarshal(raw, &next); err != nil {
			return fmt.Errorf("%s: %w", *in, err)
		}
		if next.Type == "" {
			next.Type = netprofile.CeilingType
		}
		floor := 1
		if !*noCurrent {
			c, err := current()
			if err != nil {
				return fmt.Errorf("%w (pass --no-current only for the very first signing)", err)
			}
			floor = c.Version + 1
		}
		next.Version = max(next.Version, floor)
		return signAndWrite(&next, *keyPath, *out, stdout)
	}
	return fmt.Errorf("ceiling: unknown subcommand %q (show, add, remove, sign)", sub)
}

func readEnvelope(file, url string) ([]byte, error) {
	if file != "" {
		return os.ReadFile(file)
	}
	// Normal public TLS trust, the pinned origin: the same bootstrap rule
	// clients follow.
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return body, nil
}

func cloneCeiling(c *netprofile.Ceiling) *netprofile.Ceiling {
	n := &netprofile.Ceiling{Type: c.Type, Version: c.Version, Issued: c.Issued, Platforms: map[string][]string{}}
	for p, f := range c.Platforms {
		n.Platforms[p] = slices.Clone(f)
	}
	return n
}

func loadSigningKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s: not PEM", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 key", path)
	}
	return priv, nil
}

func signAndWrite(c *netprofile.Ceiling, keyPath, out string, stdout io.Writer) error {
	c.Issued = ceilingNow().Format(time.RFC3339)
	if err := c.Validate(); err != nil {
		return err
	}
	key, err := loadSigningKey(keyPath)
	if err != nil {
		return err
	}
	// A signature under a key no client pins is a file every client rejects.
	id := netprofile.KeyID(key.Public().(ed25519.PublicKey))
	if !slices.ContainsFunc(ceilingKeys, func(k netprofile.PinnedKey) bool { return k.KeyID == id }) {
		return fmt.Errorf("signing key %s is not a pinned ceiling key", id)
	}
	env, err := netprofile.SignCeiling(c, key)
	if err != nil {
		return err
	}
	// Round-trip through the verifier the server and clients use.
	if _, err := netprofile.VerifyCeiling(env, ceilingKeys); err != nil {
		return fmt.Errorf("signed envelope does not verify: %w", err)
	}
	if out == "" {
		out = fmt.Sprintf("netprofile-ceiling.v%d.json", c.Version)
	}
	// The envelope is public; 0644 so the website container can read the
	// copy on the box.
	if err := os.WriteFile(out, env, 0o644); err != nil {
		return err
	}
	printCeiling(stdout, c)
	fmt.Fprintf(stdout, "\nwrote %s\ninstall it with:\n  scp %s %s\n", out, out, ceilingBoxTarget)
	return nil
}

func printCeiling(w io.Writer, c *netprofile.Ceiling) {
	fmt.Fprintf(w, "ceiling version %d, issued %s\n", c.Version, c.Issued)
	for _, p := range netprofile.PlatformsAllowed {
		if fams, ok := c.Platforms[p]; ok {
			fams = slices.Sorted(slices.Values(fams))
			fmt.Fprintf(w, "  %-6s %s\n", p, strings.Join(fams, " "))
		}
	}
}
