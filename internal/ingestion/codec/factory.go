package codec

import (
	"fmt"
	"sync"
	
	"github.com/zsiec/mirror/internal/ingestion/memory"
)

// DepacketizerFactory creates depacketizers for different codecs
type DepacketizerFactory struct {
	mu            sync.RWMutex
	registry      map[Type]func(streamID string) Depacketizer
	memController *memory.Controller
	codecLimits   map[Type]int64
}

// NewDepacketizerFactory creates a new depacketizer factory with default codecs
func NewDepacketizerFactory(memController *memory.Controller) *DepacketizerFactory {
	f := &DepacketizerFactory{
		registry:      make(map[Type]func(streamID string) Depacketizer),
		memController: memController,
		codecLimits: map[Type]int64{
			TypeH264:   10 * 1024 * 1024,  // 10MB
			TypeHEVC:   15 * 1024 * 1024,  // 15MB
			TypeAV1:    12 * 1024 * 1024,  // 12MB
			TypeJPEGXS: 5 * 1024 * 1024,   // 5MB
		},
	}
	
	// Register default depacketizers
	f.RegisterDefaults()
	
	return f
}

// RegisterDefaults registers all built-in depacketizers
func (f *DepacketizerFactory) RegisterDefaults() {
	// If memory controller is nil, use regular depacketizers
	if f.memController == nil {
		f.Register(TypeHEVC, func(streamID string) Depacketizer {
			return NewHEVCDepacketizer()
		})
		
		f.Register(TypeH264, func(streamID string) Depacketizer {
			return NewH264Depacketizer()
		})
		
		f.Register(TypeAV1, func(streamID string) Depacketizer {
			return NewAV1Depacketizer()
		})
		
		f.Register(TypeJPEGXS, func(streamID string) Depacketizer {
			return NewJPEGXSDepacketizer()
		})
		return
	}
	
	// Register memory-aware depacketizers
	f.Register(TypeHEVC, func(streamID string) Depacketizer {
		return NewHEVCDepacketizerWithMemory(streamID, f.memController, f.codecLimits[TypeHEVC])
	})
	
	f.Register(TypeH264, func(streamID string) Depacketizer {
		return NewH264DepacketizerWithMemory(streamID, f.memController, f.codecLimits[TypeH264])
	})
	
	f.Register(TypeAV1, func(streamID string) Depacketizer {
		return NewAV1DepacketizerWithMemory(streamID, f.memController, f.codecLimits[TypeAV1])
	})
	
	f.Register(TypeJPEGXS, func(streamID string) Depacketizer {
		return NewJPEGXSDepacketizerWithMemory(streamID, f.memController, f.codecLimits[TypeJPEGXS])
	})
}

// Register adds a depacketizer creator for a codec type
func (f *DepacketizerFactory) Register(codecType Type, creator func(streamID string) Depacketizer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	
	f.registry[codecType] = creator
}

// Create creates a new depacketizer for the given codec type and stream
func (f *DepacketizerFactory) Create(codecType Type, streamID string) (Depacketizer, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	
	creator, ok := f.registry[codecType]
	if !ok {
		return nil, fmt.Errorf("unsupported codec type: %s", codecType)
	}
	
	return creator(streamID), nil
}

// IsSupported checks if a codec type is supported
func (f *DepacketizerFactory) IsSupported(codecType Type) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	
	_, ok := f.registry[codecType]
	return ok
}

// SupportedCodecs returns a list of supported codec types
func (f *DepacketizerFactory) SupportedCodecs() []Type {
	f.mu.RLock()
	defer f.mu.RUnlock()
	
	codecs := make([]Type, 0, len(f.registry))
	for codecType := range f.registry {
		codecs = append(codecs, codecType)
	}
	
	return codecs
}

// Unregister removes a codec type from the factory
func (f *DepacketizerFactory) Unregister(codecType Type) {
	f.mu.Lock()
	defer f.mu.Unlock()
	
	delete(f.registry, codecType)
}

// DepacketizerPool manages a pool of depacketizers for each codec type
type DepacketizerPool struct {
	factory *DepacketizerFactory
	pools   map[Type]*sync.Pool
	mu      sync.RWMutex
}

// NewDepacketizerPool creates a new depacketizer pool
func NewDepacketizerPool(factory *DepacketizerFactory) *DepacketizerPool {
	return &DepacketizerPool{
		factory: factory,
		pools:   make(map[Type]*sync.Pool),
	}
}

// Get retrieves a depacketizer from the pool or creates a new one
func (p *DepacketizerPool) Get(codecType Type, streamID string) (Depacketizer, error) {
	// Note: Pooling is disabled when using memory controller
	// Each stream needs its own depacketizer instance with memory tracking
	return p.factory.Create(codecType, streamID)
}

// Put returns a depacketizer to the pool
func (p *DepacketizerPool) Put(codecType Type, depacketizer Depacketizer) {
	p.mu.RLock()
	pool, ok := p.pools[codecType]
	p.mu.RUnlock()
	
	if ok {
		// Reset before returning to pool
		if d, ok := depacketizer.(interface{ Reset() }); ok {
			d.Reset()
		}
		pool.Put(depacketizer)
	}
}
