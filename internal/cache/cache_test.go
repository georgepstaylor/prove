package cache

import (
	"testing"
	"time"
)

func TestGetSet(t *testing.T) {
	c := NewTTL[string, int](time.Minute, 16)
	if _, ok := c.Get("a"); ok {
		t.Fatal("empty cache should miss")
	}
	c.Set("a", 42)
	if v, ok := c.Get("a"); !ok || v != 42 {
		t.Fatalf("got (%d,%v), want (42,true)", v, ok)
	}
}

func TestExpiry(t *testing.T) {
	c := NewTTL[string, int](20*time.Millisecond, 16)
	c.Set("a", 1)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("should hit before expiry")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("should miss after expiry")
	}
}

func TestCapacityEviction(t *testing.T) {
	c := NewTTL[string, int](time.Minute, 1)
	c.Set("a", 1)
	c.Set("b", 2) // exceeds capacity → evicts LRU ("a")
	if _, ok := c.Get("a"); ok {
		t.Fatal("a should have been evicted")
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Fatalf("b: got (%d,%v), want (2,true)", v, ok)
	}
}
