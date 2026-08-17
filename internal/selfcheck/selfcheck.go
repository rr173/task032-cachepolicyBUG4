// Package selfcheck runs an end-to-end verification of the cache service
// against an in-process HTTP server. It is invoked by the --smoke-test flag and
// exits the process on completion.
package selfcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"task032-cachepolicy/internal/httpapi"
)

// Run exercises the full HTTP API and returns nil if every behavior matches
// the specification. On failure it returns an error describing the first
// mismatch.
func Run() error {
	srv := httpapi.New()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := ts.Client()

	// 1. LRU eviction sequence with update-as-access.
	if err := create(c, ts.URL, "lru", "lru", 2); err != nil {
		return err
	}
	if ev, err := put(c, ts.URL, "lru", "a", "1", 0); err != nil {
		return err
	} else if ev != nil {
		return fmt.Errorf("put a: evicted=%v want null", ev)
	}
	if _, err := put(c, ts.URL, "lru", "b", "2", 0); err != nil {
		return err
	}
	if ev, err := put(c, ts.URL, "lru", "c", "3", 0); err != nil {
		return err
	} else if ev == nil || *ev != "a" {
		return fmt.Errorf("put c: evicted=%v want a", ev)
	}
	if code, _, err := get(c, ts.URL, "lru", "a"); err != nil {
		return err
	} else if code != http.StatusNotFound {
		return fmt.Errorf("get a after evict: code=%d want 404", code)
	}
	if code, body, err := get(c, ts.URL, "lru", "b"); err != nil {
		return err
	} else if code != http.StatusOK || body["value"] != "2" {
		return fmt.Errorf("get b: code=%d body=%v", code, body)
	}
	// Update b: no eviction.
	if ev, err := put(c, ts.URL, "lru", "b", "20", 0); err != nil {
		return err
	} else if ev != nil {
		return fmt.Errorf("update b: evicted=%v want null", ev)
	}
	// b is now most-recent; c is oldest, so d evicts c.
	if ev, err := put(c, ts.URL, "lru", "d", "4", 0); err != nil {
		return err
	} else if ev == nil || *ev != "c" {
		return fmt.Errorf("put d: evicted=%v want c", ev)
	}
	if code, _, err := get(c, ts.URL, "lru", "c"); err != nil {
		return err
	} else if code != http.StatusNotFound {
		return fmt.Errorf("get c after evict: code=%d want 404", code)
	}
	if code, body, err := get(c, ts.URL, "lru", "b"); err != nil {
		return err
	} else if code != http.StatusOK || body["value"] != "20" {
		return fmt.Errorf("get b updated: code=%d body=%v", code, body)
	}
	if code, body, err := get(c, ts.URL, "lru", "d"); err != nil {
		return err
	} else if code != http.StatusOK || body["value"] != "4" {
		return fmt.Errorf("get d: code=%d body=%v", code, body)
	}

	// 2. LFU eviction by frequency.
	if err := create(c, ts.URL, "lfu", "lfu", 2); err != nil {
		return err
	}
	if _, err := put(c, ts.URL, "lfu", "a", "1", 0); err != nil {
		return err
	}
	if _, err := put(c, ts.URL, "lfu", "b", "2", 0); err != nil {
		return err
	}
	if code, _, err := get(c, ts.URL, "lfu", "a"); err != nil {
		return err
	} else if code != http.StatusOK {
		return fmt.Errorf("get a (lfu bump): code=%d want 200", code)
	}
	if code, _, err := get(c, ts.URL, "lfu", "a"); err != nil {
		return err
	} else if code != http.StatusOK {
		return fmt.Errorf("get a (lfu bump 2): code=%d want 200", code)
	}
	// b has the smaller frequency; evict b.
	if ev, err := put(c, ts.URL, "lfu", "c", "3", 0); err != nil {
		return err
	} else if ev == nil || *ev != "b" {
		return fmt.Errorf("put c (lfu): evicted=%v want b", ev)
	}
	if code, _, err := get(c, ts.URL, "lfu", "b"); err != nil {
		return err
	} else if code != http.StatusNotFound {
		return fmt.Errorf("get b after evict (lfu): code=%d want 404", code)
	}
	if code, _, err := get(c, ts.URL, "lfu", "a"); err != nil {
		return err
	} else if code != http.StatusOK {
		return fmt.Errorf("get a (lfu): code=%d want 200", code)
	}
	if code, _, err := get(c, ts.URL, "lfu", "c"); err != nil {
		return err
	} else if code != http.StatusOK {
		return fmt.Errorf("get c (lfu): code=%d want 200", code)
	}

	// 3. LFU tie-break by recency.
	if err := create(c, ts.URL, "lfu2", "lfu", 2); err != nil {
		return err
	}
	if _, err := put(c, ts.URL, "lfu2", "x", "1", 0); err != nil {
		return err
	}
	if _, err := put(c, ts.URL, "lfu2", "y", "2", 0); err != nil {
		return err
	}
	if code, _, err := get(c, ts.URL, "lfu2", "x"); err != nil {
		return err
	} else if code != http.StatusOK {
		return fmt.Errorf("get x (lfu tie bump): code=%d want 200", code)
	}
	if code, _, err := get(c, ts.URL, "lfu2", "y"); err != nil {
		return err
	} else if code != http.StatusOK {
		return fmt.Errorf("get y (lfu tie bump): code=%d want 200", code)
	}
	// Equal frequency; x is older, evict x.
	if ev, err := put(c, ts.URL, "lfu2", "z", "3", 0); err != nil {
		return err
	} else if ev == nil || *ev != "x" {
		return fmt.Errorf("put z (lfu tie): evicted=%v want x", ev)
	}
	if code, _, err := get(c, ts.URL, "lfu2", "x"); err != nil {
		return err
	} else if code != http.StatusNotFound {
		return fmt.Errorf("get x after tie evict: code=%d want 404", code)
	}
	if code, _, err := get(c, ts.URL, "lfu2", "y"); err != nil {
		return err
	} else if code != http.StatusOK {
		return fmt.Errorf("get y (lfu tie): code=%d want 200", code)
	}
	if code, _, err := get(c, ts.URL, "lfu2", "z"); err != nil {
		return err
	} else if code != http.StatusOK {
		return fmt.Errorf("get z (lfu tie): code=%d want 200", code)
	}

	// 4. TTL: hit before expiry, miss after, lazy delete + sweep, counters.
	if err := create(c, ts.URL, "ttl", "lru", 3); err != nil {
		return err
	}
	if _, err := put(c, ts.URL, "ttl", "k1", "v1", 1); err != nil {
		return err
	}
	if code, body, err := get(c, ts.URL, "ttl", "k1"); err != nil {
		return err
	} else if code != http.StatusOK || body["value"] != "v1" {
		return fmt.Errorf("get k1 before expiry: code=%d body=%v", code, body)
	}
	if _, err := put(c, ts.URL, "ttl", "k2", "v2", 1); err != nil {
		return err
	}
	fmt.Println("[smoke] ttl: sleeping ~1.1s for expiry")
	time.Sleep(1100 * time.Millisecond)
	if code, body, err := get(c, ts.URL, "ttl", "k1"); err != nil {
		return err
	} else if code != http.StatusNotFound || body["hit"] != false {
		return fmt.Errorf("get k1 after expiry: code=%d body=%v want 404 hit=false", code, body)
	}
	if code, body, err := stats(c, ts.URL, "ttl"); err != nil {
		return err
	} else if code != http.StatusOK {
		return fmt.Errorf("stats ttl: code=%d", code)
	} else if body["size"] != float64(0) {
		return fmt.Errorf("stats ttl: size=%v want 0 (swept k2)", body["size"])
	} else if body["hits"] != float64(1) {
		return fmt.Errorf("stats ttl: hits=%v want 1", body["hits"])
	} else if body["misses"] != float64(1) {
		return fmt.Errorf("stats ttl: misses=%v want 1", body["misses"])
	} else if body["evictions"] != float64(0) {
		return fmt.Errorf("stats ttl: evictions=%v want 0", body["evictions"])
	}

	// 5. Error cases.
	if code, body, err := createBody(c, ts.URL, "cap0", "lru", 0); err != nil {
		return err
	} else if code != http.StatusBadRequest || body["error"] != "capacity must be positive" {
		return fmt.Errorf("capacity=0: code=%d body=%v", code, body)
	}
	if code, body, err := createBody(c, ts.URL, "badpol", "random", 4); err != nil {
		return err
	} else if code != http.StatusBadRequest || body["error"] != "policy must be lru or lfu" {
		return fmt.Errorf("bad policy: code=%d body=%v", code, body)
	}
	if code, body, err := createBody(c, ts.URL, "lru", "lru", 2); err != nil {
		return err
	} else if code != http.StatusConflict || body["error"] != "cache already exists" {
		return fmt.Errorf("conflict: code=%d body=%v", code, body)
	}
	// Missing instance across endpoints.
	if code, _, err := get(c, ts.URL, "ghost", "k"); err != nil {
		return err
	} else if code != http.StatusNotFound {
		return fmt.Errorf("missing get: code=%d want 404", code)
	}
	if code, _, err := putRaw(c, ts.URL, "ghost", "k", "v", 0); err != nil {
		return err
	} else if code != http.StatusNotFound {
		return fmt.Errorf("missing put: code=%d want 404", code)
	}
	if code, _, err := stats(c, ts.URL, "ghost"); err != nil {
		return err
	} else if code != http.StatusNotFound {
		return fmt.Errorf("missing stats: code=%d want 404", code)
	}
	if code, err := delInstance(c, ts.URL, "ghost"); err != nil {
		return err
	} else if code != http.StatusNotFound {
		return fmt.Errorf("missing delete instance: code=%d want 404", code)
	}

	// 6. Delete instance then 404.
	if code, err := delInstance(c, ts.URL, "lru"); err != nil {
		return err
	} else if code != http.StatusNoContent {
		return fmt.Errorf("delete instance: code=%d want 204", code)
	}
	if code, _, err := stats(c, ts.URL, "lru"); err != nil {
		return err
	} else if code != http.StatusNotFound {
		return fmt.Errorf("stats after delete: code=%d want 404", code)
	}

	return nil
}

// ---- HTTP helpers ----

func create(c *http.Client, base, name, policy string, capacity int) error {
	code, body, err := createBody(c, base, name, policy, capacity)
	if err != nil {
		return err
	}
	if code != http.StatusCreated {
		return fmt.Errorf("create %s: code=%d body=%v", name, code, body)
	}
	if body["capacity"] != float64(capacity) || body["policy"] != policy {
		return fmt.Errorf("create %s: body=%v", name, body)
	}
	return nil
}

func createBody(c *http.Client, base, name, policy string, capacity int) (int, map[string]any, error) {
	return post(c, base+"/cache/"+name, map[string]any{"capacity": capacity, "policy": policy})
}

// put stores a value and returns the evicted key pointer (nil if none).
func put(c *http.Client, base, name, key, value string, ttl int) (*string, error) {
	buf, err := json.Marshal(map[string]any{"value": value, "ttl": ttl})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPut, base+"/cache/"+name+"/"+key, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("put %s/%s: code=%d body=%v", name, key, resp.StatusCode, out)
	}
	if out["evicted"] == nil {
		return nil, nil
	}
	s, ok := out["evicted"].(string)
	if !ok {
		return nil, fmt.Errorf("put %s/%s: evicted not a string: %v", name, key, out["evicted"])
	}
	return &s, nil
}

func get(c *http.Client, base, name, key string) (int, map[string]any, error) {
	return getReq(c, base+"/cache/"+name+"/"+key)
}

func stats(c *http.Client, base, name string) (int, map[string]any, error) {
	return getReq(c, base+"/cache/"+name)
}

func delInstance(c *http.Client, base, name string) (int, error) {
	req, err := http.NewRequest(http.MethodDelete, base+"/cache/"+name, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func post(c *http.Client, url string, body any) (int, map[string]any, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	return resp.StatusCode, out, nil
}

func getReq(c *http.Client, url string) (int, map[string]any, error) {
	resp, err := c.Get(url)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	return resp.StatusCode, out, nil
}

// putRaw performs a PUT and returns the raw status code without treating a
// non-200 status as an error. Used for negative tests (e.g. missing instance).
func putRaw(c *http.Client, base, name, key, value string, ttl int) (int, map[string]any, error) {
	buf, err := json.Marshal(map[string]any{"value": value, "ttl": ttl})
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPut, base+"/cache/"+name+"/"+key, bytes.NewReader(buf))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	return resp.StatusCode, out, nil
}
