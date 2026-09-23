package store

import (
	"context"
	"testing"
)

// A minted code resolves back to its account however it is typed, stays the
// same on the next read, and something that is not a code resolves to nothing.
func TestOpenPakCodes(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	code, err := m.OpenPakCode(ctx, "acct-a")
	if err != nil || NormalizeOpenPakCode(code) != code {
		t.Fatalf("minted %q, %v", code, err)
	}
	if again, _ := m.OpenPakCode(ctx, "acct-a"); again != code {
		t.Fatalf("second read %q, want %q", again, code)
	}
	for _, typed := range []string{code, code[3:], "op" + code[3:7] + code[8:], " " + code[3:7] + " " + code[8:]} {
		if id, err := m.AccountByOpenPakCode(ctx, typed); err != nil || id != "acct-a" {
			t.Errorf("AccountByOpenPakCode(%q) = %q, %v", typed, id, err)
		}
	}
	for _, bad := range []string{"", "OP-0000-0000", "OP-ABCD-EFG", "SW-1234-5678-9012"} {
		if id, err := m.AccountByOpenPakCode(ctx, bad); err == nil {
			t.Errorf("AccountByOpenPakCode(%q) = %q, want not found", bad, id)
		}
	}
}
