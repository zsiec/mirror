# MPEG-TS Package

The MPEG-TS package provides parsing and demultiplexing capabilities for MPEG Transport Stream (MPEG-TS) containers, which are commonly used in video streaming and broadcasting applications.

## Overview

MPEG Transport Stream is a standardized format for transmitting and storing digital video, audio, and metadata. It's designed for environments where data loss might occur, making it ideal for:

- **Live Streaming**: SRT, UDP, and other network protocols
- **Broadcasting**: Digital TV, IPTV systems
- **File Storage**: Long-form video content
- **Adaptive Streaming**: HLS segment containers

## Core Components

### MPEG-TS Structure

MPEG-TS consists of fixed 188-byte packets, each containing:

```
┌─────────────────────────────────────────────────────────┐
│ Header (4 bytes) │ Adaptation Field │ Payload (Variable) │
│                  │ (Optional)       │                    │
└─────────────────────────────────────────────────────────┘
```

### Packet Structure

```go
type Packet struct {
    // Basic packet info
    PID                   uint16 // Packet Identifier
    PayloadStart          bool   // Start of new PES packet
    AdaptationFieldExists bool   // Has adaptation field
    PayloadExists         bool   // Has payload data
    ContinuityCounter     uint8  // Sequence counter
    Payload               []byte // Packet payload
    
    // Timing information (from PES)
    HasPTS bool   // PTS present
    HasDTS bool   // DTS present  
    PTS    int64  // Presentation timestamp
    DTS    int64  // Decode timestamp
    
    // Clock reference (from adaptation field)
    HasPCR bool   // PCR present
    PCR    int64  // Program Clock Reference
}
```

### Parser

The `Parser` provides stateful parsing of MPEG-TS streams:

```go
type Parser struct {
    // Stream identification
    pmtPID   uint16 // Program Map Table PID
    videoPID uint16 // Video elementary stream PID
    audioPID uint16 // Audio elementary stream PID
    pcrPID   uint16 // PCR reference PID
    
    // PES assembly state
    pesBuffer  map[uint16][]byte // Per-PID buffers
    pesStarted map[uint16]bool   // Assembly state
    
    // PSI parsing state
    patParsed  bool   // PAT successfully parsed
    pmtParsed  bool   // PMT successfully parsed
    programNum uint16 // Program number
    
    // Codec detection
    videoStreamType uint8 // Stream type from PMT
    audioStreamType uint8 // Stream type from PMT
}
```

## Key Features

### Automatic Stream Discovery

The parser automatically discovers streams through PSI (Program Specific Information) tables:

1. **PAT (Program Association Table)**: Maps programs to PMT PIDs
2. **PMT (Program Map Table)**: Maps elementary streams to PIDs and types

```go
parser := NewParser()
packets, err := parser.Parse(mpegtsData)

// PIDs are automatically detected
videoPID := parser.GetVideoPID()
audioPID := parser.GetAudioPID()
```

### Timestamp Extraction

Extracts precise timing information from PES headers:

```go
for _, packet := range packets {
    if packet.HasPTS {
        pts := packet.PTS // 33-bit timestamp in 90kHz units
    }
    
    if packet.HasDTS {
        dts := packet.DTS // Decode timestamp for B-frames
    }
    
    if packet.HasPCR {
        pcr := packet.PCR // Program Clock Reference
    }
}
```

### Codec Detection

Automatically detects video and audio codecs from PMT:

```go
videoType := parser.GetVideoStreamType()
audioType := parser.GetAudioStreamType()

switch videoType {
case 0x1B: // H.264
case 0x24: // HEVC
case 0x51: // AV1
}
```

## Supported Stream Types

### Video Codecs

| Stream Type | Codec | Description |
|-------------|-------|-------------|
| 0x01 | MPEG-1 Video | Legacy video codec |
| 0x02 | MPEG-2 Video | DVD, broadcast video |
| 0x1B | H.264/AVC | Most common modern codec |
| 0x24 | H.265/HEVC | High efficiency codec |
| 0x51 | AV1 | Next-generation codec |

### Audio Codecs

| Stream Type | Codec | Description |
|-------------|-------|-------------|
| 0x03 | MPEG-1 Audio | MP3 and similar |
| 0x04 | MPEG-2 Audio | Extended MP3 |
| 0x0F | AAC | Advanced Audio Coding |
| 0x11 | AAC | AAC with additional features |
| 0x81 | AC-3 | Dolby Digital |

## Usage Examples

### Basic Parsing

```go
// Create parser
parser := NewParser()

// Parse MPEG-TS data
packets, err := parser.Parse(mpegtsData)
if err != nil {
    log.Fatal("Parse error:", err)
}

// Process packets
for _, packet := range packets {
    if parser.IsVideoPID(packet.PID) {
        processVideoPacket(packet)
    } else if parser.IsAudioPID(packet.PID) {
        processAudioPacket(packet)
    }
}
```

### Stream-Specific Processing

```go
// Manual PID configuration (if known)
parser.SetVideoPID(256)
parser.SetAudioPID(257)
parser.SetPCRPID(256)

// Process with timing
for _, packet := range packets {
    if packet.HasPTS {
        // Convert 90kHz to seconds
        presentationTime := float64(packet.PTS) / 90000.0
        schedulePresentation(packet, presentationTime)
    }
}
```

### Continuous Stream Processing

```go
// Process streaming data
for {
    data := readFromNetwork() // Read chunk from network
    
    packets, err := parser.Parse(data)
    if err != nil {
        log.Warn("Parse error:", err)
        continue
    }
    
    for _, packet := range packets {
        if packet.PayloadStart {
            // Start of new PES packet
            startNewFrame(packet)
        } else {
            // Continuation of existing PES
            continueFrame(packet)
        }
    }
}
```

## Advanced Features

### PCR-Based Synchronization

Program Clock Reference provides system timing:

```go
var lastPCR int64
var pcrBase time.Time

for _, packet := range packets {
    if packet.HasPCR {
        if lastPCR == 0 {
            // First PCR - establish time base
            pcrBase = time.Now()
            lastPCR = packet.PCR
        } else {
            // Calculate elapsed time
            pcrDelta := packet.PCR - lastPCR
            elapsed := time.Duration(pcrDelta * 1000 / 27) // 27MHz clock
            
            // Update system clock reference
            systemTime := pcrBase.Add(elapsed)
            updateSystemClock(systemTime)
        }
    }
}
```

### PES Assembly

Reconstruct complete PES packets from TS packets:

```go
type PESAssembler struct {
    buffers map[uint16][]byte
    started map[uint16]bool
}

func (a *PESAssembler) AddPacket(packet *Packet) []byte {
    pid := packet.PID
    
    if packet.PayloadStart {
        // Start new PES packet
        a.buffers[pid] = make([]byte, 0)
        a.started[pid] = true
    }
    
    if a.started[pid] && len(packet.Payload) > 0 {
        a.buffers[pid] = append(a.buffers[pid], packet.Payload...)
        
        // Check if PES is complete (implementation dependent)
        if isPESComplete(a.buffers[pid]) {
            complete := a.buffers[pid]
            delete(a.buffers, pid)
            delete(a.started, pid)
            return complete
        }
    }
    
    return nil
}
```

### Error Handling and Recovery

MPEG-TS is designed for lossy environments:

```go
func (p *Parser) parseWithRecovery(data []byte) []*Packet {
    packets := make([]*Packet, 0)
    
    // Find sync bytes for recovery
    for i := 0; i < len(data); i++ {
        if data[i] == SyncByte && i+PacketSize <= len(data) {
            packet, err := p.parsePacket(data[i:i+PacketSize])
            if err != nil {
                // Skip corrupted packet
                log.Debug("Skipping corrupted packet:", err)
                continue
            }
            
            // Check continuity counter
            if !p.checkContinuity(packet) {
                log.Warn("Continuity error on PID", packet.PID)
            }
            
            packets = append(packets, packet)
            i += PacketSize - 1 // Move to next packet
        }
    }
    
    return packets
}
```

## Performance Considerations

### Memory Management

- **Buffer Pools**: Reuse packet structures to reduce GC pressure
- **Streaming**: Process packets immediately rather than accumulating
- **PES Limits**: Set maximum PES packet size to prevent memory exhaustion

```go
type PacketPool struct {
    pool sync.Pool
}

func (p *PacketPool) Get() *Packet {
    if packet := p.pool.Get(); packet != nil {
        return packet.(*Packet)
    }
    return &Packet{}
}

func (p *PacketPool) Put(packet *Packet) {
    // Reset packet state
    packet.PID = 0
    packet.Payload = packet.Payload[:0]
    p.pool.Put(packet)
}
```

### CPU Optimization

- **Fast Path**: Pre-identify critical PIDs to avoid full parsing
- **Batch Processing**: Process multiple packets in single calls
- **Minimal Allocation**: Reuse slices and avoid string conversion

```go
// Fast PID filtering
func filterVideoPIDs(data []byte, videoPID uint16) [][]byte {
    var videoPackets [][]byte
    
    for i := 0; i+PacketSize <= len(data); i += PacketSize {
        if data[i] == SyncByte {
            // Quick PID extraction without full parsing
            pid := uint16(data[i+1]&0x1F)<<8 | uint16(data[i+2])
            if pid == videoPID {
                packet := data[i:i+PacketSize]
                videoPackets = append(videoPackets, packet)
            }
        }
    }
    
    return videoPackets
}
```

## Integration with Video Pipeline

### Input Processing

```go
// SRT connection receives MPEG-TS
func (c *SRTConnection) processMPEGTS(data []byte) {
    packets, err := c.tsParser.Parse(data)
    if err != nil {
        c.metrics.IncrementParseErrors()
        return
    }
    
    for _, packet := range packets {
        if c.tsParser.IsVideoPID(packet.PID) && packet.PayloadExists {
            // Forward to codec depacketizer
            c.videoDepacketizer.ProcessTSPacket(packet)
        }
    }
}
```

### Timestamp Mapping

```go
// Convert MPEG-TS timestamps to pipeline timestamps
func convertTSTimestamp(packet *Packet) *types.TimestampedPacket {
    tsp := &types.TimestampedPacket{
        Data:        packet.Payload,
        CaptureTime: time.Now(),
        StreamID:    packet.StreamID,
    }
    
    if packet.HasPTS {
        tsp.PTS = packet.PTS // Already in 90kHz
    }
    
    if packet.HasDTS {
        tsp.DTS = packet.DTS
    }
    
    return tsp
}
```

## Testing

The package includes comprehensive tests for:

### Packet Parsing
- Valid packet structure validation
- Error recovery from corrupted data
- Boundary condition handling

### PSI Processing
- PAT/PMT parsing accuracy
- Multi-program stream handling
- Invalid table recovery

### Timestamp Extraction
- PTS/DTS accuracy across codecs
- PCR continuity validation
- Wrap-around handling

```go
func TestPacketParsing(t *testing.T) {
    testCases := []struct {
        name     string
        input    []byte
        expected Packet
        hasError bool
    }{
        {
            name: "valid H.264 packet",
            input: buildTestPacket(0x100, true, h264Payload),
            expected: Packet{
                PID: 0x100,
                PayloadStart: true,
                PayloadExists: true,
            },
        },
        // More test cases...
    }
    
    parser := NewParser()
    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            packets, err := parser.Parse(tc.input)
            // Validate results...
        })
    }
}
```

## Configuration

The parser behavior can be configured:

```go
type Config struct {
    MaxPESSize     int  // Maximum PES packet size
    StrictContinuity bool // Enforce continuity counters
    AutoDetectStreams bool // Enable PSI parsing
    PCRAccuracy    time.Duration // PCR timing accuracy
}

func NewParserWithConfig(cfg Config) *Parser {
    // Configure parser with specific settings
}
```

## Error Types

The package defines specific error types:

```go
var (
    ErrInvalidPacketSize = errors.New("invalid packet size")
    ErrMissingSyncByte  = errors.New("missing sync byte")
    ErrTransportError   = errors.New("transport error indicator")
    ErrInvalidPID       = errors.New("invalid PID")
    ErrPESHeaderTooShort = errors.New("PES header too short")
    ErrInvalidTimestamp = errors.New("invalid timestamp format")
)
```

## Best Practices

1. **Stream Discovery**: Always allow automatic PSI parsing for unknown streams
2. **Error Recovery**: Handle corrupted packets gracefully in streaming scenarios
3. **Memory Management**: Use buffer pools for high-throughput applications
4. **Timing**: Use PCR for system synchronization, PTS/DTS for presentation
5. **Continuity**: Check continuity counters for packet loss detection

## Future Enhancements

Potential improvements for the MPEG-TS package:

- **DVB Extensions**: Support for DVB-specific PSI tables
- **Encryption**: Support for encrypted TS streams
- **Metadata**: Extract additional metadata from PSI/SI tables
- **Multiplexing**: Support for creating MPEG-TS streams
- **Advanced Codecs**: Support for emerging video codecs
