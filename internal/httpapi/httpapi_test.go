package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	s := New()
	ts := httptest.NewServer(s.Handler())
	return ts, s
}

func doRequest(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	return resp.StatusCode, out
}

func mustCreate(t *testing.T, base, name, policy string, capacity int) {
	t.Helper()
	code, body := doRequest(t, http.MethodPost, base+"/cache/"+name, map[string]any{"capacity": capacity, "policy": policy})
	if code != http.StatusCreated {
		t.Fatalf("create %s: got %d body=%v", name, code, body)
	}
}

func mustPut(t *testing.T, base, name, key, value string, ttl int) map[string]any {
	t.Helper()
	code, body := doRequest(t, http.MethodPut, base+"/cache/"+name+"/"+key, map[string]any{"value": value, "ttl": ttl})
	if code != http.StatusOK {
		t.Fatalf("put %s/%s: got %d body=%v", name, key, code, body)
	}
	return body
}

func TestCreateAndStats(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	code, body := doRequest(t, http.MethodPost, ts.URL+"/cache/c", map[string]any{"capacity": 5, "policy": "lru"})
	if code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201", code)
	}
	if body["capacity"] != float64(5) || body["policy"] != "lru" {
		t.Errorf("create body = %v", body)
	}

	code, body = doRequest(t, http.MethodGet, ts.URL+"/cache/c", nil)
	if code != http.StatusOK {
		t.Fatalf("stats: got %d", code)
	}
	if body["size"] != float64(0) || body["capacity"] != float64(5) || body["policy"] != "lru" {
		t.Errorf("stats body = %v", body)
	}
}

func TestCreateCapacityZero(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	code, body := doRequest(t, http.MethodPost, ts.URL+"/cache/bad", map[string]any{"capacity": 0, "policy": "lru"})
	if code != http.StatusBadRequest {
		t.Fatalf("capacity=0: got %d, want 400", code)
	}
	if body["error"] != "capacity must be positive" {
		t.Errorf("capacity=0 error = %v", body["error"])
	}
}

func TestCreateBadPolicy(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	code, body := doRequest(t, http.MethodPost, ts.URL+"/cache/bad", map[string]any{"capacity": 4, "policy": "random"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad policy: got %d, want 400", code)
	}
	if body["error"] != "policy must be lru or lfu" {
		t.Errorf("bad policy error = %v", body["error"])
	}
}

func TestCreateConflict(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	mustCreate(t, ts.URL, "dup", "lru", 2)
	code, body := doRequest(t, http.MethodPost, ts.URL+"/cache/dup", map[string]any{"capacity": 2, "policy": "lru"})
	if code != http.StatusConflict {
		t.Fatalf("conflict: got %d, want 409", code)
	}
	if body["error"] != "cache already exists" {
		t.Errorf("conflict error = %v", body["error"])
	}
}

func TestPutEviction(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	mustCreate(t, ts.URL, "c", "lru", 2)

	if ev := mustPut(t, ts.URL, "c", "a", "1", 0)["evicted"]; ev != nil {
		t.Errorf("put a: evicted=%v want null", ev)
	}
	mustPut(t, ts.URL, "c", "b", "2", 0)
	body := mustPut(t, ts.URL, "c", "c", "3", 0)
	if body["evicted"] != "a" {
		t.Errorf("put c: evicted=%v want a", body["evicted"])
	}

	code, body := doRequest(t, http.MethodGet, ts.URL+"/cache/c/a", nil)
	if code != http.StatusNotFound {
		t.Errorf("get a after evict: got %d, want 404", code)
	}
	if body["hit"] != false {
		t.Errorf("get a hit field = %v, want false", body["hit"])
	}

	code, body = doRequest(t, http.MethodGet, ts.URL+"/cache/c/b", nil)
	if code != http.StatusOK || body["value"] != "2" {
		t.Errorf("get b: code=%d body=%v", code, body)
	}
}

func TestPutUpdateNoEviction(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	mustCreate(t, ts.URL, "c", "lru", 2)
	mustPut(t, ts.URL, "c", "a", "1", 0)
	mustPut(t, ts.URL, "c", "b", "2", 0)
	body := mustPut(t, ts.URL, "c", "a", "10", 0)
	if body["evicted"] != nil {
		t.Errorf("update a: evicted=%v want null", body["evicted"])
	}
	code, body := doRequest(t, http.MethodGet, ts.URL+"/cache/c/a", nil)
	if code != http.StatusOK || body["value"] != "10" {
		t.Errorf("get updated a: code=%d body=%v", code, body)
	}
}

func TestDeleteKey(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	mustCreate(t, ts.URL, "c", "lru", 2)
	mustPut(t, ts.URL, "c", "a", "1", 0)

	code, body := doRequest(t, http.MethodDelete, ts.URL+"/cache/c/a", nil)
	if code != http.StatusOK || body["deleted"] != true {
		t.Fatalf("delete a: code=%d body=%v", code, body)
	}
	code, body = doRequest(t, http.MethodDelete, ts.URL+"/cache/c/a", nil)
	if code != http.StatusNotFound || body["deleted"] != false {
		t.Fatalf("delete a again: code=%d body=%v", code, body)
	}
}

func TestMissingInstance(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		if code, _ := doRequest(t, m, ts.URL+"/cache/nope/k", map[string]any{"value": "x", "ttl": 0}); code != http.StatusNotFound {
			t.Errorf("%s nope: got %d, want 404", m, code)
		}
	}
	if code, _ := doRequest(t, http.MethodGet, ts.URL+"/cache/nope", nil); code != http.StatusNotFound {
		t.Errorf("stats nope: got %d, want 404", code)
	}
	if code, _ := doRequest(t, http.MethodDelete, ts.URL+"/cache/nope", nil); code != http.StatusNotFound {
		t.Errorf("delete nope: got %d, want 404", code)
	}
}

func TestDeleteInstance(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	mustCreate(t, ts.URL, "c", "lru", 2)
	code, _ := doRequest(t, http.MethodDelete, ts.URL+"/cache/c", nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete instance: got %d, want 204", code)
	}
	code, _ = doRequest(t, http.MethodGet, ts.URL+"/cache/c", nil)
	if code != http.StatusNotFound {
		t.Errorf("stats after delete: got %d, want 404", code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	mustCreate(t, ts.URL, "c", "lru", 2)
	// POST to a key path is not a defined route.
	if code, _ := doRequest(t, http.MethodPost, ts.URL+"/cache/c/k", map[string]any{"value": "x"}); code != http.StatusMethodNotAllowed {
		t.Errorf("post key: got %d, want 405", code)
	}
}

func TestKeyContainingSlashIsRejected(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	mustCreate(t, ts.URL, "c", "lru", 2)
	code, _ := doRequest(t, http.MethodPut, ts.URL+"/cache/c/a/b", map[string]any{"value": "x", "ttl": 0})
	if code != http.StatusNotFound {
		t.Fatalf("key containing slash: got %d, want 404", code)
	}
}
