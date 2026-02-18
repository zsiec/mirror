package security

// Memory and size limits to prevent DoS attacks
const (
	// Maximum sizes for various components
	MaxNALUnitSize   = 10 * 1024 * 1024  // 10MB max for a single NAL unit
	MaxFrameSize     = 50 * 1024 * 1024  // 50MB max for a complete frame
	MaxNALBufferSize = 100 * 1024 * 1024 // 100MB max NAL buffer accumulation
	MaxFragmentSize  = 10 * 1024 * 1024  // 10MB max for a single fragment
	MaxPacketSize    = 65536             // 64KB max for a single packet

	// Maximum counts to prevent infinite loops
	MaxNALUnitsPerFrame = 1000 // Max NAL units in a single frame
	MaxOBUsPerFrame     = 500  // Max OBUs in AV1 frame
	MaxHEVCParamArrays  = 16   // Max parameter set arrays in HEVC descriptor (VPS+SPS+PPS+SEI+extras)

	// Parsing limits
	MaxParseDepth  = 10 // Max recursion depth for parsing
	MaxLEB128Bytes = 10 // Max bytes for LEB128 encoding (enough for uint64)

	// Time limits
	MaxAssemblyTimeout = 5 // 5 seconds max to assemble a frame
)

// Error messages
const (
	ErrMsgNALUnitTooLarge = "NAL unit size exceeds maximum allowed: %d > %d"
	ErrMsgFrameTooLarge   = "frame size exceeds maximum allowed: %d > %d"
	ErrMsgBufferTooLarge  = "buffer size exceeds maximum allowed: %d > %d"
	ErrMsgTooManyNALUnits = "too many NAL units in frame: %d > %d"
	ErrMsgInvalidBounds   = "invalid bounds: offset %d + size %d > buffer length %d"
)
