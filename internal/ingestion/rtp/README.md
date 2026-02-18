# RTP Package

The `rtp` package implements RTP (Real-time Transport Protocol) stream ingestion for the Mirror platform. It handles UDP packet reception, SSRC-based session management, packet validation with B-frame awareness, jitter buffering, and packet loss tracking.

## Architecture

```
UDP Socket (RTP)          UDP Socket (RTCP)
       │                        │
       ▼                        ▼
┌─────────────┐          ┌─────────────┐
│  routePackets│         │ handleRTCP  │
│  (pion/rtp)  │         │ (SSRC match)│
└──────┬──────┘          └──────┬──────┘
       │ validated               │
       ▼                         ▼
┌──────────────────────────────────────┐
│   Sessions  map[string]*Session      │
│   key: "{addr}_{SSRC}"              │
├──────────────────────────────────────┤
│ Per-session:                         │
│  • Codec detection state machine     │
│  • JitterBuffer (heap-based)         │
│  • PacketLossTracker                 │
│  • Depacketizer (codec-specific)     │
│  • NAL callback to adapter layer     │
└──────────────────────────────────────┘
```

## Core Components

### Listener

Accepts RTP/RTCP packets on UDP sockets and routes them to sessions.

```go
listener := rtp.NewListener(cfg, codecsCfg, reg, logger)
listener.SetSessionHandler(func(session *rtp.Session) error {
    return manager.HandleRTPSession(session)
})
err := listener.Start()
```

#### Constructor

```go
func NewListener(cfg *config.RTPConfig, codecsCfg *config.CodecsConfig,
    reg registry.Registry, logger logger.Logger) *Listener
```

Creates a connection limiter (5 per stream, 100 total), a bandwidth manager (1 Gbps), and a `Validator` accepting dynamic payload types 96-127 with `MaxSequenceGap: 100` and `MaxTimestampJump: 90000 * 60` (60s at 90kHz).

#### Key Methods

| Method | Description |
|--------|-------------|
| `Start() error` | Binds RTP + RTCP UDP sockets, starts routing and cleanup goroutines |
| `Stop() error` | Stops listener and all sessions |
| `SetSessionHandler(SessionHandler)` | Sets callback invoked for new sessions |
| `GetActiveSessions() int` | Returns count of active sessions |
| `GetSessionStats() map[string]SessionStats` | Returns per-session statistics |
| `TerminateStream(streamID) error` | Terminates a specific session |
| `PauseStream(streamID) error` | Pauses packet processing for a session |
| `ResumeStream(streamID) error` | Resumes a paused session |

#### Packet Routing

`routePackets` reads from a 65535-byte UDP buffer, parses via `pion/rtp`, validates with the `Validator`, and creates sessions keyed by `"{addr}_{SSRC}"`. Session cleanup runs on a configurable interval (default 10s) removing inactive sessions.

### Session

Manages an individual RTP stream identified by SSRC and source address.

```go
session, err := rtp.NewSession(streamID, remoteAddr, ssrc, registry, codecsCfg, logger)
```

#### Constructor

```go
func NewSession(streamID string, remoteAddr *net.UDPAddr, ssrc uint32,
    reg registry.Registry, codecsCfg *config.CodecsConfig, logger logger.Logger) (*Session, error)
```

Creates a codec detector and depacketizer factory, initializes a `JitterBuffer(100, 100ms, 90000)`, a `PacketLossTracker`, and registers the stream in the registry.

#### Codec Detection State Machine

Sessions use a state machine for codec detection:

```go
const (
    CodecStateUnknown   // Initial state
    CodecStateDetecting // Detection in progress
    CodecStateDetected  // Codec detected, depacketizer created
    CodecStateError     // Detection failed
    CodecStateTimeout   // Detection timed out
)
```

#### Key Methods

| Method | Description |
|--------|-------------|
| `ProcessPacket(packet *rtp.Packet)` | Main packet processing entry point |
| `Start()` / `Stop()` | Lifecycle management |
| `Pause()` / `Resume()` | Pauses/resumes packet processing |
| `IsActive() bool` | True if last packet received within timeout |
| `GetStats() SessionStats` | Returns session statistics |
| `SetNALCallback(func([][]byte) error)` | Sets callback for depacketized NAL units |
| `SetSDPInfo(mediaFormat, encodingName string, clockRate uint32)` | Sets SDP-derived codec info |
| `GetRecoveryStats() RecoveryStats` | Returns packet loss/recovery statistics |
| `GetLostPackets(maxCount int) []uint16` | Returns recently lost sequence numbers |

#### SessionStats

```go
type SessionStats struct {
    PacketsReceived   uint64
    BytesReceived     uint64
    LastPayloadType   uint8
    PacketsLost       uint64
    RateLimitDrops    uint64
    BufferOverflows   uint64
    LastSequence      uint16
    InitialSequence   uint16
    Jitter            float64
    LastPacketTime    time.Time
    StartTime         time.Time

    // Enhanced sequence tracking
    SequenceInitialized bool
    SequenceGaps        uint64
    MaxSequenceGap      uint16
    ReorderedPackets    uint64
    SequenceResets      uint64

    // Jitter buffer stats
    LastTimestamp   uint32
    LastTransitTime int64
    MaxJitter       float64
    JitterSamples   uint64

    // SSRC tracking
    SSRCChanges     uint64
    LastSSRC        uint32
    SSRCInitialized bool
}
```

### Validator

Validates RTP packets for protocol conformance with B-frame awareness.

```go
validator := rtp.NewValidator(rtp.DefaultValidatorConfig())
err := validator.ValidatePacket(packet)
```

#### Validation Checks

1. **RTP version** must be 2
2. **Payload type** must be in the allowed list (default: dynamic 96-127)
3. **Padding** validation -- if set, last byte must be > 0 and <= raw length
4. **Sequence numbers** -- gaps detected via signed 16-bit arithmetic for wraparound
5. **Timestamps** -- B-frame aware: maintains a monotonic tracker, estimates frame duration from a 10-sample window, allows backward timestamps by up to 5 frame durations

#### Configuration

```go
type ValidatorConfig struct {
    AllowedPayloadTypes []uint8 // Valid payload types
    MaxSequenceGap      int     // Max gap in sequence numbers (default: 100)
    MaxTimestampJump    uint32  // Max timestamp jump in RTP units (default: 90000 * 60)
}
```

#### Errors

```go
var (
    ErrInvalidRTPVersion  = errors.New("invalid RTP version")
    ErrInvalidPayloadType = errors.New("invalid payload type")
    ErrPacketTooSmall     = errors.New("packet too small")
    ErrInvalidPadding     = errors.New("invalid padding")
    ErrSequenceGap        = errors.New("sequence number gap detected")
    ErrTimestampJump      = errors.New("timestamp jump detected")
)
```

### JitterBuffer

Heap-based packet reordering buffer that delivers packets based on playout time.

```go
jb := rtp.NewJitterBuffer(100, 100*time.Millisecond, 90000)
jb.Add(packet)
readyPackets, err := jb.Get()
```

#### Behavior

- **Add**: Discards duplicates via `seqSeen` map, drops oldest on overflow, uses `container/heap` sorted by sequence number (with 16-bit wraparound handling)
- **Get**: Returns packets whose playout time has elapsed. Playout time = base wall time + RTP timestamp offset + target delay. Uses `int32` cast for 32-bit timestamp wraparound
- **Flush**: Clears all packets and resets state

#### Statistics

```go
type JitterBufferStats struct {
    PacketsBuffered  uint64
    PacketsDelivered uint64
    PacketsDropped   uint64
    PacketsLate      uint64
    CurrentDepth     int
    MaxDepth         int
    UnderrunCount    uint64
    OverrunCount     uint64
}
```

### PacketLossTracker

Tracks sequence number gaps to detect and recover from packet loss.

```go
tracker := rtp.NewPacketLossTracker()
err := tracker.ProcessSequence(seqNum)
stats := tracker.GetStats()
```

Uses RFC 1982 signed 16-bit arithmetic for sequence distance. Maintains a circular buffer (1000 entries) of recently lost sequence numbers. Tracks a sliding 10-second loss window for current loss rate calculation.

#### RecoveryStats

```go
type RecoveryStats struct {
    TotalLost          uint64
    TotalRecovered     uint64
    UnrecoverableLoss  uint64
    CurrentLossRate    float64
    AverageLossRate    float64
    MaxConsecutiveLoss uint16
    LossEvents         uint64
}
```

## Testing

```bash
source scripts/srt-env.sh && go test ./internal/ingestion/rtp/...
```
