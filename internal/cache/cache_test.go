package cache

import (
	"testing"
	"time"
)

// clock is a mutable in-process clock for deterministic tests.
type clock struct{ t time.Time }

func newClock() *clock                   { return &clock{t: time.Unix(1_000_000, 0)} }
func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newCache builds a cache wired to the test clock so TTL is exercised without
// real sleeps.
func newCache(capacity int, p Policy, clk *clock) *Cache {
	c := New(capacity, p)
	c.now = clk.now
	return c
}

func TestPutGetBasic(t *testing.T) {
	clk := newClock()
	c := newCache(2, LRU, clk)

	if ev := c.Put("a", "1", 0); ev != "" {
		t.Fatalf("put a: evicted=%q want empty", ev)
	}
	if v, hit := c.Get("a"); !hit || v != "1" {
		t.Fatalf("get a: v=%q hit=%v", v, hit)
	}
	if _, hit := c.Get("missing"); hit {
		t.Fatal("get missing: want miss")
	}
}

func TestLRUEviction(t *testing.T) {
	clk := newClock()
	c := newCache(2, LRU, clk)

	c.Put("a", "1", 0)
	c.Put("b", "2", 0)
	if ev := c.Put("c", "3", 0); ev != "a" {
		t.Fatalf("put c: evicted=%q want a", ev)
	}
	if _, hit := c.Get("a"); hit {
		t.Fatal("get a after evict: want miss")
	}
	if v, hit := c.Get("b"); !hit || v != "2" {
		t.Fatalf("get b: v=%q hit=%v", v, hit)
	}
	// Updating b refreshes recency; no eviction.
	if ev := c.Put("b", "20", 0); ev != "" {
		t.Fatalf("update b: evicted=%q want empty", ev)
	}
	// b is now most-recent; c is oldest, so d evicts c.
	if ev := c.Put("d", "4", 0); ev != "c" {
		t.Fatalf("put d: evicted=%q want c", ev)
	}
	if _, hit := c.Get("c"); hit {
		t.Fatal("get c after evict: want miss")
	}
	if v, hit := c.Get("b"); !hit || v != "20" {
		t.Fatalf("get b updated: v=%q hit=%v", v, hit)
	}
	if v, hit := c.Get("d"); !hit || v != "4" {
		t.Fatalf("get d: v=%q hit=%v", v, hit)
	}
}

func TestLRUUpdateIsAccess(t *testing.T) {
	clk := newClock()
	c := newCache(2, LRU, clk)

	c.Put("a", "1", 0)
	c.Put("b", "2", 0)
	// Refresh a so b becomes the oldest.
	c.Put("a", "10", 0)
	if ev := c.Put("c", "3", 0); ev != "b" {
		t.Fatalf("put c: evicted=%q want b (a was refreshed)", ev)
	}
	if v, hit := c.Get("a"); !hit || v != "10" {
		t.Fatalf("get a: v=%q hit=%v", v, hit)
	}
}

func TestLFUEviction(t *testing.T) {
	clk := newClock()
	c := newCache(2, LFU, clk)

	c.Put("a", "1", 0) // freq a = 1
	c.Put("b", "2", 0) // freq b = 1
	c.Get("a")         // freq a = 2
	c.Get("a")         // freq a = 3
	// b has the smaller frequency; evict b.
	if ev := c.Put("c", "3", 0); ev != "b" {
		t.Fatalf("put c: evicted=%q want b", ev)
	}
	if _, hit := c.Get("b"); hit {
		t.Fatal("get b after evict: want miss")
	}
	if v, hit := c.Get("a"); !hit || v != "1" {
		t.Fatalf("get a: v=%q hit=%v", v, hit)
	}
	if v, hit := c.Get("c"); !hit || v != "3" {
		t.Fatalf("get c: v=%q hit=%v", v, hit)
	}
}

func TestLFUTieBreak(t *testing.T) {
	clk := newClock()
	c := newCache(2, LFU, clk)

	c.Put("x", "1", 0) // seq 1
	c.Put("y", "2", 0) // seq 2
	c.Get("x")         // freq x = 2, seq 3
	c.Get("y")         // freq y = 2, seq 4
	// Equal frequency (2); x has the older recency, so evict x.
	if ev := c.Put("z", "3", 0); ev != "x" {
		t.Fatalf("put z: evicted=%q want x (tie-break)", ev)
	}
	if _, hit := c.Get("x"); hit {
		t.Fatal("get x after evict: want miss")
	}
	if v, hit := c.Get("y"); !hit || v != "2" {
		t.Fatalf("get y: v=%q hit=%v", v, hit)
	}
	if v, hit := c.Get("z"); !hit || v != "3" {
		t.Fatalf("get z: v=%q hit=%v", v, hit)
	}
}

func TestLFUPutUpdateBumpsFreq(t *testing.T) {
	clk := newClock()
	c := newCache(2, LFU, clk)

	c.Put("a", "1", 0)  // freq a = 1
	c.Put("b", "2", 0)  // freq b = 1
	c.Put("a", "10", 0) // write-update: freq a = 2
	// b is now the sole min-frequency entry.
	if ev := c.Put("c", "3", 0); ev != "b" {
		t.Fatalf("put c: evicted=%q want b", ev)
	}
}

func TestTTLExpiry(t *testing.T) {
	clk := newClock()
	c := newCache(3, LRU, clk)

	c.Put("k", "v", 1*time.Second) // expires at clk+1s
	if v, hit := c.Get("k"); !hit || v != "v" {
		t.Fatalf("get before expiry: v=%q hit=%v", v, hit)
	}
	clk.advance(2 * time.Second)
	if _, hit := c.Get("k"); hit {
		t.Fatal("get after expiry: want miss")
	}
	// The expired entry must have been lazily removed.
	if st := c.Stats(); st.Size != 0 {
		t.Fatalf("stats size=%d want 0", st.Size)
	}
	if st := c.Stats(); st.Hits != 1 || st.Misses != 1 {
		t.Fatalf("stats hits=%d misses=%d want 1/1", st.Hits, st.Misses)
	}
	if st := c.Stats(); st.Evictions != 0 {
		t.Fatalf("stats evictions=%d want 0 (TTL is not eviction)", st.Evictions)
	}
}

func TestTTLSweepOnStats(t *testing.T) {
	clk := newClock()
	c := newCache(3, LRU, clk)

	c.Put("k1", "v1", 1*time.Second)
	c.Put("k2", "v2", 1*time.Second)
	clk.advance(2 * time.Second)
	// Neither entry has been read; sweep happens during Stats.
	if st := c.Stats(); st.Size != 0 {
		t.Fatalf("stats size=%d want 0 (swept)", st.Size)
	}
}

func TestTTLPutUpdateRefreshesExpiry(t *testing.T) {
	clk := newClock()
	c := newCache(2, LRU, clk)

	c.Put("k", "v", 2*time.Second) // expires at +2s
	clk.advance(1 * time.Second)
	c.Put("k", "v2", 2*time.Second) // refresh: now expires at +3s from start
	clk.advance(1 * time.Second)    // +2s: original expiry passed, refreshed still alive
	if v, hit := c.Get("k"); !hit || v != "v2" {
		t.Fatalf("get after refresh: v=%q hit=%v", v, hit)
	}
	clk.advance(1 * time.Second) // +3s: refreshed expiry reached
	if _, hit := c.Get("k"); hit {
		t.Fatal("get after refreshed expiry: want miss")
	}
}

func TestDeleteExpiredEntryReportsMissing(t *testing.T) {
	clk := newClock()
	c := newCache(2, LRU, clk)
	c.Put("expired", "v", time.Second)
	clk.advance(2 * time.Second)
	if c.Delete("expired") {
		t.Fatal("delete expired entry: want missing")
	}
	if st := c.Stats(); st.Size != 0 {
		t.Fatalf("stats size=%d want 0", st.Size)
	}
}

func TestStatsCounters(t *testing.T) {
	clk := newClock()
	c := newCache(2, LRU, clk)

	c.Get("nope") // miss
	c.Put("a", "1", 0)
	c.Get("a") // hit
	c.Put("b", "2", 0)
	c.Put("c", "3", 0) // evict a

	st := c.Stats()
	if st.Hits != 1 {
		t.Errorf("hits=%d want 1", st.Hits)
	}
	if st.Misses != 1 {
		t.Errorf("misses=%d want 1", st.Misses)
	}
	if st.Evictions != 1 {
		t.Errorf("evictions=%d want 1", st.Evictions)
	}
	if st.Size != 2 {
		t.Errorf("size=%d want 2", st.Size)
	}
}

func TestParsePolicy(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Policy
		ok   bool
	}{
		{"lru", LRU, true},
		{"lfu", LFU, true},
		{"", 0, false},
		{"random", 0, false},
	} {
		if p, ok := ParsePolicy(tc.in); ok != tc.ok || (ok && p != tc.want) {
			t.Errorf("ParsePolicy(%q) = %v,%v want %v,%v", tc.in, p, ok, tc.want, tc.ok)
		}
	}
}
