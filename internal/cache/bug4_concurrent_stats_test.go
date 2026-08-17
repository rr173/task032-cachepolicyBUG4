package cache

import (
	"fmt"
	"sync"
	"testing"
)

func TestBug4ConcurrentStatsIsRaceFree(t *testing.T) {
	c := New(32, LRU)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Put(fmt.Sprintf("%d-%d", worker, j), "v", 0)
				_ = c.Stats()
			}
		}(i)
	}
	wg.Wait()
}
