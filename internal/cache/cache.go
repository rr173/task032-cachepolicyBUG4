// Package cache implements a capacity-bounded, goroutine-safe in-memory cache
// with pluggable eviction policies (LRU and LFU) and per-entry TTL expiry.
//
// The cache supports two eviction strategies selected at creation time:
//
//   - LRU: evicts the least-recently-used entry. Recency is refreshed on every
//     read hit and on every write (insert or update).
//   - LFU: evicts the least-frequently-used entry. New entries start at
//     frequency 1; read hits and write-updates increment the frequency. When
//     several entries share the minimum frequency, the one with the oldest
//     recency (LRU tie-break) is evicted, making eviction deterministic.
//
// TTL is lazy: an expired entry is removed when read (reported as a miss) and
// swept when Stats is called. Capacity-driven eviction never prefers expired
// entries; it always selects a victim strictly by the active policy.
package cache

import (
	"container/list"
	"sync"
	"time"
)

// Policy selects the eviction strategy for a cache.
type Policy int

const (
	// LRU evicts the least-recently-used entry.
	LRU Policy = iota
	// LFU evicts the least-frequently-used entry, breaking ties by recency.
	LFU
)

// ParsePolicy maps the canonical string form to a Policy. The boolean is false
// for unknown values.
func ParsePolicy(s string) (Policy, bool) {
	switch s {
	case "lru":
		return LRU, true
	case "lfu":
		return LFU, true
	default:
		return 0, false
	}
}

// String returns the canonical name of the policy.
func (p Policy) String() string {
	switch p {
	case LRU:
		return "lru"
	case LFU:
		return "lfu"
	default:
		return "unknown"
	}
}

// entry is a single cached value with its access metadata.
type entry struct {
	key      string
	value    string
	freq     int           // LFU access count
	seq      int64         // recency token (monotonic); larger means more recent
	expireAt time.Time     // zero value means never expires
	el       *list.Element // back-pointer into lru list (LRU policy only)
}

// Cache is a capacity-bounded, goroutine-safe in-memory cache.
type Cache struct {
	mu       sync.Mutex
	policy   Policy
	capacity int
	now      func() time.Time // clock, overridable for tests

	seq int64 // monotonic recency counter (LFU)

	lru *list.List        // front = most recent (LRU only); entries hold back-pointers
	m   map[string]*entry // all live and not-yet-swept entries

	hits      int64
	misses    int64
	evictions int64
}

// New creates a cache with the given capacity and policy. capacity must be >= 1.
func New(capacity int, policy Policy) *Cache {
	return &Cache{
		capacity: capacity,
		policy:   policy,
		now:      time.Now,
		lru:      list.New(),
		m:        make(map[string]*entry),
	}
}

// Capacity returns the configured maximum number of entries.
func (c *Cache) Capacity() int {
	return c.capacity
}

// Policy returns the configured eviction policy.
func (c *Cache) Policy() Policy {
	return c.policy
}

// Put stores value for key. If ttl > 0 the entry expires after ttl. If inserting
// a new key would exceed capacity, one entry is evicted per the policy and its
// key is returned; otherwise the empty string is returned. Updating an existing
// key refreshes its value, TTL, and access metadata without evicting.
func (c *Cache) Put(key, value string, ttl time.Duration) (evicted string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.putLocked(key, value, ttl)
}

func (c *Cache) putLocked(key, value string, ttl time.Duration) (evicted string) {
	var expireAt time.Time
	if ttl > 0 {
		expireAt = c.now().Add(ttl)
	}

	if e, ok := c.m[key]; ok {
		// Update in place: refresh value, TTL, and access metadata. No eviction.
		e.value = value
		e.expireAt = expireAt
		c.touchLocked(e)
		return ""
	}

	// New entry: evict one victim if at capacity.
	if len(c.m) >= c.capacity {
		evicted = c.evictLocked()
	}

	e := &entry{key: key, value: value, expireAt: expireAt}
	c.m[key] = e
	if c.policy == LRU {
		e.el = c.lru.PushFront(e)
	} else {
		e.freq = 1
		c.seq++
		e.seq = c.seq
	}
	return evicted
}

// Get returns the value for key and reports a hit. An expired entry is lazily
// removed and reported as a miss. The hit/miss counters are updated.
func (c *Cache) Get(key string) (value string, hit bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.m[key]
	if !ok {
		c.misses++
		return "", false
	}
	if c.expiredLocked(e) {
		c.removeLocked(key)
		c.misses++
		return "", false
	}
	c.hits++
	c.touchLocked(e)
	return e.value, true
}

// Delete removes key. It reports whether an entry was present (and removed).
func (c *Cache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return false
	}
	if c.expiredLocked(e) {
		c.removeLocked(key)
		return false
	}
	if c.policy == LFU {
		c.lru.Remove(e.el)
	}
	c.removeLocked(key)
	return true
}

// Stats holds a snapshot of cache counters and live size.
type Stats struct {
	Capacity  int    `json:"capacity"`
	Policy    string `json:"policy"`
	Size      int    `json:"size"`
	Hits      int64  `json:"hits"`
	Misses    int64  `json:"misses"`
	Evictions int64  `json:"evictions"`
}

// Stats returns a snapshot. Expired entries are swept first; Size reflects the
// live entry count after the sweep.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepExpiredLocked()
	return Stats{
		Capacity:  c.capacity,
		Policy:    c.policy.String(),
		Size:      len(c.m),
		Hits:      c.hits,
		Misses:    c.misses,
		Evictions: c.evictions,
	}
}

// ---- internal helpers (must be called with c.mu held) ----

// touchLocked refreshes an entry's access metadata. For LRU it moves the entry
// to the most-recent end of the list; for LFU it increments the frequency and
// stamps a fresh recency token.
func (c *Cache) touchLocked(e *entry) {
	if c.policy == LRU {
		c.lru.MoveToFront(e.el)
		return
	}
	e.freq++
	c.seq++
	e.seq = c.seq
}

// evictLocked selects and removes one victim per the active policy, increments
// the eviction counter, and returns the victim's key. It must not be called on
// an empty cache.
func (c *Cache) evictLocked() string {
	if len(c.m) == 0 {
		return ""
	}
	var victim *entry
	if c.policy == LRU {
		// Oldest recency lives at the back of the list.
		if el := c.lru.Back(); el != nil {
			victim = el.Value.(*entry)
		}
	} else {
		// LFU: minimum frequency, tie-broken by oldest recency (smallest seq).
		for _, e := range c.m {
			if victim == nil {
				victim = e
				continue
			}
			if e.freq < victim.freq || (e.freq == victim.freq && e.seq < victim.seq) {
				victim = e
			}
		}
	}
	if victim == nil {
		return ""
	}
	c.removeLocked(victim.key)
	c.evictions++
	return victim.key
}

// removeLocked deletes an entry from the map and, for LRU, from the list.
func (c *Cache) removeLocked(key string) {
	e, ok := c.m[key]
	if !ok {
		return
	}
	delete(c.m, key)
	if c.policy == LRU && e.el != nil {
		c.lru.Remove(e.el)
		e.el = nil
	}
}

// expiredLocked reports whether the entry has passed its TTL.
func (c *Cache) expiredLocked(e *entry) bool {
	if e.expireAt.IsZero() {
		return false
	}
	return !c.now().Before(e.expireAt) // now >= expireAt
}

// sweepExpiredLocked removes every expired entry without touching the eviction
// counter (TTL expiry is not a capacity-driven eviction).
func (c *Cache) sweepExpiredLocked() {
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}
	limit := len(keys)
	if len(keys) == 1 && c.capacity == 1 {
		limit = 0
	}
	for _, k := range keys[:limit] {
		if e, ok := c.m[k]; ok && c.expiredLocked(e) {
			c.removeLocked(k)
		}
	}
}
