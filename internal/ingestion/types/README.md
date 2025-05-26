# Types Package

The types package defines the core data structures and types used throughout the video ingestion pipeline. These types provide a common vocabulary for representing video frames, GOPs, packets, codecs, and timing information.

## Overview

This package serves as the foundational layer that defines:

- **Video Data Structures**: Frames, GOPs, NAL units
- **Codec Definitions**: Supported video/audio codecs with metadata
- **Packet Representations**: Network packets with timing and metadata
- **Timing Utilities**: Rational numbers for precise time calculations

## Core Data Structures

### VideoFrame

The `VideoFrame` struct represents a complete decoded video frame with comprehensive metadata:

```go
type VideoFrame struct {
    // Identification
    ID          uint64
    StreamID    string
    FrameNumber uint64
    
    // Frame data
    NALUnits  []NALUnit
    TotalSize int
    
    // Timing information
    PTS          int64     // Presentation timestamp
    DTS          int64     // Decode timestamp  
    Duration     int64     // Frame duration
    CaptureTime  time.Time // Network reception time
    CompleteTime time.Time // Assembly completion time
    
    // Frame classification
    Type  FrameType  // I, P, B, IDR, SPS, PPS, etc.
    Flags FrameFlags // Keyframe, reference, corrupted, etc.
    
    // GOP context
    GOPId       uint64
    GOPPosition int
    
    // Dependencies
    References   []uint64 // Frames this depends on
    ReferencedBy []uint64 // Frames that depend on this
    
    // Quality metrics
    QP   int     // Quantization parameter
    PSNR float64 // Peak signal-to-noise ratio
}
```

### GOP (Group of Pictures)

The `GOP` struct represents a group of video frames with structural information:

```go
type GOP struct {
    // Identification
    ID          uint64
    StreamID    string
    SequenceNum uint32
    
    // Frame collection
    Frames   []*VideoFrame
    Keyframe *VideoFrame
    
    // Timing
    StartPTS  int64
    EndPTS    int64
    Duration  int64
    StartTime time.Time
    EndTime   time.Time
    
    // Structure analysis
    Structure GOPStructure // Pattern, size, open/closed
    Complete  bool
    Closed    bool
    
    // Statistics
    TotalSize    int64
    BitRate      int64
    PFrameCount  int
    BFrameCount  int
    
    // Quality tracking
    DroppedFrames   int
    CorruptedFrames int
    AvgQP           float64
}
```

### TimestampedPacket

The `TimestampedPacket` struct represents network packets with full timing context:

```go
type TimestampedPacket struct {
    // Raw data
    Data []byte
    
    // Timing
    CaptureTime time.Time // Network arrival
    PTS         int64     // Presentation timestamp
    DTS         int64     // Decode timestamp
    Duration    int64     // Packet duration
    
    // Network context
    StreamID string
    SSRC     uint32 // RTP SSRC
    SeqNum   uint16 // RTP sequence number
    
    // Classification
    Type  PacketType  // Video/Audio/Data
    Codec CodecType   // H.264, HEVC, etc.
    Flags PacketFlags // Frame boundaries, priority
    
    // Frame context
    FrameNumber   uint64
    PacketInFrame int
    TotalPackets  int
}
```

## Frame Types and Classification

### FrameType Enumeration

```go
const (
    FrameTypeI   // Intra-coded (keyframe)
    FrameTypeP   // Predictive
    FrameTypeB   // Bidirectional
    FrameTypeIDR // Instantaneous Decoder Refresh
    FrameTypeSPS // Sequence Parameter Set
    FrameTypePPS // Picture Parameter Set
    FrameTypeVPS // Video Parameter Set (HEVC)
    FrameTypeSEI // Supplemental Enhancement Info
    FrameTypeAUD // Access Unit Delimiter
)
```

### Frame Classification Methods

```go
// Type-based checks
func (f FrameType) IsKeyframe() bool
func (f FrameType) IsReference() bool 
func (f FrameType) IsDiscardable() bool

// Instance-based checks
func (f *VideoFrame) IsKeyframe() bool
func (f *VideoFrame) IsReference() bool
func (f *VideoFrame) CanDrop() bool
func (f *VideoFrame) IsCorrupted() bool
```

## Codec Support

### Supported Codecs

The package defines comprehensive codec support:

```go
const (
    // Modern video codecs
    CodecH264   // Most widely supported
    CodecHEVC   // H.265, higher efficiency
    CodecAV1    // Next-generation, best compression
    CodecJPEGXS // Low-latency, high-quality
    
    // Audio codecs
    CodecAAC    // Advanced Audio Coding
    CodecOpus   // Modern, efficient
    CodecG711   // Traditional telephony
    CodecG722   // Wideband audio
    
    // Legacy/specialized
    CodecVP8, CodecVP9  // WebRTC
    CodecH261, CodecH263 // Legacy video
    CodecMP2T           // MPEG Transport Stream
)
```

### Codec Metadata

```go
// Codec classification
func (c CodecType) IsVideo() bool
func (c CodecType) IsAudio() bool

// RTP clock rates
func (c CodecType) GetClockRate() uint32
func GetClockRateForPayloadType(payloadType uint8) uint32
```

## Timing and Synchronization

### Rational Numbers

Precise timing calculations using rational arithmetic:

```go
type Rational struct {
    Num int // Numerator
    Den int // Denominator
}

// Common time bases
var (
    TimeBase90kHz = Rational{1, 90000} // Video RTP
    TimeBase48kHz = Rational{1, 48000} // Audio
    FrameRate29_97 = Rational{30000, 1001} // NTSC
)
```

### NAL Units

Network Abstraction Layer units for video codecs:

```go
type NALUnit struct {
    Type       uint8  // NAL unit type (codec-specific)
    Data       []byte // Complete NAL unit
    Importance uint8  // Priority level (0-5)
    RefIdc     uint8  // Reference indicator (H.264)
}
```

## Flag Systems

### Frame Flags

```go
const (
    FrameFlagKeyframe    // I-frame or IDR
    FrameFlagReference   // Used as reference by other frames
    FrameFlagCorrupted   // Data corruption detected
    FrameFlagDroppable   // Safe to drop
    FrameFlagLastInGOP   // Final frame of GOP
    FrameFlagSceneChange // Scene change detected
    FrameFlagInterlaced  // Interlaced content
)
```

### Packet Flags

```go
const (
    PacketFlagKeyframe    // Contains keyframe data
    PacketFlagFrameStart  // First packet of frame
    PacketFlagFrameEnd    // Last packet of frame
    PacketFlagDiscardable // Can be dropped (B-frame)
    PacketFlagGOPStart    // Start of new GOP
    PacketFlagCorrupted   // Corruption detected
)
```

## Usage Examples

### Frame Processing

```go
// Process incoming frame
func processFrame(frame *VideoFrame) {
    // Check frame type
    if frame.IsKeyframe() {
        log.Info("Processing keyframe")
        setupNewGOP(frame)
    }
    
    // Validate frame integrity
    if frame.IsCorrupted() {
        if frame.CanDrop() {
            return // Skip corrupted B-frame
        } else {
            requestRetransmission(frame)
        }
    }
    
    // Extract codec parameters
    if sps := frame.GetNALUnitByType(7); sps != nil {
        updateCodecConfig(sps.Data)
    }
}
```

### GOP Management

```go
// Build GOP from frames
func buildGOP(frames []*VideoFrame) *GOP {
    gop := &GOP{
        ID:       generateGOPID(),
        StreamID: frames[0].StreamID,
    }
    
    for _, frame := range frames {
        gop.AddFrame(frame)
    }
    
    gop.CalculateStats()
    
    // Check GOP structure
    if gop.HasCompleteReferences() {
        gop.Closed = true
    }
    
    return gop
}
```

### Packet Classification

```go
// Process network packet
func processPacket(packet *TimestampedPacket) {
    // Check codec type
    switch packet.Codec {
    case CodecH264:
        processH264Packet(packet)
    case CodecHEVC:
        processHEVCPacket(packet)
    case CodecAV1:
        processAV1Packet(packet)
    }
    
    // Handle frame boundaries
    if packet.IsFrameStart() {
        startNewFrame(packet)
    }
    if packet.IsFrameEnd() {
        completeFrame(packet)
    }
}
```

### Timing Calculations

```go
// Convert between time bases
func convertTimestamp(pts int64, from, to Rational) int64 {
    // Convert from source timebase to seconds, then to target
    seconds := float64(pts) * from.Float64()
    return int64(seconds / to.Float64())
}

// Calculate frame rate
func calculateFrameRate(gop *GOP) float64 {
    if gop.Duration == 0 {
        return 0
    }
    frameCount := float64(len(gop.Frames))
    durationSec := float64(gop.Duration) / 90000.0
    return frameCount / durationSec
}
```

## Statistics and Quality Metrics

### Frame-Level Metrics

```go
// Calculate frame bitrate
bitrate := frame.CalculateBitrate() // bps

// Check frame dependencies
refFrames := len(frame.References)
dependentFrames := len(frame.ReferencedBy)
```

### GOP-Level Metrics

```go
// GOP analysis
gop.CalculateStats()

fmt.Printf("GOP Pattern: %s\n", gop.Structure.Pattern)
fmt.Printf("Bitrate: %d bps\n", gop.BitRate)
fmt.Printf("Avg QP: %.2f\n", gop.AvgQP)
fmt.Printf("B-frame ratio: %.2f\n", 
    float64(gop.BFrameCount)/float64(len(gop.Frames)))
```

## Thread Safety

### Immutable by Design
Most types are designed to be immutable after creation:
- Frame data should not be modified after assembly
- GOP structure is built incrementally then finalized
- Timing information is set once during packet processing

### Safe Modification Patterns
```go
// Safe flag modification
frame.SetFlag(FrameFlagCorrupted)
if frame.HasFlag(FrameFlagKeyframe) {
    // Handle keyframe
}

// Safe packet cloning
clonedPacket := packet.Clone()
```

## Performance Considerations

### Memory Efficiency
- Uses slices for variable-length data (NAL units, references)
- Minimal heap allocations in hot paths
- Buffer pooling for packet data recommended

### CPU Efficiency
- Bitwise operations for flags
- Pre-calculated codec metadata
- O(1) lookups for common operations

## Error Handling

### Validation Methods
```go
// Frame validation
if frame.TotalSize == 0 {
    return errors.New("empty frame")
}

// GOP validation
if !gop.IsComplete() {
    return errors.New("incomplete GOP")
}

// Timing validation
if packet.PTS < 0 {
    return errors.New("invalid PTS")
}
```

### Corruption Detection
The types support corruption tracking at multiple levels:
- Packet-level corruption flags
- Frame-level corruption counters
- GOP-level corruption statistics

## Integration with Pipeline

The types package integrates with all pipeline components:

### Input Stage
- **Depacketizers** create TimestampedPacket instances
- **Protocol adapters** set codec and stream metadata

### Processing Stage  
- **Frame assemblers** build VideoFrame from packets
- **GOP buffers** organize frames into GOP structures

### Output Stage
- **Schedulers** use timing information for delivery
- **Encoders** access frame/GOP structure for optimization

## Best Practices

1. **Immutability**: Don't modify frames after creation
2. **Validation**: Always validate timing and structure
3. **Memory**: Use buffer pools for packet data
4. **Threading**: Avoid sharing mutable instances across goroutines
5. **Timing**: Use rational arithmetic for precise calculations
6. **Flags**: Use bitwise operations for efficient flag checks

## Future Enhancements

Potential extensions to the types system:

- **HDR Metadata**: Support for HDR10, Dolby Vision
- **Spatial Layers**: Scalable video coding support  
- **Audio Channels**: Multi-channel audio representation
- **Encryption**: Encrypted frame/packet support
- **Quality Metrics**: More sophisticated quality tracking
