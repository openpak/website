package ratelimit

import (
	"testing"
	"time"
)

func TestFixedWindow(t *testing.T) {
	l := New(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("event %d denied", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("4th event in window must be denied")
	}
	if !l.Allow("other") {
		t.Fatal("other key must be independent")
	}
}

func TestWindowReset(t *testing.T) {
	l := New(1, 10*time.Millisecond)
	if !l.Allow("k") || l.Allow("k") {
		t.Fatal("second immediate event must be denied")
	}
	time.Sleep(20 * time.Millisecond)
	if !l.Allow("k") {
		t.Fatal("window must reset")
	}
}
