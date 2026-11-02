package accounts

import (
	"context"
	"reflect"
	"testing"

	"github.com/openpak/website/internal/core"
)

type codeIdentity struct{ core.Identity }

func (codeIdentity) GetAccount(context.Context, string) (core.Account, error) {
	return core.Account{Handle: "LeiaOrgana"}, nil
}

type codeLinks struct {
	core.Links
	links map[string]core.Link
}

func (l codeLinks) ActiveLinkByAccount(_ context.Context, namespace, _ string) (core.Link, error) {
	if link, ok := l.links[namespace]; ok {
		return link, nil
	}
	return core.Link{}, core.ErrNotFound
}

func (l codeLinks) AccountByPublicCode(_ context.Context, namespace, code string) (string, error) {
	if link, ok := l.links[namespace]; ok && link.PublicCode == code {
		return "leia", nil
	}
	return "", core.ErrNotFound
}

func TestNamedNetworkCodesResolveInTheirOwnNetwork(t *testing.T) {
	for _, kind := range []string{CodeWiiU, CodeXbox, CodeX360, CodePSN} {
		links := codeLinks{links: map[string]core.Link{kind: {PublicCode: "LeiaOrgana"}}}
		for typed, want := range map[string]string{"LeiaOrgana": "leia", "unknown": ""} {
			if got := ResolveFriendCode(context.Background(), nil, links, nil, kind, typed, ""); got != want {
				t.Errorf("%s %q: got %q, want %q", kind, typed, got, want)
			}
		}
	}
}

func TestFriendCodesIncludesEveryConsoleIdentity(t *testing.T) {
	links := codeLinks{links: map[string]core.Link{
		"switch": {PublicCode: "SW-2077-7677-2534"},
		"3ds":    {SubjectID: "1517549416"},
		"wiiu":   {PublicCode: "LeiaOrgana"},
		"xbox":   {PublicCode: "LeiaOrgana"},
		"x360":   {PublicCode: "LeiaOrgana"},
		"np":     {PublicCode: "LeiaOrgana"},
	}}
	codes := FriendCodes(context.Background(), codeIdentity{}, links, nil, "leia", "leia@example.test")
	want := []Code{
		{CodeHandle, "OpenPak", "Handle", "LeiaOrgana"},
		{CodeEmail, "OpenPak", "Email", "leia@example.test"},
		{CodeSwitch, "Switch", "Switch", "SW-2077-7677-2534"},
		{Code3DS, "3DS", "3DS", FriendCode3DS(1517549416)},
		{CodeWiiU, "Wii U", "Wii U NNID", "LeiaOrgana"},
		{CodeXbox, "Xbox", "Xbox gamertag", "LeiaOrgana"},
		{CodeX360, "Xbox 360", "Xbox 360 gamertag", "LeiaOrgana"},
		{CodePSN, "PS3", "PlayStation 3 Online ID", "LeiaOrgana"},
	}
	if !reflect.DeepEqual(codes, want) {
		t.Fatalf("codes = %#v; want %#v", codes, want)
	}
	// An unprovisioned network or an unpublished name must not become a
	// made-up identity, even when the core account has a shared handle.
	delete(links.links, "xbox")
	links.links["np"] = core.Link{SubjectID: "numeric-subject"}
	codes = FriendCodes(context.Background(), codeIdentity{}, links, nil, "leia", "")
	for _, code := range codes {
		if code.Kind == CodeXbox || code.Kind == CodePSN {
			t.Fatalf("unavailable identity shown: %#v", code)
		}
	}
}
