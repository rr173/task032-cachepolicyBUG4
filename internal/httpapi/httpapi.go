// Package httpapi exposes the cache service over HTTP+JSON.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"task032-cachepolicy/internal/cache"
)

// Server holds the in-memory collection of named cache instances.
type Server struct {
	mu     sync.Mutex
	caches map[string]*cache.Cache
}

// New returns a Server with an empty instance collection.
func New() *Server {
	return &Server{caches: make(map[string]*cache.Cache)}
}

// Handler returns the HTTP handler serving the cache API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/cache/", s.handle)
	return mux
}

// ---- request / response types ----

type createReq struct {
	Capacity int    `json:"capacity"`
	Policy   string `json:"policy"`
}

type createResp struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
	Policy   string `json:"policy"`
}

type putReq struct {
	Value string `json:"value"`
	TTL   int    `json:"ttl"` // seconds; <=0 means never expire
}

type putResp struct {
	Key     string  `json:"key"`
	Stored  bool    `json:"stored"`
	Evicted *string `json:"evicted"` // null when nothing was evicted
}

type getResp struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Hit   bool   `json:"hit"`
}

type deleteKeyResp struct {
	Key     string `json:"key"`
	Deleted bool   `json:"deleted"`
}

type statsResp struct {
	Name      string `json:"name"`
	Capacity  int    `json:"capacity"`
	Policy    string `json:"policy"`
	Size      int    `json:"size"`
	Hits      int64  `json:"hits"`
	Misses    int64  `json:"misses"`
	Evictions int64  `json:"evictions"`
}

type errResp struct {
	Error string `json:"error"`
}

// ---- routing ----

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/cache/")
	if rest == "" || strings.Contains(rest, "//") {
		writeJSON(w, http.StatusNotFound, errResp{Error: "not found"})
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	name := parts[0]
	var sub string
	if len(parts) == 2 {
		sub = parts[1]
	}
	if strings.Contains(sub, "/") {
		writeJSON(w, http.StatusNotFound, errResp{Error: "not found"})
		return
	}

	switch {
	case sub == "" && r.Method == http.MethodPost:
		s.create(w, r, name)
	case sub == "" && r.Method == http.MethodGet:
		s.stats(w, r, name)
	case sub == "" && r.Method == http.MethodDelete:
		s.deleteInstance(w, r, name)
	case sub != "" && r.Method == http.MethodPut:
		s.put(w, r, name, sub)
	case sub != "" && r.Method == http.MethodGet:
		s.get(w, r, name, sub)
	case sub != "" && r.Method == http.MethodDelete:
		s.deleteKey(w, r, name, sub)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errResp{Error: "method not allowed"})
	}
}

func (s *Server) create(w http.ResponseWriter, r *http.Request, name string) {
	var req createReq
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp{Error: "invalid request body"})
		return
	}
	if req.Capacity < 1 {
		writeJSON(w, http.StatusBadRequest, errResp{Error: "capacity must be positive"})
		return
	}
	pol, ok := cache.ParsePolicy(req.Policy)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errResp{Error: "policy must be lru or lfu"})
		return
	}

	s.mu.Lock()
	if _, ok := s.caches[name]; ok {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, errResp{Error: "cache already exists"})
		return
	}
	s.caches[name] = cache.New(req.Capacity, pol)
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, createResp{
		Name:     name,
		Capacity: req.Capacity,
		Policy:   pol.String(),
	})
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, name, key string) {
	var req putReq
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp{Error: "invalid request body"})
		return
	}
	if req.TTL < 0 {
		writeJSON(w, http.StatusBadRequest, errResp{Error: "ttl must not be negative"})
		return
	}
	c, ok := s.lookup(w, name)
	if !ok {
		return
	}
	ev := c.Put(key, req.Value, time.Duration(req.TTL)*time.Second)
	resp := putResp{Key: key, Stored: true}
	if ev != "" {
		evKey := ev
		resp.Evicted = &evKey
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, name, key string) {
	c, ok := s.lookup(w, name)
	if !ok {
		return
	}
	val, hit := c.Get(key)
	if !hit {
		writeJSON(w, http.StatusNotFound, getResp{Key: key, Hit: false})
		return
	}
	writeJSON(w, http.StatusOK, getResp{Key: key, Value: val, Hit: true})
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request, name, key string) {
	c, ok := s.lookup(w, name)
	if !ok {
		return
	}
	if !c.Delete(key) {
		writeJSON(w, http.StatusNotFound, deleteKeyResp{Key: key, Deleted: false})
		return
	}
	writeJSON(w, http.StatusOK, deleteKeyResp{Key: key, Deleted: true})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request, name string) {
	c, ok := s.lookup(w, name)
	if !ok {
		return
	}
	st := c.Stats()
	if st.Policy == "lfu" {
		st.Policy = "LFU"
	}
	writeJSON(w, http.StatusOK, statsResp{
		Name:      name,
		Capacity:  st.Capacity,
		Policy:    st.Policy,
		Size:      st.Size,
		Hits:      st.Hits,
		Misses:    st.Misses,
		Evictions: st.Evictions,
	})
}

func (s *Server) deleteInstance(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	_, ok := s.caches[name]
	if ok {
		delete(s.caches, name)
	}
	s.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, errResp{Error: "cache not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- helpers ----

// lookup fetches a cache instance by name, writing a 404 if it is missing. The
// boolean reports whether the caller should proceed.
func (s *Server) lookup(w http.ResponseWriter, name string) (*cache.Cache, bool) {
	s.mu.Lock()
	c, ok := s.caches[name]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, errResp{Error: "cache not found"})
		return nil, false
	}
	return c, true
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	defer r.Body.Close()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
