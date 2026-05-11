package main

import (
	"sync"
	"time"
)

// CacheEntry holds a pre-synthesized utterance keyed by caller-supplied ID.
// State machine: preparing → ready → playing → (removed) or → failed.
type CacheEntry struct {
	State      string // "preparing" | "ready" | "playing" | "failed"
	Text       string
	Sid        int
	Speed      float64
	PCM        []int16
	SampleRate int
	Err        string
	CreatedAt  time.Time
	ReadyAt    time.Time
}

// Cache is a process-local store of pre-rendered PCM, indexed by caller ID.
// Entries are removed automatically after a successful Play (unless keep=true)
// and via DELETE /cache/<id> for manual eviction.
type Cache struct {
	mu sync.RWMutex
	m  map[string]*CacheEntry
}

func NewCache() *Cache {
	return &Cache{m: make(map[string]*CacheEntry)}
}

// GetOrCreate atomically returns the existing entry, or inserts a placeholder
// in "preparing" state and returns (entry, true) — true means the caller is
// responsible for filling it in.
func (c *Cache) GetOrCreate(id, text string, sid int, speed float64) (*CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[id]; ok {
		return e, false
	}
	e := &CacheEntry{
		State:     "preparing",
		Text:      text,
		Sid:       sid,
		Speed:     speed,
		CreatedAt: time.Now(),
	}
	c.m[id] = e
	return e, true
}

func (c *Cache) MarkReady(id string, pcm []int16, sr int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[id]; ok {
		e.PCM = pcm
		e.SampleRate = sr
		e.State = "ready"
		e.ReadyAt = time.Now()
	}
}

func (c *Cache) MarkFailed(id, errMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[id]; ok {
		e.State = "failed"
		e.Err = errMsg
	}
}

// TakeForPlay returns a copy of the PCM/SR if the entry is ready, and
// transitions state to "playing". Caller must call EndPlay afterwards.
func (c *Cache) TakeForPlay(id string) (pcm []int16, sr int, state string, errMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	if !ok {
		return nil, 0, "missing", ""
	}
	switch e.State {
	case "ready":
		e.State = "playing"
		return e.PCM, e.SampleRate, "ready", ""
	case "preparing":
		return nil, 0, "preparing", ""
	case "failed":
		return nil, 0, "failed", e.Err
	case "playing":
		return nil, 0, "playing", "another play in progress"
	default:
		return nil, 0, e.State, ""
	}
}

// EndPlay finalizes a play. If remove=true (default) the entry is dropped.
// If remove=false the entry returns to "ready" so it can be played again.
func (c *Cache) EndPlay(id string, remove bool, playErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if remove {
		delete(c.m, id)
		return
	}
	if e, ok := c.m[id]; ok {
		if playErr != nil {
			e.State = "ready" // recover so caller can retry
		} else {
			e.State = "ready"
		}
	}
}

// Delete removes an entry. Returns true if it existed.
func (c *Cache) Delete(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.m[id]
	delete(c.m, id)
	return ok
}

// List returns a snapshot of all entries for the GET /cache endpoint.
func (c *Cache) List() []map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]map[string]any, 0, len(c.m))
	for id, e := range c.m {
		item := map[string]any{
			"id":         id,
			"state":      e.State,
			"text":       e.Text,
			"created_at": e.CreatedAt.Format(time.RFC3339),
		}
		if len(e.PCM) > 0 && e.SampleRate > 0 {
			item["duration_ms"] = int64(len(e.PCM)) * 1000 / int64(e.SampleRate)
			item["bytes"] = len(e.PCM) * 2
		}
		if e.Err != "" {
			item["error"] = e.Err
		}
		out = append(out, item)
	}
	return out
}
