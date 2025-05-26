# RTP Package

The RTP package implements Real-time Transport Protocol (RTP) support for video streaming ingestion. It provides comprehensive RTP packet processing, session management, and stream validation capabilities.

## Overview

RTP (Real-time Transport Protocol) is a network protocol for delivering audio and video over IP networks. This package handles:

- **RTP Packet Parsing**: Complete RFC 3550 implementation
- **Session Management**: Multi-stream RTP session handling
- **Sequence Validation**: Packet loss detection and reordering
- **Timestamp Processing**: RTP timestamp to PTS conversion
- **RTCP Support**: Real-time Control Protocol handling (future)

## Core Components

### RTP Listener

The `Listener` manages incoming RTP streams on UDP sockets:

```go
type Listener struct {
    addr        *net.UDPAddr     // Listen address
    conn        *net.UDPConn     // UDP connection
    sessions    map[uint32]*Session // Active sessions by SSRC
    sessionsMu  sync.RWMutex     // Session map protection
    metrics     *Metrics         // Performance metrics
    config      *Config          // Configuration
}
```

### RTP Session

The `Session` represents an individual RTP stream:

```go
type Session struct {
    ssrc        uint32           // Synchronization source ID
    sourceAddr  *net.UDPAddr     // Source address
    payloadType uint8            // RTP payload type
    clockRate   uint32           // RTP clock rate
    
    // Sequence tracking
    lastSeqNum    uint16         // Last sequence number
    expectedSeq   uint16         // Expected next sequence
    packetsLost   uint32         // Lost packet count
    packetsRecv   uint32         // Received packet count
    
    // Timing
    firstTimestamp uint32        // First RTP timestamp
    lastTimestamp  uint32        // Last RTP timestamp
    
    // Validation
    validator     *Validator     // Packet validator
    lastActivity  time.Time      // Last packet time
}
```

### RTP Validator

The `Validator` ensures packet integrity and proper sequencing:

```go
type Validator struct {
    // Sequence validation
    maxDropout    uint16         // Max sequence dropout
    maxMisorder   uint16         // Max misordered packets
    minSequential uint16         // Min sequential packets
    
    // State tracking
    baseSeq       uint16         // Base sequence number
    maxSeq        uint16         // Highest sequence seen
    badSeq        uint16         // Last bad sequence
    probation     uint16         // Probationary packets
    
    // Statistics
    received      uint32         // Packets received
    expectedPrior uint32         // Expected packets (previous)
    receivedPrior uint32         // Received packets (previous)
}
```

## Protocol Implementation

### RTP Packet Structure

```go
type RTPHeader struct {
    Version    uint8   // RTP version (2)
    Padding    bool    // Padding flag
    Extension  bool    // Extension flag
    CSRCCount  uint8   // CSRC count
    Marker     bool    // Marker bit
    PayloadType uint8  // Payload type
    SeqNumber  uint16  // Sequence number
    Timestamp  uint32  // RTP timestamp
    SSRC       uint32  // Synchronization source
    CSRC       []uint32 // Contributing sources
}
```

### Packet Processing

```go
func (l *Listener) processPacket(data []byte, addr *net.UDPAddr) error {
    // Parse RTP header
    header, payload, err := parseRTPPacket(data)
    if err != nil {
        return fmt.Errorf("invalid RTP packet: %w", err)
    }
    
    // Get or create session
    session := l.getSession(header.SSRC, addr)
    
    // Validate packet
    if !session.validator.Validate(header.SeqNumber) {
        return errors.New("packet validation failed")
    }
    
    // Create timestamped packet
    packet := &types.TimestampedPacket{
        Data:        payload,
        PTS:         int64(header.Timestamp),
        CaptureTime: time.Now(),
        SSRC:        header.SSRC,
        SeqNum:      header.SeqNumber,
        StreamID:    fmt.Sprintf("rtp_%d", header.SSRC),
    }
    
    // Forward to pipeline
    return l.forwardPacket(packet)
}
```

## Stream Management

### Session Discovery

RTP sessions are automatically discovered based on SSRC:

```go
func (l *Listener) getSession(ssrc uint32, addr *net.UDPAddr) *Session {
    l.sessionsMu.Lock()
    defer l.sessionsMu.Unlock()
    
    session, exists := l.sessions[ssrc]
    if !exists {
        session = NewSession(ssrc, addr)
        l.sessions[ssrc] = session
        l.metrics.ActiveSessions.Inc()
    }
    
    return session
}
```

### Session Cleanup

Inactive sessions are automatically cleaned up:

```go
func (l *Listener) cleanupSessions() {
    l.sessionsMu.Lock()
    defer l.sessionsMu.Unlock()
    
    timeout := time.Now().Add(-l.config.SessionTimeout)
    
    for ssrc, session := range l.sessions {
        if session.lastActivity.Before(timeout) {
            delete(l.sessions, ssrc)
            l.metrics.ActiveSessions.Dec()
            l.metrics.SessionTimeouts.Inc()
        }
    }
}
```

## Validation and Error Recovery

### Sequence Number Validation

RFC 3550 compliant sequence validation:

```go
func (v *Validator) Validate(seq uint16) bool {
    if v.probation > 0 {
        // Still in probationary period
        if seq == v.expectedSeq {
            v.probation--
            v.expectedSeq = seq + 1
            if v.probation == 0 {
                return true // Source validated
            }
        } else {
            v.probation = v.minSequential - 1
            v.expectedSeq = seq + 1
        }
        return false
    }
    
    // Normal validation
    return v.validateSequence(seq)
}
```

### Packet Loss Detection

```go
func (s *Session) updateLossStatistics(seqNum uint16) {
    expected := s.expectedSeq
    if seqNum == expected {
        // Packet arrived in order
        s.expectedSeq++
        s.packetsRecv++
    } else if seqNum > expected {
        // Packet(s) lost
        lost := uint32(seqNum - expected)
        s.packetsLost += lost
        s.expectedSeq = seqNum + 1
        s.packetsRecv++
    } else {
        // Late/duplicate packet
        s.handleLatePacket(seqNum)
    }
}
```

## Codec Support

### Payload Type Mapping

```go
var PayloadTypeMap = map[uint8]CodecInfo{
    // Static payload types
    0:  {Codec: types.CodecG711, ClockRate: 8000},   // PCMU
    8:  {Codec: types.CodecG711, ClockRate: 8000},   // PCMA
    14: {Codec: types.CodecMP3, ClockRate: 90000},   // MPA
    26: {Codec: types.CodecJPEG, ClockRate: 90000},  // JPEG
    33: {Codec: types.CodecMP2T, ClockRate: 90000},  // MP2T
    
    // Dynamic payload types (96-127) require SDP
}
```

### Dynamic Payload Types

```go
func (s *Session) configureCodec(payloadType uint8, codecName string, clockRate uint32) {
    s.payloadType = payloadType
    s.clockRate = clockRate
    
    switch strings.ToLower(codecName) {
    case "h264":
        s.codec = types.CodecH264
    case "h265", "hevc":
        s.codec = types.CodecHEVC
    case "av01":
        s.codec = types.CodecAV1
    default:
        s.codec = types.CodecUnknown
    }
}
```

## Performance Optimization

### Buffer Management

```go
type PacketBuffer struct {
    packets    []*RTPPacket     // Packet buffer
    size       int              // Buffer size
    head       int              // Buffer head
    tail       int              // Buffer tail
    mu         sync.Mutex       // Buffer protection
}

func (b *PacketBuffer) Add(packet *RTPPacket) bool {
    b.mu.Lock()
    defer b.mu.Unlock()
    
    if b.isFull() {
        // Drop oldest packet
        b.head = (b.head + 1) % len(b.packets)
        b.metrics.BufferOverruns.Inc()
    }
    
    b.packets[b.tail] = packet
    b.tail = (b.tail + 1) % len(b.packets)
    b.size++
    
    return true
}
```

### Memory Pooling

```go
var packetPool = sync.Pool{
    New: func() interface{} {
        return &RTPPacket{
            Header:  RTPHeader{},
            Payload: make([]byte, 0, 1500), // MTU size
        }
    },
}

func getPacket() *RTPPacket {
    return packetPool.Get().(*RTPPacket)
}

func putPacket(packet *RTPPacket) {
    packet.reset()
    packetPool.Put(packet)
}
```

## Configuration

### Listener Configuration

```go
type Config struct {
    // Network settings
    ListenAddr      string        // UDP listen address
    Port            int           // UDP port
    BufferSize      int           // UDP receive buffer size
    
    // Session management
    SessionTimeout  time.Duration // Session inactivity timeout
    MaxSessions     int           // Maximum concurrent sessions
    
    // Validation settings
    MaxDropout      uint16        // Max sequence dropout
    MaxMisorder     uint16        // Max misordered packets
    MinSequential   uint16        // Min sequential for validation
    
    // Performance
    WorkerCount     int           // Number of worker goroutines
    ChannelBuffer   int           // Channel buffer size
}
```

### Default Values

```go
func DefaultConfig() *Config {
    return &Config{
        ListenAddr:      "0.0.0.0",
        Port:            5004,
        BufferSize:      1048576,  // 1MB
        SessionTimeout:  30 * time.Second,
        MaxSessions:     100,
        MaxDropout:      3000,
        MaxMisorder:     100,
        MinSequential:   2,
        WorkerCount:     4,
        ChannelBuffer:   1000,
    }
}
```

## Metrics and Monitoring

### Session Metrics

```go
type SessionMetrics struct {
    PacketsReceived  prometheus.Counter   // Total packets received
    PacketsLost      prometheus.Counter   // Total packets lost
    BytesReceived    prometheus.Counter   // Total bytes received
    Jitter          prometheus.Histogram // Packet jitter
    Bitrate         prometheus.Gauge     // Current bitrate
}
```

### Quality Metrics

```go
func (s *Session) calculateJitter(timestamp uint32, arrival time.Time) {
    if s.lastArrival.IsZero() {
        s.lastArrival = arrival
        s.lastTimestamp = timestamp
        return
    }
    
    // RFC 3550 jitter calculation
    arrivalDelta := arrival.Sub(s.lastArrival)
    timestampDelta := time.Duration(timestamp-s.lastTimestamp) * time.Second / time.Duration(s.clockRate)
    
    transit := arrivalDelta - timestampDelta
    jitter := float64(abs(transit - s.lastTransit)) / float64(time.Millisecond)
    
    s.jitter = s.jitter + (jitter-s.jitter)/16 // RFC 3550 formula
    s.metrics.Jitter.Observe(s.jitter)
}
```

## Integration with Video Pipeline

### Packet Forwarding

```go
func (l *Listener) forwardToDepacketizer(packet *types.TimestampedPacket) error {
    // Determine codec from payload type
    codec := l.getCodecFromPayloadType(packet.PayloadType)
    
    // Forward to appropriate depacketizer
    switch codec {
    case types.CodecH264:
        return l.h264Depacketizer.Process(packet)
    case types.CodecHEVC:
        return l.hevcDepacketizer.Process(packet)
    case types.CodecAV1:
        return l.av1Depacketizer.Process(packet)
    default:
        return fmt.Errorf("unsupported codec: %v", codec)
    }
}
```

### Synchronization Integration

```go
func (l *Listener) createSyncPacket(rtpPacket *RTPPacket) *sync.TimedPacket {
    return &sync.TimedPacket{
        Data:      rtpPacket.Payload,
        PTS:       int64(rtpPacket.Header.Timestamp),
        StreamID:  fmt.Sprintf("rtp_%d", rtpPacket.Header.SSRC),
        ClockRate: l.getClockRate(rtpPacket.Header.PayloadType),
        Marker:    rtpPacket.Header.Marker,
    }
}
```

## Testing

The package includes comprehensive tests for:

### Protocol Compliance
- RFC 3550 RTP header parsing
- Sequence number validation
- Timestamp handling
- Payload type support

### Error Conditions
- Malformed packets
- Network errors
- Session timeouts
- Buffer overruns

### Performance
- High packet rate handling
- Memory usage optimization
- Concurrent session support

```go
func TestRTPPacketParsing(t *testing.T) {
    testCases := []struct {
        name     string
        packet   []byte
        expected RTPHeader
        hasError bool
    }{
        {
            name: "valid H.264 packet",
            packet: buildRTPPacket(RTPHeader{
                Version:     2,
                PayloadType: 96,
                SeqNumber:   12345,
                Timestamp:   67890,
                SSRC:        0x12345678,
            }),
            expected: RTPHeader{
                Version:     2,
                PayloadType: 96,
                SeqNumber:   12345,
                Timestamp:   67890,
                SSRC:        0x12345678,
            },
        },
        // More test cases...
    }
    
    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            header, _, err := parseRTPPacket(tc.packet)
            if tc.hasError {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
                assert.Equal(t, tc.expected, header)
            }
        })
    }
}
```

## Best Practices

1. **Session Management**: Clean up inactive sessions regularly
2. **Validation**: Always validate sequence numbers per RFC 3550
3. **Buffer Management**: Use bounded buffers to prevent memory exhaustion
4. **Error Handling**: Handle packet loss gracefully
5. **Performance**: Use packet pooling for high-throughput scenarios

## Future Enhancements

Potential improvements for the RTP package:

- **RTCP Support**: Real-time Control Protocol implementation
- **FEC**: Forward Error Correction support
- **Multicast**: IP multicast support for efficient delivery
- **RED**: Redundancy encoding for loss recovery
- **SRTP**: Secure RTP for encrypted streams
