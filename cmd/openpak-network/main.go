// openpak-network is the CI-facing fetch tool of build-time integration
// (PRD emulators/prds/universal-tls-forwarding/03). One command authenticates the release
// job, one fetches and verifies the pinned bundle, one re-verifies a
// fetched directory offline. Day-to-day input is the standard variable
// contract; certification is never inferred from a variable being set.
//
//	openpak-network authenticate --token-output "$RUNNER_TEMP/openpak-token"
//	openpak-network fetch --output build/openpak
//	openpak-network verify --bundle-dir build/openpak
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openpak/website/internal/fetchtool"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "authenticate":
		err = cmdAuthenticate(rest, stderr)
	case "fetch":
		err = cmdFetch(rest, stdout)
	case "verify":
		err = cmdVerify(rest, stdout)
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "openpak-network: unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
	if err != nil {
		if errors.Is(err, fetchtool.ErrDisabled) {
			// The supported local configuration: a build that asked for no
			// networking got exactly that. This is success.
			fmt.Fprintln(stdout, "openpak-network: networking disabled; no bundle fetched")
			return 0
		}
		fmt.Fprintf(stderr, "openpak-network: %v\n", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  openpak-network authenticate --token-output PATH [--audience AUD] [--token TOKEN]
  openpak-network fetch [--output DIR]
  openpak-network verify --bundle-dir DIR

Configuration comes from the environment:
  OPENPAK_NETWORK_MODE      disabled (default) | development | certified
  OPENPAK_CLIENT_HASH       public registration identifier
  OPENPAK_PLATFORM          switch | wiiu | 3ds | wii | ds
  OPENPAK_CHANNEL           stable (default) | staging
  OPENPAK_BUNDLE_SHA256     optional exact content pin
  OPENPAK_BUILD_TOKEN_FILE  path to the CI bearer credential
  OPENPAK_BUNDLE_DIR        output directory, exposed atomically
  OPENPAK_ROOT_FILE         pinned trust root the tool verifies against
  OPENPAK_REGISTRY_URL      registry endpoint (development mode only)
`)
}

// cmdAuthenticate turns the job's CI identity into the build credential the
// fetch step presents (02 AUTH-001/002). Inside GitHub Actions the identity
// is the workflow's own OIDC token, requested for the audience the registry
// expects; outside it, an explicitly passed --token serves staging
// credentials. Either way the identity is exchanged at the registry for a
// short-lived build credential — the CI identity itself never becomes the
// fetch bearer — and only that credential lands in one 0600 file.
func cmdAuthenticate(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("authenticate", flag.ExitOnError)
	out := fs.String("token-output", "", "path to write the bearer credential (required)")
	audience := fs.String("audience", "", "OIDC audience to request (Actions only)")
	token := fs.String("token", "", "literal credential for staging; never production")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("--token-output is required")
	}
	value := *token
	if value == "" {
		reqURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
		reqToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
		if reqURL == "" || reqToken == "" {
			return fmt.Errorf("no ACTIONS_ID_TOKEN_REQUEST_URL/TOKEN in the environment; run inside the release workflow, or pass --token for staging")
		}
		separator := "?"
		if strings.Contains(reqURL, "?") {
			separator = "&"
		}
		url := reqURL + separator + "audience=" + *audience
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+reqToken)
		client := &http.Client{Timeout: 10 * time.Second}
		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("actions token service: %w", err)
		}
		defer res.Body.Close()
		var doc struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&doc); err != nil || doc.Value == "" {
			return fmt.Errorf("actions token service answered %d", res.StatusCode)
		}
		value = doc.Value
	}
	// The exchange: CI identity in, build credential out. Mode and identity
	// come from the same variable contract fetch enforces; certified mode
	// exchanges only at the pinned registry.
	buildToken, err := fetchtool.Exchange(context.Background(), nil,
		strings.ToLower(strings.TrimSpace(os.Getenv("OPENPAK_NETWORK_MODE"))),
		os.Getenv("OPENPAK_REGISTRY_URL"),
		value,
		os.Getenv("OPENPAK_CLIENT_HASH"),
		os.Getenv("OPENPAK_PLATFORM"),
		os.Getenv("OPENPAK_CHANNEL"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	// 0600: the credential file is briefly on disk by design; scans and
	// accidental prints are the failure modes the mode and file mode contain.
	return os.WriteFile(*out, []byte(buildToken+"\n"), 0o600)
}

func cmdFetch(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	output := fs.String("output", "", "bundle output directory (overrides OPENPAK_BUNDLE_DIR)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := fetchtool.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	if *output != "" {
		cfg.Output = *output
	}
	if cfg.Mode != fetchtool.ModeDisabled && cfg.Output == "" {
		return fmt.Errorf("fetch needs --output or OPENPAK_BUNDLE_DIR")
	}
	tool, err := fetchtool.New(cfg)
	if err != nil {
		return err
	}
	lock, err := tool.Fetch(context.Background())
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "bundle %s\nplatform %s channel %s sequence %d\noutput %s\nlockfile %s\n",
		lock.BundleSHA256, lock.Platform, lock.Channel, lock.Sequence, cfg.Output,
		filepath.Join(cfg.Output, fetchtool.LockfileName))
	return nil
}

func cmdVerify(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	dir := fs.String("bundle-dir", "", "fetched bundle directory (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := fetchtool.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("--bundle-dir is required")
	}
	// Verify runs even where fetch was disabled: an offline audit of a
	// directory someone else fetched needs only the pinned root.
	tool, err := fetchtool.New(fetchtool.Config{
		Mode:        fetchtool.ModeDevelopment,
		RegistryURL: "http://localhost", // never contacted by verify
		TokenFile:   cfg.TokenFile,
		Output:      cfg.Output,
		RootFile:    cfg.RootFile,
	})
	if err != nil {
		return err
	}
	lock, err := tool.Verify(context.Background(), *dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "verified: bundle %s sequence %d policy %s\n",
		lock.BundleSHA256, lock.Sequence, lock.CanonicalPolicySHA256)
	return nil
}
