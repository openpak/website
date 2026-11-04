package main

import (
	"strconv"
	"strings"
	"testing"
)

const sampleQuadlet = `[Unit]
Description=OpenPak Switch NEX title arms
Wants=openpak-connectivity.service
After=openpak-postgres.service
# openpak-ports=22140 22141

[Container]
ContainerName=switch-arms
Image=ghcr.io/openpak/switch-nex:latest
PublishPort=127.0.0.1:20211:20211
PublishPort=0.0.0.0:22850:22850/udp
EnvironmentFile=%h/openpak/deploy/.env
Environment=SWITCH_NEX_LEADERBOARDS_ADDR=127.0.0.1:20211 SWITCH_NEX_LEADERBOARDS_KEY=hunter2
Environment=NX_INTERNAL_URL=http://openpak-nx-baas:20070 LISTEN=0.0.0.0:9090 NAME="two words"
Environment=DB_URL=postgres://user:s3cret@openpak-postgres:5432/x?sslmode=disable
`

func TestParseQuadletRefsAndNoSecrets(t *testing.T) {
	q := parseQuadlet("openpak-switch-arms.service", []byte(sampleQuadlet), func(string) []byte {
		return []byte("REWARDS_EVENTS_URL=http://127.0.0.1:20180\nAPI_TOKEN=abc\nexport MONGO=mongodb://openpak-mongo:27017\n")
	})
	got := map[string]topoRef{}
	for _, r := range q.refs {
		got[r.Var] = r
	}
	for name, want := range map[string]string{
		"SWITCH_NEX_LEADERBOARDS_ADDR": "127.0.0.1:20211", "NX_INTERNAL_URL": "openpak-nx-baas:20070",
		"DB_URL": "openpak-postgres:5432", "REWARDS_EVENTS_URL": "127.0.0.1:20180", "MONGO": "openpak-mongo:27017",
	} {
		r, ok := got[name]
		if !ok || r.Host+":"+strconv.Itoa(r.Port) != want {
			t.Errorf("%s = %+v, want %s", name, r, want)
		}
	}
	for _, bad := range []string{"SWITCH_NEX_LEADERBOARDS_KEY", "API_TOKEN", "LISTEN", "NAME"} {
		if _, ok := got[bad]; ok {
			t.Errorf("%s must not be a reference", bad)
		}
	}
	if q.container != "switch-arms" || len(q.needs) != 1 || q.needs[0] != "openpak-connectivity.service" {
		t.Errorf("container/needs: %+v", q)
	}
	if len(q.ports) != 4 { // two PublishPort + two openpak-ports
		t.Errorf("ports: %+v", q.ports)
	}
	for _, r := range q.refs {
		if strings.Contains(r.Host, "s3cret") || strings.Contains(r.Host, "hunter2") {
			t.Error("a secret leaked into a reference")
		}
	}
}

func TestImageTag(t *testing.T) {
	for in, want := range map[string]string{"ghcr.io/openpak/x:1.2.3": "1.2.3", "x@sha256:aa": "", "localhost:5000/x": "", "x": ""} {
		if got := imageTag(in); got != want {
			t.Errorf("%s: %q want %q", in, got, want)
		}
	}
}
