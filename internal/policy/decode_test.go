package policy

import (
	"strings"
	"testing"
)

func TestDecodeAcceptsAWellFormedBundle(t *testing.T) {
	c := fixtureCanonical(t, fixtureOptions{})
	b, err := c.Project("switch", "stable", 42, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeBundle(data)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if err := got.Validate(EnvProduction); err != nil {
		t.Fatalf("round trip does not validate: %v", err)
	}
	if got.Sequence != 42 || got.Platform != "switch" {
		t.Fatalf("round trip lost identity: %+v", got)
	}
}

func TestDecodeRejections(t *testing.T) {
	c := fixtureCanonical(t, fixtureOptions{})
	b, err := c.Project("switch", "stable", 42, fixtureIssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	good, err := MarshalBundle(b)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{
			name: "duplicate key",
			mutate: func(s string) string {
				return strings.Replace(s, `"sequence":42,`, `"sequence":42,"sequence":43,`, 1)
			},
			wantErr: "duplicate",
		},
		{
			name: "unknown field",
			mutate: func(s string) string {
				return strings.Replace(s, `{"schema_version":2,`, `{"schema_version":2,"route":"x",`, 1)
			},
			wantErr: "unknown field",
		},
		{
			name: "diagnostic extension is ignored",
			mutate: func(s string) string {
				return strings.Replace(s, `{"schema_version":2,`, `{"schema_version":2,"x_probe_id":"lab-7",`, 1)
			},
			wantErr: "",
		},
		{
			name: "trailing data",
			mutate: func(s string) string {
				return s + `{"schema_version":2}`
			},
			wantErr: "trailing",
		},
		{
			name: "fractional sequence",
			mutate: func(s string) string {
				return strings.Replace(s, `"sequence":42,`, `"sequence":42.5,`, 1)
			},
			wantErr: "sequence",
		},
		{
			name: "out of range sequence",
			mutate: func(s string) string {
				return strings.Replace(s, `"sequence":42,`, `"sequence":9223372036854775808,`, 1)
			},
			wantErr: "sequence",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.mutate(string(good))
			_, err := DecodeBundle([]byte(data))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected acceptance, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected rejection, got success")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestDecodeRejectsAnOversizedBundle(t *testing.T) {
	pad := strings.Repeat("x", MaxPolicyBytes)
	data := `{"schema_version":2,"x_pad":"` + pad + `"}`
	if _, err := DecodeBundle([]byte(data)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized bundle accepted: %v", err)
	}
}
