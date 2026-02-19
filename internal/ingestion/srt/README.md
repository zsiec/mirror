# SRT Package

The `srt` package implements SRT (Secure Reliable Transport) stream ingestion for the Mirror platform. It uses an adapter pattern to abstract the underlying SRT library (pure Go `github.com/zsiec/srtgo`), providing connection management, stream ID validation, and statistics collection.

## Architecture

```
┌─────────────────────────────────────────────┐
│              SRTAdapter (interface)          │
│  NewListener / NewConnection / Connect      │
├─────────────────────────────────────────────┤
│         PureGoAdapter (zsiec/srtgo)         │
└──────────────┬──────────────────────────────┘
               │
┌──────────────▼──────────────────────────────┐
│   Listener                                  │
│  • Stream ID validation (regex)             │
│  • Connection limiting (100 max)            │
│  • Bandwidth management (1 Gbps)            │
│  • Listen callback for accept/reject        │
│  • Exponential backoff on accept errors     │
└──────────────┬──────────────────────────────┘
               │ per connection
┌──────────────▼──────────────────────────────┐
│   Connection                                │
│  • io.Reader / io.Writer                    │
│  • Atomic stats tracking                    │
│  • Pause/resume support                     │
│  • Backpressure via MaxBandwidth            │
│  • ReadLoop with heartbeat + stats ticker   │
└─────────────────────────────────────────────┘
```

## Adapter Pattern

The package abstracts SRT library details behind four interfaces:

```go
type SRTAdapter interface {
    NewListener(address string, port int, config Config) (SRTListener, error)
    NewConnection(socket SRTSocket) (SRTConnection, error)
    Connect(ctx context.Context, address string, port int, config Config) (SRTConnection, error)
}

type SRTListener interface {
    Listen(ctx context.Context, backlog int) error
    Accept() (SRTSocket, *net.UDPAddr, error)
    SetListenCallback(callback ListenCallback) error
    Close() error
    GetPort() int
}

type SRTSocket interface {
    GetStreamID() string
    Close() error
    SetRejectReason(reason RejectionReason) error
}

type SRTConnection interface {
    Read(b []byte) (int, error)
    Write(b []byte) (int, error)
    Close() error
    GetStreamID() string
    GetStats() ConnectionStats
    SetMaxBW(bw int64) error
    GetMaxBW() int64
}
```

The `PureGoAdapter` implements these using the pure Go `zsiec/srtgo` library with the following socket configuration:

- Live mode with TSBPD enabled
- Buffer sizes derived from `inputBandwidth * latency * 2` (8MB minimum)
- 25% overhead bandwidth, packet reorder tolerance of 128 (`LossMaxTTL`)
- 5-second connection timeout

### Rejection Reasons

```go
const (
    RejectionReasonUnauthorized        // SRT 401
    RejectionReasonResourceUnavailable // SRT 402 (Overload)
    RejectionReasonBadRequest          // SRT 400
    RejectionReasonForbidden           // SRT 403
    RejectionReasonNotFound            // SRT 404
    RejectionReasonBadMode             // SRT 405
    RejectionReasonUnacceptable        // SRT 406
)
```

## Core Components

### Listener

Accepts incoming SRT connections with stream ID validation and connection limiting.

```go
adapter := srt.NewPureGoAdapter()
listener := srt.NewListenerWithAdapter(cfg, codecsCfg, reg, adapter, logger)
listener.SetHandler(func(conn *srt.Connection) error {
    return manager.HandleSRTConnection(conn)
})
err := listener.Start()
```

#### Constructor

```go
func NewListenerWithAdapter(cfg *config.SRTConfig, codecsCfg *config.CodecsConfig,
    reg registry.Registry, adapter SRTAdapter, logger logger.Logger) *Listener
```

Creates a connection limiter (default 100 max), bandwidth manager (1 Gbps), and codec detector.

#### Key Methods

| Method | Description |
|--------|-------------|
| `Start() error` | Creates SRT listener via adapter, sets listen callback, spawns accept loop |
| `Stop() error` | Cancels context, closes listener, waits up to 10s for goroutines |
| `SetHandler(ConnectionHandler)` | Sets callback for accepted connections |
| `GetActiveConnections() int` | Returns count from `sync.Map` |
| `GetConnectionInfo() map[string]interface{}` | Returns per-connection details and stats |

#### Stream ID Validation

Stream IDs are validated against `^[\x20-\x7E]{1,512}$` (printable ASCII, up to 512 bytes), supporting SRT access control syntax (`#!::key=value,key2=value2`).

The accept loop uses exponential backoff (100ms initial, 5s max) on accept errors.

### Connection

Wraps an `SRTConnection` with statistics, lifecycle management, and backpressure.

```go
conn := srt.NewConnectionWithSRTConn(streamID, srtConn, remoteAddr, registry, codecDetector, logger)
```

#### Constructor

```go
func NewConnectionWithSRTConn(streamID string, conn SRTConnection, remoteAddr string,
    registry registry.Registry, codecDetector *codec.Detector, logger logger.Logger) *Connection
```

Implements `io.Reader` and `io.Writer`. Uses `sync.Once` for close safety and `atomic.Int64` for last-active tracking.

#### Key Methods

| Method | Description |
|--------|-------------|
| `Read(b []byte) (int, error)` | Reads from SRT, updates stats atomically |
| `Write(b []byte) (int, error)` | Writes to SRT, updates stats atomically |
| `Close() error` | Closes once, unregisters from registry |
| `ReadLoop(ctx context.Context) error` | Main read loop with stats ticker, heartbeat (10s), and context cancellation |
| `Pause()` / `Resume()` | Atomic pause/resume |
| `GetStats() ConnectionStats` | Returns stats from underlying SRT connection |
| `SetMaxBandwidth(bw int64) error` | Sets max bandwidth for backpressure |
| `GetStreamID() string` | Returns stream ID |
| `GetRemoteAddr() string` | Returns remote address |

### ConnectionStats

```go
type ConnectionStats struct {
    BytesReceived    int64
    BytesSent        int64
    PacketsReceived  int64
    PacketsSent      int64
    PacketsLost      int64
    PacketsRetrans   int64
    RTTMs            float64
    BandwidthMbps    float64
    DeliveryDelayMs  float64
    ConnectionTimeMs time.Duration

    // Extended stats
    PacketsDropped            int64
    PacketsReceiveLost        int64
    PacketsFlightSize         int
    NegotiatedLatencyMs       int
    ReceiveRateMbps           float64
    EstimatedLinkCapacityMbps float64
    AvailableRcvBuf           int
}
```

### Config

```go
type Config struct {
    Address           string
    Port              int
    Latency           time.Duration
    MaxBandwidth      int64
    InputBandwidth    int64
    PayloadSize       int
    FlowControlWindow int
    PeerIdleTimeout   time.Duration
    MaxConnections    int
    Encryption        EncryptionConfig
}

type EncryptionConfig struct {
    Enabled         bool
    Passphrase      string
    KeyLength       int    // 128, 192, or 256
    PBKDFIterations int
}
```

## SRT Library Lifecycle

```go
srt.InitSRTLibrary()  // No-op (pure Go, no C library to init)
srt.CleanupSRT()      // No-op (pure Go, no C library to clean up)
```

## Testing

```bash
go test ./internal/ingestion/srt/...
```
