package accounts

import "testing"

// A 3DS code decodes to the id it was made from, for short ids too (their
// hex is narrower, which shifts the checksum), and a typo decodes to nobody.
func TestFriendCode3DSRoundTrip(t *testing.T) {
	for _, pid := range []int64{1, 0xff, 0x1234, 1743126339, 0x7fffffff, 0xffffffff} {
		code := FriendCode3DS(pid)
		if got, ok := PIDFrom3DSCode(code); !ok || got != pid {
			t.Errorf("PIDFrom3DSCode(%q) = %d, %v; want %d", code, got, ok, pid)
		}
	}
	code := FriendCode3DS(1743126339)
	typo := code[:13] + string('0'+(code[13]-'0'+1)%10)
	if pid, ok := PIDFrom3DSCode(typo); ok {
		t.Errorf("typo %q decoded to %d", typo, pid)
	}
}
