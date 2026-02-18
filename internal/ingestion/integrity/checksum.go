package integrity

import (
	"crypto/md5"
	"encoding/binary"
	"hash"
	"hash/crc32"
	"sync"
)

// ChecksumType defines the type of checksum to use
type ChecksumType int

const (
	ChecksumCRC32 ChecksumType = iota
	ChecksumMD5
	ChecksumXXHash // Future: for performance
)

// Checksummer provides checksum calculation and verification
type Checksummer struct {
	mu           sync.RWMutex
	checksumType ChecksumType
	crcTable     *crc32.Table

	// Cache for performance
	cache      map[string]uint32
	cacheSize  int
	cacheMutex sync.RWMutex
}

// NewChecksummer creates a new checksummer
func NewChecksummer() *Checksummer {
	return &Checksummer{
		checksumType: ChecksumCRC32,
		crcTable:     crc32.MakeTable(crc32.IEEE),
		cache:        make(map[string]uint32),
		cacheSize:    1000,
	}
}

// Calculate computes checksum for data
func (c *Checksummer) Calculate(data []byte) uint32 {
	if len(data) == 0 {
		return 0
	}

	// Check cache first
	key := c.cacheKey(data)
	if cached, ok := c.getFromCache(key); ok {
		return cached
	}

	var checksum uint32
	switch c.checksumType {
	case ChecksumCRC32:
		checksum = crc32.Checksum(data, c.crcTable)
	case ChecksumMD5:
		// Use first 4 bytes of MD5 for consistency
		h := md5.Sum(data)
		checksum = binary.BigEndian.Uint32(h[:4])
	default:
		checksum = crc32.Checksum(data, c.crcTable)
	}

	c.addToCache(key, checksum)
	return checksum
}

// Verify checks if data matches expected checksum
func (c *Checksummer) Verify(data []byte, expected uint32) bool {
	actual := c.Calculate(data)
	return actual == expected
}

// CalculateIncremental computes checksum incrementally
func (c *Checksummer) CalculateIncremental(chunks [][]byte) uint32 {
	if len(chunks) == 0 {
		return 0
	}

	h := crc32.New(c.crcTable)
	for _, chunk := range chunks {
		h.Write(chunk)
	}

	return h.Sum32()
}

// SetType changes the checksum algorithm
func (c *Checksummer) SetType(t ChecksumType) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.checksumType = t
	c.clearCache()
}

// Private methods

func (c *Checksummer) cacheKey(data []byte) string {
	// Use length and first/last bytes as key for performance
	if len(data) < 8 {
		return string(data)
	}

	key := make([]byte, 16)
	binary.BigEndian.PutUint64(key[0:8], uint64(len(data)))
	copy(key[8:12], data[:4])
	copy(key[12:16], data[len(data)-4:])
	return string(key)
}

func (c *Checksummer) getFromCache(key string) (uint32, bool) {
	c.cacheMutex.RLock()
	defer c.cacheMutex.RUnlock()

	val, ok := c.cache[key]
	return val, ok
}

func (c *Checksummer) addToCache(key string, value uint32) {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()

	// Simple cache eviction
	if len(c.cache) >= c.cacheSize {
		// Remove first item (simple strategy)
		for k := range c.cache {
			delete(c.cache, k)
			break
		}
	}

	c.cache[key] = value
}

func (c *Checksummer) clearCache() {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()

	c.cache = make(map[string]uint32)
}

// StreamChecksum provides streaming checksum calculation
type StreamChecksum struct {
	h    hash.Hash32
	size uint64
}

// NewStreamChecksum creates a new streaming checksum calculator
func NewStreamChecksum() *StreamChecksum {
	return &StreamChecksum{
		h: crc32.NewIEEE(),
	}
}

// Update adds more data to the checksum
func (sc *StreamChecksum) Update(data []byte) {
	sc.h.Write(data)
	sc.size += uint64(len(data))
}

// Sum returns the current checksum
func (sc *StreamChecksum) Sum() uint32 {
	return sc.h.Sum32()
}

// Reset clears the checksum state
func (sc *StreamChecksum) Reset() {
	sc.h.Reset()
	sc.size = 0
}

// Size returns total bytes processed
func (sc *StreamChecksum) Size() uint64 {
	return sc.size
}
