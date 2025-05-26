package types

import (
	"container/list"
	"sync"
	"time"
)

// ParameterSetCache implements TTL and LRU eviction for parameter sets
// This handles encoder restarts, bitrate adaptation, and configuration changes
type ParameterSetCache struct {
	mu      sync.RWMutex
	maxSize int
	ttl     time.Duration

	// LRU tracking
	order *list.List
	items map[string]*cacheItem

	// TTL tracking
	ticker *time.Ticker
	stopCh chan struct{}

	// Statistics
	hits    uint64
	misses  uint64
	evicted uint64
	expired uint64
}

type cacheItem struct {
	key         string
	value       *ParameterSet
	element     *list.Element
	expiresAt   time.Time
	accessCount uint64
}

// NewParameterSetCache creates a production-quality parameter set cache
func NewParameterSetCache(maxSize int, ttl time.Duration) *ParameterSetCache {
	cache := &ParameterSetCache{
		maxSize: maxSize,
		ttl:     ttl,
		order:   list.New(),
		items:   make(map[string]*cacheItem),
		ticker:  time.NewTicker(ttl / 4),
		stopCh:  make(chan struct{}),
	}

	go cache.cleanupExpired()

	return cache
}

// Get retrieves a parameter set with LRU promotion
func (c *ParameterSetCache) Get(key string) (*ParameterSet, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, exists := c.items[key]
	if !exists {
		c.misses++
		return nil, false
	}

	// Check TTL
	if time.Now().After(item.expiresAt) {
		c.removeItem(item)
		c.expired++
		c.misses++
		return nil, false
	}

	// Update LRU and access count
	c.order.MoveToFront(item.element)
	item.accessCount++
	c.hits++

	return item.value, true
}

// Put stores a parameter set with TTL and LRU eviction
func (c *ParameterSetCache) Put(key string, value *ParameterSet) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing item
	if item, exists := c.items[key]; exists {
		item.value = value
		item.expiresAt = time.Now().Add(c.ttl)
		c.order.MoveToFront(item.element)
		return
	}

	// Create new item
	item := &cacheItem{
		key:         key,
		value:       value,
		expiresAt:   time.Now().Add(c.ttl),
		accessCount: 1,
	}

	item.element = c.order.PushFront(item)
	c.items[key] = item

	// Evict LRU if over capacity
	if c.order.Len() > c.maxSize {
		c.evictLRU()
	}
}

// evictLRU removes the least recently used item
func (c *ParameterSetCache) evictLRU() {
	if c.order.Len() == 0 {
		return
	}

	oldest := c.order.Back()
	if oldest != nil {
		item := oldest.Value.(*cacheItem)
		c.removeItem(item)
		c.evicted++
	}
}

// removeItem removes an item from both structures
func (c *ParameterSetCache) removeItem(item *cacheItem) {
	c.order.Remove(item.element)
	delete(c.items, item.key)
}

// cleanupExpired runs background cleanup of expired items
func (c *ParameterSetCache) cleanupExpired() {
	for {
		select {
		case <-c.ticker.C:
			c.mu.Lock()
			now := time.Now()

			// Walk from back (oldest) and remove expired items
			for elem := c.order.Back(); elem != nil; {
				item := elem.Value.(*cacheItem)
				if now.After(item.expiresAt) {
					next := elem.Prev()
					c.removeItem(item)
					c.expired++
					elem = next
				} else {
					// Items are ordered by access time, so we can stop here
					break
				}
			}
			c.mu.Unlock()

		case <-c.stopCh:
			return
		}
	}
}

// GetStatistics returns cache performance metrics
func (c *ParameterSetCache) GetStatistics() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hitRate := float64(0)
	if c.hits+c.misses > 0 {
		hitRate = float64(c.hits) / float64(c.hits+c.misses)
	}

	return map[string]interface{}{
		"size":        c.order.Len(),
		"max_size":    c.maxSize,
		"hits":        c.hits,
		"misses":      c.misses,
		"hit_rate":    hitRate,
		"evicted":     c.evicted,
		"expired":     c.expired,
		"ttl_seconds": c.ttl.Seconds(),
	}
}

// Close stops background cleanup
func (c *ParameterSetCache) Close() {
	close(c.stopCh)
	c.ticker.Stop()
}
