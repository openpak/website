package xbaccount

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type pki struct {
	ca     *x509.Certificate
	caKey  *ecdsa.PrivateKey
	caPEM  []byte
	serial int64
}

func newPKI(t *testing.T) *pki {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	return &pki{ca: ca, caKey: key, caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), serial: 1}
}

// leaf issues a certificate with a SPIFFE URI SAN (client) or localhost (server).
func (p *pki) leaf(t *testing.T, spiffe string, server bool) (certPEM, keyPEM []byte) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p.serial++
	tpl := &x509.Certificate{SerialNumber: big.NewInt(p.serial), Subject: pkix.Name{CommonName: "leaf"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour)}
	if server {
		tpl.DNSNames, tpl.IPAddresses = []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}
		tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	} else {
		u, _ := url.Parse(spiffe)
		tpl.URIs = []*url.URL{u}
		tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}

// mtlsServer answers like xb-account: it refuses any client whose certificate lacks the
// provisioner's SPIFFE URI, and replies with answer for the rest.
func mtlsServer(t *testing.T, p *pki, status int, answer string, seen *map[string]any, path *string) *httptest.Server {
	t.Helper()
	sc, sk := p.leaf(t, "", true)
	pair, _ := tls.X509KeyPair(sc, sk)
	pool := x509.NewCertPool()
	pool.AddCert(p.ca)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates[0].URIs) != 1 || r.TLS.PeerCertificates[0].URIs[0].String() != "spiffe://openpak/xb-provisioner" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		*path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(seen)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func writeEnv(t *testing.T, p *pki, srv *httptest.Server, spiffe string) {
	t.Helper()
	dir := t.TempDir()
	cert, key := p.leaf(t, spiffe, false)
	for name, data := range map[string][]byte{"cert.pem": cert, "key.pem": key, "ca.pem": p.caPEM} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XB_ACCOUNT_URL", srv.URL)
	t.Setenv("XB_ACCOUNT_CLIENT_CERT_FILE", filepath.Join(dir, "cert.pem"))
	t.Setenv("XB_ACCOUNT_CLIENT_KEY_FILE", filepath.Join(dir, "key.pem"))
	t.Setenv("XB_ACCOUNT_CA_FILE", filepath.Join(dir, "ca.pem"))
}

func TestSetGamertagOverMTLS(t *testing.T) {
	p := newPKI(t)
	var seen map[string]any
	var path string
	srv := mtlsServer(t, p, 200, `{"xuid":"0009abc","gamertag":"Cool Tag","previous":"Old","changed":true}`, &seen, &path)
	writeEnv(t, p, srv, "spiffe://openpak/xb-provisioner")
	c, err := FromEnv()
	if err != nil || c == nil {
		t.Fatalf("FromEnv: %v %v", c, err)
	}
	res, err := c.SetGamertag(context.Background(), "acct/1 x", "Cool Tag", "web-1", "website:acct")
	if err != nil || res.Gamertag != "Cool Tag" || !res.Changed || res.Previous != "Old" {
		t.Fatalf("result %+v err %v", res, err)
	}
	if path != "/internal/v1/accounts/acct%2F1%20x/gamertag" && path != "/internal/v1/accounts/acct/1 x/gamertag" {
		t.Errorf("path %q", path)
	}
	if seen["gamertag"] != "Cool Tag" || seen["request_id"] != "web-1" || seen["actor"] != "website:acct" || len(seen) != 3 {
		t.Errorf("body %v", seen)
	}
}

func TestWrongIdentityAndNoCertAreUnavailable(t *testing.T) {
	p := newPKI(t)
	var seen map[string]any
	var path string
	srv := mtlsServer(t, p, 200, `{}`, &seen, &path)
	writeEnv(t, p, srv, "spiffe://openpak/xb-gateway")
	c, _ := FromEnv()
	if _, err := c.SetGamertag(context.Background(), "a", "Tag", "", "x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("foreign SPIFFE id: %v", err)
	}
	if path != "" {
		t.Error("the handler ran for a certificate without the provisioner identity")
	}
	// No client certificate at all: the handshake fails.
	plain := New(srv.URL, &tls.Config{RootCAs: func() *x509.CertPool { q := x509.NewCertPool(); q.AddCert(p.ca); return q }()})
	if _, err := plain.SetGamertag(context.Background(), "a", "Tag", "", "x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no client cert: %v", err)
	}
}

func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
	}{
		{400, `{"error":"invalid_gamertag"}`, ErrInvalid},
		{422, `{"error":"reserved_gamertag"}`, ErrReserved},
		{409, `{"error":"gamertag_taken"}`, ErrTaken},
		{409, `{"error":"console_bound"}`, ErrConsoleBound},
		{409, `{"error":"request_id_reused"}`, ErrRequestReused},
		{404, `{"error":"no_xbox_identity"}`, ErrNoIdentity},
		{503, `{"error":"unavailable"}`, ErrUnavailable},
		{500, `oops`, ErrUnavailable},
		{200, `not json`, ErrUnavailable},
		{200, `{"xuid":"1"}`, ErrUnavailable},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := New(srv.URL, nil).SetGamertag(context.Background(), "a", "Tag", "r", "x")
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Errorf("%d %s: got %v, want %v", tc.status, tc.body, err, tc.want)
		}
	}
	var none *Client
	if _, err := none.SetGamertag(context.Background(), "a", "T", "", ""); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil client: %v", err)
	}
}

func TestFromEnvUnsetAndBroken(t *testing.T) {
	t.Setenv("XB_ACCOUNT_URL", "")
	if c, err := FromEnv(); c != nil || err != nil {
		t.Errorf("unset: %v %v", c, err)
	}
	t.Setenv("XB_ACCOUNT_URL", "https://x")
	if c, err := FromEnv(); c != nil || err == nil {
		t.Errorf("missing certificate files must be an error: %v %v", c, err)
	}
}
