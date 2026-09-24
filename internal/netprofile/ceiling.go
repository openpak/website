package netprofile

// The signed redirect ceiling (docs/signed-ceiling.md): per platform, the
// domain families a client may redirect at all. It used to be compiled into
// every client and into this server (families.go); it is now a file signed
// with an Ed25519 key that never touches the server. The box only serves the
// bytes it was given, verbatim: it cannot produce a new ceiling, so a
// compromised box cannot widen what clients redirect.
//
// This file is the verification side, shared by the website (serving and
// profile generation) and by `openpak-policy ceiling`, which also signs.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// CeilingType is the payload's type tag; anything else is not a ceiling.
const CeilingType = "openpak-ceiling"

// PinnedKey is a public key a ceiling may be signed with, and its keyid:
// the hex sha256 of the raw 32-byte key.
type PinnedKey struct {
	KeyID  string
	Public ed25519.PublicKey
}

// PinnedKeys is the list of keys a ceiling is accepted under. A list, so a
// successor can be pinned before the current key is retired. The private
// half lives only on the owner's machine.
var PinnedKeys = []PinnedKey{
	NewPinnedKey(mustHex("0fd2a2660868e53d20fc811e3f15710cb1ad157ba7da8019f7a59e8ad604d87c")),
}

// NewPinnedKey derives the keyid of a raw Ed25519 public key.
func NewPinnedKey(pub ed25519.PublicKey) PinnedKey {
	return PinnedKey{KeyID: KeyID(pub), Public: pub}
}

// KeyID is the contract's keyid: hex sha256 of the raw public key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

func mustHex(s string) ed25519.PublicKey {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		panic("netprofile: bad pinned key " + s)
	}
	return b
}

// Envelope is the file as served: the payload's exact bytes, base64, and the
// signatures over those bytes.
type Envelope struct {
	Payload    string      `json:"payload"`
	Signatures []Signature `json:"signatures"`
}

// Signature is one Ed25519 signature over the decoded payload bytes.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"`
}

// Ceiling is the signed payload. Unknown fields are ignored on decode.
type Ceiling struct {
	Type      string              `json:"type"`
	Version   int                 `json:"version"`
	Issued    string              `json:"issued"`
	Platforms map[string][]string `json:"platforms"`
}

// VerifyCeiling checks an envelope against the pinned keys and returns the
// payload it vouches for. The signature is checked over the decoded payload
// bytes — never a re-serialisation — and only then are those bytes parsed.
// At least one signature must be valid under a pinned key with that keyid.
func VerifyCeiling(envelope []byte, keys []PinnedKey) (*Ceiling, error) {
	var env Envelope
	if err := json.Unmarshal(envelope, &env); err != nil {
		return nil, fmt.Errorf("ceiling: envelope: %w", err)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil || len(payload) == 0 {
		return nil, errors.New("ceiling: payload is not base64")
	}
	if len(env.Signatures) == 0 {
		return nil, errors.New("ceiling: no signatures")
	}
	verified := false
	for _, s := range env.Signatures {
		sig, err := base64.StdEncoding.DecodeString(s.Sig)
		if err != nil || len(sig) != ed25519.SignatureSize {
			continue
		}
		for _, k := range keys {
			if k.KeyID == strings.ToLower(s.KeyID) && ed25519.Verify(k.Public, payload, sig) {
				verified = true
			}
		}
	}
	if !verified {
		return nil, errors.New("ceiling: no valid signature under a pinned key")
	}
	var c Ceiling
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("ceiling: payload: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate applies the contract's payload rules. Any malformed entry
// rejects the whole ceiling, the same way every client does.
func (c *Ceiling) Validate() error {
	if c.Type != CeilingType {
		return fmt.Errorf("ceiling: type %q, want %q", c.Type, CeilingType)
	}
	if c.Version <= 0 {
		return fmt.Errorf("ceiling: version %d is not a positive integer", c.Version)
	}
	if _, err := time.Parse(time.RFC3339, c.Issued); err != nil {
		return fmt.Errorf("ceiling: issued %q is not an RFC 3339 time", c.Issued)
	}
	if len(c.Platforms) == 0 {
		return errors.New("ceiling: no platforms")
	}
	for _, p := range sortedKeys(c.Platforms) {
		if !slices.Contains(PlatformsAllowed, p) {
			return fmt.Errorf("ceiling: platform %q is not one of %s", p, strings.Join(PlatformsAllowed, ", "))
		}
		seen := map[string]bool{}
		for _, f := range c.Platforms[p] {
			if err := ValidFamily(f); err != nil {
				return fmt.Errorf("ceiling: platform %s: %w", p, err)
			}
			if seen[f] {
				return fmt.Errorf("ceiling: platform %s lists %s twice", p, f)
			}
			seen[f] = true
		}
	}
	return nil
}

// ValidFamily is the contract's family rule: a leading dot, lower case, at
// least two non-empty labels (`.ea.com` yes, `.com` no), hostname characters.
func ValidFamily(f string) error {
	if !strings.HasPrefix(f, ".") {
		return fmt.Errorf("family %q does not start with a dot", f)
	}
	if f != strings.ToLower(f) {
		return fmt.Errorf("family %q is not lower case", f)
	}
	labels := strings.Split(f[1:], ".")
	if len(labels) < 2 {
		return fmt.Errorf("family %q has fewer than two labels", f)
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return fmt.Errorf("family %q has an invalid label %q", f, l)
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("family %q has an invalid character %q", f, r)
			}
		}
	}
	return nil
}

// Covers reports whether a hostname, or a family (leading dot), lies inside
// the platform's ceiling. A family covers its apex and everything below it.
func (c *Ceiling) Covers(platform, name string) bool {
	name = strings.ToLower(name)
	for _, f := range c.Platforms[platform] {
		if name == f || strings.HasSuffix(name, f) || name == f[1:] {
			return true
		}
	}
	return false
}

// CoversAny is Covers against every platform: what the union profile uses.
func (c *Ceiling) CoversAny(name string) bool {
	for p := range c.Platforms {
		if c.Covers(p, name) {
			return true
		}
	}
	return false
}

// FallbackCeiling is the compiled Universe applied to every platform: what
// the generator validates against when no verified ceiling file is present
// (development, tests, a box that has not been given one yet). Version 0
// marks it as not signed.
func FallbackCeiling() *Ceiling {
	c := &Ceiling{Type: CeilingType, Platforms: map[string][]string{}}
	for _, p := range PlatformsAllowed {
		c.Platforms[p] = slices.Clone(Universe)
	}
	return c
}

// CeilingPayload is the canonical payload serialisation the signer uses:
// compact JSON, platforms and families sorted. Verification never depends on
// it — clients verify whatever bytes were signed.
func CeilingPayload(c *Ceiling) ([]byte, error) {
	out := Ceiling{Type: c.Type, Version: c.Version, Issued: c.Issued, Platforms: map[string][]string{}}
	for p, fams := range c.Platforms {
		fams = slices.Clone(fams)
		sort.Strings(fams)
		out.Platforms[p] = fams
	}
	return json.Marshal(&out)
}

// SignCeiling validates a payload, signs its canonical bytes with key and
// returns the envelope file (indented, trailing newline).
func SignCeiling(c *Ceiling, key ed25519.PrivateKey) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	payload, err := CeilingPayload(c)
	if err != nil {
		return nil, err
	}
	pub := key.Public().(ed25519.PublicKey)
	env := Envelope{
		Payload: base64.StdEncoding.EncodeToString(payload),
		Signatures: []Signature{{
			KeyID: KeyID(pub),
			Sig:   base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)),
		}},
	}
	b, err := json.MarshalIndent(&env, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ErrNoCeiling means the ceiling file is absent or not verifiable: the
// endpoint answers 503 and the generator falls back to Universe.
var ErrNoCeiling = errors.New("netprofile: no verified ceiling")

// CeilingStore serves the ceiling file as the owner signed it. It re-reads
// the file whenever its mtime or size changes, so an scp'd file takes effect
// without a restart, and it verifies every version it reads: a file that
// does not verify is never served.
type CeilingStore struct {
	path string
	keys []PinnedKey
	log  *slog.Logger

	mu      sync.Mutex
	stamped bool
	modTime time.Time
	size    int64
	body    []byte
	etag    string
	ceiling *Ceiling
	err     error
}

// NewCeilingStore watches path; keys nil means PinnedKeys.
func NewCeilingStore(path string, keys []PinnedKey, log *slog.Logger) *CeilingStore {
	if keys == nil {
		keys = PinnedKeys
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &CeilingStore{path: path, keys: keys, log: log}
}

// Current returns the verified envelope bytes, their ETag and the payload.
// The error wraps ErrNoCeiling when the file is missing or invalid.
func (s *CeilingStore) Current() ([]byte, string, *Ceiling, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fi, err := os.Stat(s.path)
	if err != nil {
		if !s.stamped || s.err == nil || s.size != -1 {
			if errors.Is(err, fs.ErrNotExist) {
				s.log.Warn("netprofile: ceiling file missing; /api/v1/network/ceiling answers 503 and profiles use the compiled fallback", "path", s.path)
			} else {
				s.log.Error("netprofile: ceiling file unreadable", "path", s.path, "err", err)
			}
		}
		s.stamped, s.size, s.modTime = true, -1, time.Time{}
		s.body, s.etag, s.ceiling = nil, "", nil
		s.err = fmt.Errorf("%w: %v", ErrNoCeiling, err)
		return nil, "", nil, s.err
	}
	if s.stamped && fi.ModTime().Equal(s.modTime) && fi.Size() == s.size {
		return s.body, s.etag, s.ceiling, s.err
	}
	s.stamped, s.modTime, s.size = true, fi.ModTime(), fi.Size()
	body, err := os.ReadFile(s.path)
	if err == nil {
		var c *Ceiling
		if c, err = VerifyCeiling(body, s.keys); err == nil {
			sum := sha256.Sum256(body)
			s.body, s.etag, s.ceiling, s.err = body, `"`+hex.EncodeToString(sum[:])+`"`, c, nil
			s.log.Info("netprofile: ceiling loaded", "path", s.path, "version", c.Version, "issued", c.Issued)
			return s.body, s.etag, s.ceiling, nil
		}
	}
	s.log.Error("netprofile: ceiling file rejected; refusing to serve it and using the compiled fallback", "path", s.path, "err", err)
	s.body, s.etag, s.ceiling = nil, "", nil
	s.err = fmt.Errorf("%w: %v", ErrNoCeiling, err)
	return nil, "", nil, s.err
}

// Effective is the ceiling the generator validates against: the verified
// file, or the compiled fallback without one.
func (s *CeilingStore) Effective() *Ceiling {
	if s != nil {
		if _, _, c, err := s.Current(); err == nil {
			return c
		}
	}
	return FallbackCeiling()
}
