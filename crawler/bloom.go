package crawler

import (
	"encoding/binary"
	"hash"
	"hash/fnv"
	"math"
	"os"
	"sync"
)

// BloomFilter is a space-efficient probabilistic set.
// False positives possible; false negatives impossible.
type BloomFilter struct {
	bits []uint64
	k    int // number of hash functions
	m    int // number of bits
	mu   sync.RWMutex
}

// NewBloomFilter creates a Bloom filter sized for n items at the given
// false positive rate. For 10K domains at 0.1% FPR: ~18KB.
func NewBloomFilter(n int, fpr float64) *BloomFilter {
	m := optimalM(n, fpr)
	k := optimalK(m, n)
	return &BloomFilter{
		bits: make([]uint64, (m+63)/64),
		k:    k,
		m:    m,
	}
}

// Add inserts an item into the filter.
func (bf *BloomFilter) Add(item string) {
	bf.mu.Lock()
	defer bf.mu.Unlock()
	h1, h2 := hashes(item)
	for i := 0; i < bf.k; i++ {
		pos := (h1 + uint64(i)*h2) % uint64(bf.m)
		bf.bits[pos/64] |= 1 << (pos % 64)
	}
}

// Contains returns true if the item might be in the set.
// False positives possible; false negatives impossible.
func (bf *BloomFilter) Contains(item string) bool {
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	h1, h2 := hashes(item)
	for i := 0; i < bf.k; i++ {
		pos := (h1 + uint64(i)*h2) % uint64(bf.m)
		if bf.bits[pos/64]&(1<<(pos%64)) == 0 {
			return false
		}
	}
	return true
}

func hashes(s string) (uint64, uint64) {
	var h hash.Hash64
	h = fnv.New64a()
	h.Write([]byte(s))
	h1 := h.Sum64()
	h = fnv.New64()
	h.Write([]byte(s))
	h2 := h.Sum64()
	return h1, h2
}

// Save writes the Bloom filter to a file. Format: m(4B) + k(4B) + bits.
func (bf *BloomFilter) Save(path string) error {
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	binary.Write(f, binary.LittleEndian, uint32(bf.m))
	binary.Write(f, binary.LittleEndian, uint32(bf.k))
	return binary.Write(f, binary.LittleEndian, bf.bits)
}

// LoadBloomFilter reads a Bloom filter from disk, seeding from text files
// if the file doesn't exist yet.
func LoadBloomFilter(path string, seeds map[string]bool, moreSeeds map[string]bool) *BloomFilter {
	if data, err := os.ReadFile(path); err == nil && len(data) >= 8 {
		m := int(binary.LittleEndian.Uint32(data[0:4]))
		k := int(binary.LittleEndian.Uint32(data[4:8]))
		bits := make([]uint64, (m+63)/64)
		if len(data) >= 8+len(bits)*8 {
			for i := range bits {
				bits[i] = binary.LittleEndian.Uint64(data[8+i*8:])
			}
			return &BloomFilter{bits: bits, k: k, m: m}
		}
	}
	// File missing or corrupt — create fresh and seed
	bf := NewBloomFilter(10000, 0.001)
	for d := range seeds {
		bf.Add(d)
	}
	for d := range moreSeeds {
		bf.Add(d)
	}
	bf.Save(path)
	return bf
}

func optimalM(n int, fpr float64) int {
	return int(math.Ceil(-float64(n) * math.Log(fpr) / (math.Log(2) * math.Log(2))))
}

func optimalK(m, n int) int {
	k := int(math.Round(float64(m) / float64(n) * math.Log(2)))
	if k < 1 {
		k = 1
	}
	return k
}
