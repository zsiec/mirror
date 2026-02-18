# Security Package

The `security` package provides safety constants and LEB128 encoding/decoding for the Mirror ingestion system. It defines size limits that prevent DoS attacks through oversized data, and implements secure variable-length integer parsing used by the AV1 codec.

## Package Contents

This package contains two files:

- **`limits.go`** — Constants for maximum sizes and parsing depth
- **`leb128.go`** — LEB128 (Little Endian Base 128) encoding/decoding with overflow protection

## Size Limits

Constants enforced across the ingestion pipeline to prevent resource exhaustion:

```go
// Maximum sizes for data components
MaxNALUnitSize   = 10 * 1024 * 1024  // 10MB per NAL unit
MaxFrameSize     = 50 * 1024 * 1024  // 50MB per complete frame
MaxNALBufferSize = 100 * 1024 * 1024 // 100MB NAL buffer accumulation
MaxFragmentSize  = 10 * 1024 * 1024  // 10MB per fragment
MaxPacketSize    = 65536             // 64KB per packet

// Maximum counts to prevent infinite loops
MaxNALUnitsPerFrame  = 1000 // Max NAL units in a single frame
MaxOBUsPerFrame      = 500  // Max OBUs in AV1 frame
MaxHEVCParamArrays   = 16   // Max parameter set arrays in HEVC descriptor

// Parsing limits
MaxParseDepth  = 10 // Max recursion depth
MaxLEB128Bytes = 10 // Max bytes for LEB128 (enough for uint64)

// Time limits
MaxAssemblyTimeout = 5 // Seconds max to assemble a frame
```

### Error Message Templates

```go
ErrMsgNALUnitTooLarge = "NAL unit size exceeds maximum allowed: %d > %d"
ErrMsgFrameTooLarge   = "frame size exceeds maximum allowed: %d > %d"
ErrMsgBufferTooLarge  = "buffer size exceeds maximum allowed: %d > %d"
ErrMsgTooManyNALUnits = "too many NAL units in frame: %d > %d"
ErrMsgInvalidBounds   = "invalid bounds: offset %d + size %d > buffer length %d"
```

## LEB128 Encoding

[LEB128](https://en.wikipedia.org/wiki/LEB128) is a variable-length encoding for integers, used in the AV1 bitstream format for OBU (Open Bitstream Unit) size fields.

### Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `ReadLEB128` | `(data []byte) (uint64, int, error)` | Read LEB128 from byte slice |
| `ReadLEB128FromReader` | `(r io.Reader) (uint64, int, error)` | Read LEB128 from io.Reader |
| `WriteLEB128` | `(value uint64) []byte` | Encode uint64 as LEB128 |
| `LEB128Size` | `(value uint64) int` | Bytes needed to encode a value |

### Usage

```go
// Decode from byte slice
value, bytesRead, err := security.ReadLEB128(data)
if err != nil {
    // Handle error
}

// Decode from reader (e.g., streaming data)
value, bytesRead, err := security.ReadLEB128FromReader(reader)

// Encode
encoded := security.WriteLEB128(42)

// Calculate encoded size without allocating
size := security.LEB128Size(42) // returns 1
```

### Error Variables

```go
var ErrLEB128Overflow     = errors.New("LEB128 value overflows uint64")
var ErrLEB128TooManyBytes = errors.New("LEB128 encoding exceeds maximum allowed bytes")
var ErrLEB128Incomplete   = errors.New("LEB128 encoding is incomplete")
```

### Security Protections

The LEB128 decoder guards against:

- **Overflow**: Detects when decoded value would exceed uint64 range (special handling at shift=63)
- **Excessive length**: Rejects encodings longer than `MaxLEB128Bytes` (10 bytes, sufficient for uint64)
- **Incomplete data**: Returns error when input ends mid-encoding (continuation bit set on last byte)

## Integration

These constants are used throughout the ingestion pipeline:

- **Codec depacketizers** (`codec/`) check `MaxNALUnitSize`, `MaxOBUsPerFrame`
- **Frame assembly** (`frame/`) checks `MaxFrameSize`, `MaxNALUnitsPerFrame`
- **Validation** (`validation/`) uses limits for size validation
- **AV1 depacketizer** uses LEB128 for OBU size parsing
- **Buffer management** (`buffer/`) references `MaxPacketSize`

## Testing

```bash
source scripts/srt-env.sh && go test ./internal/ingestion/security/...
```
