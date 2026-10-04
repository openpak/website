// openpak-policy is the operator console for the v2 network policy registry
// (PRD emulators/prds/universal-tls-forwarding/01 POL-M1/G1 and 02 AUTH-M0). The
// publication pipeline is a library; this command is the reviewed path that
// calls it: publish a canonical revision's projections, roll a channel back
// to its last good content, withdraw poisoned bytes, inspect a channel, and
// manage certified client registrations. It runs where the REGISTRY_*
// configuration is valid and never invents host data: every hostname comes
// from the pinned platform map.
//
//	openpak-policy publish --map platforms.yml --ca ca.pem \
//	    --ceiling switch=unified-1 [--platform switch] [--no-promote]
//	openpak-policy rollback --platform switch --channel stable
//	openpak-policy withdraw --bundle-sha256 <hex>
//	openpak-policy status --platform switch --channel stable
//	openpak-policy register --repository openpak/citron --kind emulator \
//	    --platform switch --channel staging --workflow .github/workflows/release.yml
//	openpak-policy client-status --client-id <id> --status suspended
//	openpak-policy ceiling show|add|remove|sign   (signed redirect ceiling)
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/openpak/website/internal/netprofile"
	"github.com/openpak/website/internal/policy"
	"github.com/openpak/website/internal/registry"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// newService builds the registry service from REGISTRY_* configuration; a
// var so the tests drive the same command paths against an in-memory
// service.
var newService = openService

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "publish":
		err = cmdPublish(rest, getenv, stdout)
	case "refresh":
		err = cmdRefresh(rest, getenv, stdout)
	case "rollback":
		err = cmdRollback(rest, getenv, stdout)
	case "withdraw":
		err = cmdWithdraw(rest, getenv, stdout)
	case "status":
		err = cmdStatus(rest, getenv, stdout)
	case "keygen":
		err = cmdKeygen(rest, stdout)
	case "provision-root":
		err = cmdProvisionRoot(rest, stdout)
	case "root":
		err = cmdRoot(rest, getenv, stdout)
	case "register":
		err = cmdRegister(rest, getenv, stdout)
	case "client-status":
		err = cmdClientStatus(rest, getenv, stdout)
	case "ceiling":
		err = cmdCeiling(rest, stdout)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "openpak-policy: unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "openpak-policy: %v\n", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  openpak-policy publish --map PATH [--transports PATH] [--ca PATH] [--ca-id ID]
                         [--ceiling PLATFORM=REV]... [--platform NAME]...
                         [--channel stable] [--promote=true]
  openpak-policy rollback --platform NAME --channel NAME
  openpak-policy refresh [--older-than 72h]
  openpak-policy withdraw --bundle-sha256 HEX
  openpak-policy status --platform NAME --channel NAME
  openpak-policy keygen --root PATH --online PATH [--root-metadata PATH]
  openpak-policy provision-root --root-key PATH --online-key PATH --out PATH
  openpak-policy root --platform NAME --channel NAME [--out PATH]
  openpak-policy register --repository OWNER/REPO --kind emulator|setup|service
                          --platform NAME... --channel NAME...
                          [--workflow PATH]... [--status requested]
                          [--environment production] [--client-id ID]
  openpak-policy client-status --client-id ID --status STATUS
  openpak-policy ceiling show [--file PATH | --url URL]
  openpak-policy ceiling add|remove PLATFORM .FAMILY [--file PATH | --url URL]
                         [--key PEM] [--out PATH]
  openpak-policy ceiling sign --in PAYLOAD.json [--no-current]
                         [--file PATH | --url URL] [--key PEM] [--out PATH]

The ceiling commands (docs/signed-ceiling.md) run on the owner's machine with
the Ed25519 key (default `+ceilingDefaultKey+`); they need no REGISTRY_*
configuration and print the scp line that installs the envelope on the box.

Configuration comes from the same REGISTRY_* environment the website uses:
  REGISTRY_DATABASE_URL       Postgres connection for the registry store
  REGISTRY_BLOB_DIR           private content-addressed blob directory
  REGISTRY_ONLINE_KEY_FILE    online signing ed25519 seed file, 0600
  REGISTRY_ROOT_METADATA_FILE provisioned root.json; the bytes every client pins
  REGISTRY_POLICY_ID          policy id (default openpak-production)
`)
}

// openService wires the production shape: a Postgres-backed store, the
// private blob directory and the two signing keys. Anything missing is a
// hard operator error, not a degraded mode: an operator command that
// "silently" did nothing would be worse than one that refuses to start.
func openService(getenv func(string) string) (*registry.Service, func(), error) {
	var missing []string
	for _, name := range []string{"REGISTRY_DATABASE_URL", "REGISTRY_BLOB_DIR", "REGISTRY_ONLINE_KEY_FILE", "REGISTRY_ROOT_METADATA_FILE"} {
		if getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, func() {}, fmt.Errorf("registry configuration missing: %s", strings.Join(missing, ", "))
	}
	store, err := registry.OpenPG(context.Background(), getenv("REGISTRY_DATABASE_URL"))
	if err != nil {
		return nil, func() {}, fmt.Errorf("open registry store: %w", err)
	}
	fail := func(err error) (*registry.Service, func(), error) {
		store.Close()
		return nil, func() {}, err
	}
	blobs, err := registry.NewDirBlobs(getenv("REGISTRY_BLOB_DIR"))
	if err != nil {
		return fail(err)
	}
	// The root seed is deliberately not loaded here: the offline root
	// provisions its metadata once, and the publication host works from
	// those bytes plus the online key (POL-006).
	onlineKey, err := registry.LoadEd25519(getenv("REGISTRY_ONLINE_KEY_FILE"))
	if err != nil {
		return fail(err)
	}
	rootBytes, err := os.ReadFile(getenv("REGISTRY_ROOT_METADATA_FILE"))
	if err != nil {
		return fail(fmt.Errorf("read provisioned root metadata: %w", err))
	}
	publisher, err := registry.NewPublisherWithRoot(rootBytes, onlineKey)
	if err != nil {
		return fail(err)
	}
	policyID := getenv("REGISTRY_POLICY_ID")
	if policyID == "" {
		policyID = "openpak-production"
	}
	return &registry.Service{
		Store:     store,
		Blobs:     blobs,
		Publisher: publisher,
		PolicyID:  policyID,
	}, func() { store.Close() }, nil
}

type listFlags []string

func (f *listFlags) String() string { return strings.Join(*f, ", ") }
func (f *listFlags) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// envFor mirrors the publication service's channel rule: staging publishes
// under the nonproduction environment, everything else is production.
func envFor(channel string) policy.Environment {
	if channel == "staging" {
		return policy.EnvNonproduction
	}
	return policy.EnvProduction
}

// cmdPublish builds one canonical revision from the pinned inputs and
// publishes its projections. One invocation is one policy change: every
// requested platform projects from the same canonical bytes, so their
// canonical_policy_sha256 agree by construction.
func cmdPublish(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	mapPath := fs.String("map", "", "platform map YAML, the only authored host input (required)")
	transportsPath := fs.String("transports", "", "reviewed transport declarations JSON: platform → name → {transport, ports}")
	caPath := fs.String("ca", "", "CA PEM to distribute as the scoped trust artifact; omit for a TLS-less revision")
	caID := fs.String("ca-id", "openpak-ca-2", "trust id recorded for the CA")
	var ceilings, platforms listFlags
	fs.Var(&ceilings, "ceiling", "approved ceiling revision per platform, PLATFORM=REV (repeatable; required for every published platform)")
	fs.Var(&platforms, "platform", "platform to publish (repeatable; default: every platform in the map)")
	channel := fs.String("channel", "stable", "channel to publish into")
	promote := fs.Bool("promote", true, "move the channel pointer atomically after the publication verifies; false stages without promoting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mapPath == "" {
		return errors.New("--map is required; generation never invents host data")
	}
	mapData, err := os.ReadFile(*mapPath)
	if err != nil {
		return err
	}
	m, err := netprofile.LoadMap(mapData)
	if err != nil {
		return err
	}
	var transports map[string]map[string]policy.TransportSpec
	if *transportsPath != "" {
		raw, err := os.ReadFile(*transportsPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &transports); err != nil {
			return fmt.Errorf("transport declarations: %w", err)
		}
	}
	var ca *policy.CA
	if *caPath != "" {
		pemBytes, err := os.ReadFile(*caPath)
		if err != nil {
			return err
		}
		notAfter, err := pemNotAfter(pemBytes)
		if err != nil {
			return err
		}
		ca = &policy.CA{ID: *caID, PEM: pemBytes, NotAfter: notAfter}
	}
	ceilingMap := map[string]string{}
	for _, c := range ceilings {
		platform, rev, found := strings.Cut(c, "=")
		if !found || platform == "" || rev == "" {
			return fmt.Errorf("--ceiling %q is not PLATFORM=REV", c)
		}
		ceilingMap[platform] = rev
	}
	svc, closer, err := newService(getenv)
	if err != nil {
		return err
	}
	defer closer()

	canonical, err := policy.Build(policy.Inputs{
		PolicyID:         svc.PolicyID,
		Map:              m,
		CA:               ca,
		CeilingRevisions: ceilingMap,
		TransportMeta:    transports,
	}, envFor(*channel))
	if err != nil {
		return err
	}
	names := platforms
	if len(names) == 0 {
		for name := range canonical.Platforms {
			names = append(names, name)
		}
		sortStrings(names)
	}
	var artifacts []policy.PayloadInput
	if ca != nil {
		artifacts = append(artifacts, policy.PayloadInput{
			Path:      policy.TrustArtifactPath,
			MediaType: policy.MediaTypePEM,
			Bytes:     ca.PEM,
		})
	}
	digest, err := canonical.Digest()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "canonical revision sha256:%s platforms %s channel %s\n",
		digest, strings.Join(names, ","), *channel)
	ctx := context.Background()
	for _, platform := range names {
		res, err := svc.Publish(ctx, registry.PublishInput{
			Canonical: canonical,
			Platform:  platform,
			Channel:   *channel,
			Artifacts: artifacts,
			Promote:   *promote,
		})
		if err != nil {
			return fmt.Errorf("publish %s: %w", platform, err)
		}
		fmt.Fprintf(stdout, "%s %s: sequence %d bundle sha256:%s promoted=%v\n",
			platform, *channel, res.Sequence, res.Revision.BundleSHA256, res.Promoted)
	}
	return nil
}

// cmdRollback republishes the channel's last good content under a higher
// sequence (POL-007) — the sanctioned answer to a bad policy.
func cmdRollback(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	platform := fs.String("platform", "", "platform to roll back (required)")
	channel := fs.String("channel", "stable", "channel to roll back")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *platform == "" {
		return errors.New("--platform is required")
	}
	svc, closer, err := newService(getenv)
	if err != nil {
		return err
	}
	defer closer()
	res, err := svc.Rollback(context.Background(), *platform, *channel)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s %s: rolled back to content sha256:%s as sequence %d\n",
		*platform, *channel, res.Revision.BundleSHA256, res.Sequence)
	return nil
}

// cmdRefresh re-signs every channel's current content whose last publication is older than
// --older-than: the same content at a higher sequence, exactly what rollback does. The TUF
// timestamp lives 7 days (registry timestampTTL) and only a publication renews it; nothing
// else does, so the box runs this daily (openpak-registry-refresh.timer). Without it every
// certified fetch fails "timestamp metadata expired" a week after the last policy change.
func cmdRefresh(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("refresh", flag.ExitOnError)
	olderThan := fs.Duration("older-than", 72*time.Hour, "re-sign channels last published longer ago than this")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, closer, err := newService(getenv)
	if err != nil {
		return err
	}
	defer closer()
	ctx := context.Background()
	channels, err := svc.Store.ListChannels(ctx)
	if err != nil {
		return err
	}
	var failed []string
	for _, c := range channels {
		if time.Since(c.UpdatedAt) < *olderThan {
			continue
		}
		res, err := svc.Rollback(ctx, c.Platform, c.Channel)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s/%s: %v", c.Platform, c.Channel, err))
			continue
		}
		fmt.Fprintf(stdout, "%s %s: re-signed content sha256:%s as sequence %d\n",
			c.Platform, c.Channel, res.Revision.BundleSHA256, res.Sequence)
	}
	if len(failed) > 0 {
		return fmt.Errorf("refresh failed for %s", strings.Join(failed, "; "))
	}
	return nil
}

// cmdWithdraw marks a bundle's bytes dead: resolution answers 410 from now
// on, the bytes stay for audit, and recovery is a fresh higher sequence.
func cmdWithdraw(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("withdraw", flag.ExitOnError)
	bundle := fs.String("bundle-sha256", "", "bundle digest to withdraw (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !isSHA256Hex(*bundle) {
		return errors.New("--bundle-sha256 must be a lowercase sha-256 hex digest")
	}
	svc, closer, err := newService(getenv)
	if err != nil {
		return err
	}
	defer closer()
	if err := svc.Store.Withdraw(context.Background(), *bundle); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "withdrawn %s: resolution answers 410; recovery is a fresh higher-sequence publication\n", *bundle)
	return nil
}

// cmdStatus shows what a channel answers with right now.
func cmdStatus(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	platform := fs.String("platform", "", "platform (required)")
	channel := fs.String("channel", "stable", "channel")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *platform == "" {
		return errors.New("--platform is required")
	}
	svc, closer, err := newService(getenv)
	if err != nil {
		return err
	}
	defer closer()
	ctx := context.Background()
	cur, err := svc.Store.Channel(ctx, *platform, *channel)
	if err != nil {
		return fmt.Errorf("channel %s %s has no pointer: %w", *platform, *channel, err)
	}
	rev, err := svc.ResolveChannel(ctx, *platform, *channel)
	if err != nil {
		return fmt.Errorf("channel %s %s does not resolve (withdrawn?): %w", *platform, *channel, err)
	}
	fmt.Fprintf(stdout, "%s %s: sequence %d bundle sha256:%s canonical sha256:%s issued %s\n",
		*platform, *channel, rev.Sequence, rev.BundleSHA256, rev.CanonicalPolicySHA256, rev.IssuedAt.UTC().Format(time.RFC3339))
	if cur.Sequence != rev.Sequence {
		fmt.Fprintf(stdout, "note: pointer records sequence %d while the revision carries %d\n", cur.Sequence, rev.Sequence)
	}
	return nil
}

// cmdKeygen provisions the two signing seeds of one publication environment:
// an offline root that signs nothing but root metadata, and the online key
// targets/snapshot/timestamp use. Both land 0600, and neither is ever
// committed. The root seed is the disaster-recovery secret: keep it off the
// publication host where the operating model allows.
func cmdKeygen(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	root := fs.String("root", "", "path for the offline root seed (required)")
	online := fs.String("online", "", "path for the online signing seed (required)")
	rootMetadata := fs.String("root-metadata", "", "write the provisioned root.json here; production wiring loads these bytes (REGISTRY_ROOT_METADATA_FILE)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *root == "" || *online == "" {
		return errors.New("--root and --online are required; the keys are generated, never derived")
	}
	if *root == *online {
		return errors.New("the root and online keys must be distinct files")
	}
	rootKey, err := registry.GenerateEd25519()
	if err != nil {
		return err
	}
	onlineKey, err := registry.GenerateEd25519()
	if err != nil {
		return err
	}
	if err := registry.SaveEd25519(*root, rootKey); err != nil {
		return err
	}
	if err := registry.SaveEd25519(*online, onlineKey); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "generated %s (offline root; signs root metadata only)\ngenerated %s (online; signs targets/snapshot/timestamp)\nboth files are 0600 hex ed25519 seeds — back the root seed up somewhere off this host\n", *root, *online)
	if *rootMetadata != "" {
		if err := writeProvisionedRoot(*rootMetadata, rootKey, onlineKey, stdout); err != nil {
			return err
		}
	}
	return nil
}

// writeProvisionedRoot signs the root metadata once and writes it: the
// bytes a client repository pins must be the same for the root key's whole
// life, never re-minted with a fresh expiry at every restart (POL-006).
func writeProvisionedRoot(path string, rootKey, onlineKey ed25519.PrivateKey, stdout io.Writer) error {
	data, err := registry.ProvisionRoot(rootKey, onlineKey, time.Now())
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "provisioned %s — REGISTRY_ROOT_METADATA_FILE points here; commit copies to client repositories\n", path)
	return nil
}

// cmdProvisionRoot derives the root metadata from keys that already exist,
// for deployments provisioned before the root file existed.
func cmdProvisionRoot(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("provision-root", flag.ExitOnError)
	rootKeyFile := fs.String("root-key", "", "existing offline root seed file (required)")
	onlineKeyFile := fs.String("online-key", "", "existing online seed file (required)")
	out := fs.String("out", "", "path to write the provisioned root.json (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *rootKeyFile == "" || *onlineKeyFile == "" || *out == "" {
		return errors.New("--root-key, --online-key and --out are required")
	}
	rootKey, err := registry.LoadEd25519(*rootKeyFile)
	if err != nil {
		return err
	}
	onlineKey, err := registry.LoadEd25519(*onlineKeyFile)
	if err != nil {
		return err
	}
	return writeProvisionedRoot(*out, rootKey, onlineKey, stdout)
}

// cmdRoot writes out the pinned trust root of one channel: the root.json
// bytes the registry recorded at its first publication. This is the file a
// client repository commits and the fetch tool pins against (OPENPAK_ROOT_FILE)
// — public trust material, not a secret.
func cmdRoot(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("root", flag.ExitOnError)
	platform := fs.String("platform", "", "platform of the revision to read (required)")
	channel := fs.String("channel", "stable", "channel")
	out := fs.String("out", "", "write the root to this path instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *platform == "" {
		return errors.New("--platform is required")
	}
	svc, closer, err := newService(getenv)
	if err != nil {
		return err
	}
	defer closer()
	ctx := context.Background()
	rev, err := svc.ResolveChannel(ctx, *platform, *channel)
	if err != nil {
		return err
	}
	digest, ok := rev.Metadata["root.json"]
	if !ok {
		return errors.New("the revision records no root.json; it cannot pin a fetch tool")
	}
	data, err := svc.Blobs.Get(ctx, digest)
	if err != nil {
		return err
	}
	if *out == "" {
		_, err = stdout.Write(data)
		if !bytes.HasSuffix(data, []byte("\n")) {
			_, _ = stdout.Write([]byte("\n"))
		}
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s (%d bytes, digest %s)\n", *out, len(data), digest)
	return nil
}

// cmdRegister creates one certified client registration (02's identity
// model): the server issues the registration value, stores only its
// SHA-256, and hands the hash to the maintainer as OPENPAK_CLIENT_HASH.
func cmdRegister(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	repository := fs.String("repository", "", "immutable repository identity, owner/repo (required)")
	kind := fs.String("kind", "", "emulator | setup | service (required)")
	var platforms, channels, workflows listFlags
	fs.Var(&platforms, "platform", "permitted platform (repeatable, required)")
	fs.Var(&channels, "channel", "permitted channel (repeatable, required)")
	fs.Var(&workflows, "workflow", "approved CI workflow path (repeatable; required before production approval)")
	status := fs.String("status", registry.ClientRequested, "initial lifecycle status")
	environment := fs.String("environment", registry.EnvProduction, "production | nonproduction")
	clientID := fs.String("client-id", "", "explicit immutable id; default: generated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *repository == "" || !strings.Contains(*repository, "/") {
		return errors.New("--repository must be an owner/repo identity")
	}
	switch *kind {
	case "emulator", "setup", "service":
	default:
		return errors.New("--kind must be emulator, setup or service")
	}
	if len(platforms) == 0 || len(channels) == 0 {
		return errors.New("a registration scopes at least one --platform and one --channel")
	}
	for _, p := range platforms {
		if !contains(netprofile.PlatformsAllowed, p) {
			return fmt.Errorf("platform %q is not one the PRD names (%s)", p, strings.Join(netprofile.PlatformsAllowed, ", "))
		}
	}
	switch *status {
	case registry.ClientRequested, registry.ClientReviewing, registry.ClientStagingApproved, registry.ClientProductionApproved:
	case registry.ClientSuspended, registry.ClientRevoked:
		return fmt.Errorf("--status %q is a lifecycle transition, not an initial state", *status)
	default:
		return fmt.Errorf("--status %q is not a lifecycle state", *status)
	}
	if *status == registry.ClientProductionApproved && (len(workflows) == 0 || *environment != registry.EnvProduction) {
		return errors.New("a production-approved registration needs an approved --workflow and the production environment")
	}

	svc, closer, err := newService(getenv)
	if err != nil {
		return err
	}
	defer closer()

	value, err := randomRegistrationValue()
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(value))
	if *clientID == "" {
		if *clientID, err = randomID("client-"); err != nil {
			return err
		}
	}
	client := registry.Client{
		ID:          *clientID,
		ClientHash:  hex.EncodeToString(sum[:]),
		Repository:  *repository,
		Kind:        *kind,
		Status:      *status,
		Environment: *environment,
		Platforms:   platforms,
		Channels:    channels,
		Workflows:   workflows,
	}
	if err := svc.Store.PutClient(context.Background(), client); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "registered %s (%s) repository %s status %s environment %s platforms %s channels %s\n",
		client.ID, client.Kind, client.Repository, client.Status, client.Environment,
		strings.Join(platforms, ","), strings.Join(channels, ","))
	for _, w := range workflows {
		fmt.Fprintf(stdout, "approved workflow: %s\n", w)
	}
	fmt.Fprintf(stdout, "client hash (OPENPAK_CLIENT_HASH): %s\n", client.ClientHash)
	fmt.Fprintf(stdout, "registration value, shown once, never stored: %s\n", value)
	return nil
}

// cmdClientStatus moves one registration along its lifecycle (AUTH-004).
// Suspensions and revocations bump the revocation epoch, which kills every
// build session issued before it on its next request.
func cmdClientStatus(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("client-status", flag.ExitOnError)
	clientID := fs.String("client-id", "", "registration id (required)")
	status := fs.String("status", "", "requested | reviewing | staging-approved | production-approved | suspended | revoked (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *clientID == "" || *status == "" {
		return errors.New("--client-id and --status are required")
	}
	switch *status {
	case registry.ClientRequested, registry.ClientReviewing, registry.ClientStagingApproved,
		registry.ClientProductionApproved, registry.ClientSuspended, registry.ClientRevoked:
	default:
		return fmt.Errorf("--status %q is not a lifecycle state", *status)
	}
	svc, closer, err := newService(getenv)
	if err != nil {
		return err
	}
	defer closer()
	if err := svc.Store.SetClientStatus(context.Background(), *clientID, *status); err != nil {
		return err
	}
	note := ""
	switch *status {
	case registry.ClientSuspended, registry.ClientRevoked:
		note = "; the revocation epoch moved, so issued build sessions die on their next request"
	case registry.ClientProductionApproved:
		note = "; only production-approved registrations fetch production bundles"
	}
	fmt.Fprintf(stdout, "%s is now %s%s\n", *clientID, *status, note)
	return nil
}

// pemNotAfter reads the CA's expiry from the first certificate block, so
// the operator carries one file instead of a parallel out-of-band date.
func pemNotAfter(data []byte) (time.Time, error) {
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return time.Time{}, errors.New("the CA PEM carries no CERTIFICATE block")
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse CA certificate: %w", err)
		}
		return cert.NotAfter, nil
	}
}

func randomRegistrationValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomID(prefix string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
