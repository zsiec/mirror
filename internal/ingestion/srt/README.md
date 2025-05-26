# SRT Package

The SRT package implements Secure Reliable Transport (SRT) protocol support for low-latency, high-quality video streaming. SRT is designed for live streaming over unpredictable networks with automatic error recovery and adaptive bitrate.

## Overview

SRT (Secure Reliable Transport) is an open-source protocol that enables secure, reliable transport of data across unpredictable networks. Key features include:

- **Low Latency**: Optimized for real-time streaming
- **Error Recovery**: Automatic retransmission and FEC
- **Encryption**: AES encryption for secure transmission
- **Adaptive**: Dynamic adjustment to network conditions
- **Firewall Friendly**: Works through NAT and firewalls

## Core Components

### SRT Listener

The `Listener` manages incoming SRT connections:

```go
type Listener struct {
    socket    srt.Socket       // SRT socket
    addr      *net.UDPAddr     // Listen address
    config    *Config          // Configuration
    handlers  map[string]*ConnectionHandler // Active connections
    mu        sync.RWMutex     // Handler map protection
    metrics   *Metrics         // Performance metrics
    stopChan  chan struct{}    // Shutdown signal
}
```

### SRT Connection

The `Connection` represents an individual SRT stream:

```go
type Connection struct {
    socket      srt.Socket      // SRT socket
    streamID    string          // Stream identifier
    remoteAddr  *net.UDPAddr    // Remote address
    
    // Stream processing
    tsParser    *mpegts.Parser  // MPEG-TS parser
    depacketizer Depacketizer   // Video depacketizer
    
    // Metrics
    bytesReceived uint64         // Total bytes received
    packetsReceived uint64       // Total packets received
    lastActivity time.Time       // Last packet time
    bitrate     float64          // Current bitrate
    
    // State
    connected   bool             // Connection state
    stopChan    chan struct{}    // Stop signal
}
```

### Haivision Adapter

The `HaivisionAdapter` handles Haivision-specific SRT extensions:

```go
type HaivisionAdapter struct {
    // Haivision-specific parameters
    streamID     string          // Stream ID from Haivision
    mode         string          // Caller/Listener mode
    latency      time.Duration   // Target latency
    maxBandwidth int64           // Maximum bandwidth
    
    // Encryption
    passphrase   string          // Encryption passphrase
    keyLength    int             // Key length (128/192/256)
    
    // Statistics
    stats        HaivisionStats  // Extended statistics
}
```

## Protocol Implementation

### SRT Socket Configuration

```go
type SRTConfig struct {
    // Basic settings
    Port         int             // Listen port
    Latency      time.Duration   // Target latency
    MaxBandwidth int64           // Maximum bandwidth (bps)
    
    // Reliability
    LossMaxTTL   int             // Max time-to-live for lost packets
    RecvBuffer   int             // Receive buffer size
    SendBuffer   int             // Send buffer size
    
    // Encryption
    Passphrase   string          // Pre-shared key
    KeyLength    int             // AES key length
    
    // Performance
    MSS          int             // Maximum segment size
    FlightFlag   bool            // Flight flag option
    TooLatePacketDrop bool       // Drop too-late packets
}
```

### Connection Establishment

```go
func (l *Listener) acceptConnection() (*Connection, error) {
    // Accept SRT connection
    socket, addr, err := l.socket.Accept()
    if err != nil {
        return nil, fmt.Errorf("SRT accept failed: %w", err)
    }
    
    // Extract stream ID from SRT stream identifier
    streamID, err := l.extractStreamID(socket)
    if err != nil {
        socket.Close()
        return nil, fmt.Errorf("failed to extract stream ID: %w", err)
    }
    
    // Create connection handler
    conn := &Connection{
        socket:     socket,
        streamID:   streamID,
        remoteAddr: addr,
        tsParser:   mpegts.NewParser(),
        connected:  true,
        stopChan:   make(chan struct{}),
    }
    
    return conn, nil
}
```

## MPEG-TS Integration

### Stream Processing

SRT typically carries MPEG-TS containers:

```go
func (c *Connection) processData(data []byte) error {
    // Update metrics
    c.bytesReceived += uint64(len(data))
    c.packetsReceived++
    c.lastActivity = time.Now()
    
    // Parse MPEG-TS packets
    packets, err := c.tsParser.Parse(data)
    if err != nil {
        c.metrics.ParseErrors.Inc()
        return fmt.Errorf("MPEG-TS parse error: %w", err)
    }
    
    // Process each TS packet
    for _, packet := range packets {
        if c.tsParser.IsVideoPID(packet.PID) {
            if err := c.processVideoPacket(packet); err != nil {
                return err
            }
        } else if c.tsParser.IsAudioPID(packet.PID) {
            if err := c.processAudioPacket(packet); err != nil {
                return err
            }
        }
    }
    
    return nil
}
```

### Video Packet Processing

```go
func (c *Connection) processVideoPacket(packet *mpegts.Packet) error {
    // Create timestamped packet
    tsp := &types.TimestampedPacket{
        Data:        packet.Payload,
        CaptureTime: time.Now(),
        StreamID:    c.streamID,
        Type:        types.PacketTypeVideo,
    }
    
    // Add timing information if available
    if packet.HasPTS {
        tsp.PTS = packet.PTS
    }
    if packet.HasDTS {
        tsp.DTS = packet.DTS
    }
    
    // Forward to depacketizer
    return c.depacketizer.Process(tsp)
}
```

## Bitrate and Quality Monitoring

### Real-time Bitrate Calculation

```go
type BitrateCalculator struct {
    window     time.Duration    // Measurement window
    samples    []BitrateSample  // Sample history
    mu         sync.Mutex       // Sample protection
}

type BitrateSample struct {
    timestamp time.Time
    bytes     uint64
}

func (bc *BitrateCalculator) AddBytes(bytes uint64) {
    bc.mu.Lock()
    defer bc.mu.Unlock()
    
    now := time.Now()
    bc.samples = append(bc.samples, BitrateSample{
        timestamp: now,
        bytes:     bytes,
    })
    
    // Remove old samples
    cutoff := now.Add(-bc.window)
    for i, sample := range bc.samples {
        if sample.timestamp.After(cutoff) {
            bc.samples = bc.samples[i:]
            break
        }
    }
}

func (bc *BitrateCalculator) GetBitrate() float64 {
    bc.mu.Lock()
    defer bc.mu.Unlock()
    
    if len(bc.samples) < 2 {
        return 0
    }
    
    first := bc.samples[0]
    last := bc.samples[len(bc.samples)-1]
    
    duration := last.timestamp.Sub(first.timestamp).Seconds()
    totalBytes := last.bytes - first.bytes
    
    return float64(totalBytes*8) / duration // bits per second
}
```

### Connection Quality Metrics

```go
type ConnectionStats struct {
    // Basic metrics
    BytesReceived    uint64        // Total bytes
    PacketsReceived  uint64        // Total packets
    PacketsLost      uint64        // Lost packets
    PacketsDropped   uint64        // Dropped packets
    
    // Timing
    RTT             time.Duration  // Round-trip time
    Bandwidth       int64          // Available bandwidth
    SendRate        int64          // Current send rate
    ReceiveRate     int64          // Current receive rate
    
    // Quality
    PacketLossRate  float64        // Packet loss percentage
    Jitter          time.Duration  // Packet jitter
    Latency         time.Duration  // End-to-end latency
}

func (c *Connection) getStats() ConnectionStats {
    srtStats := c.socket.GetStats()
    
    return ConnectionStats{
        BytesReceived:   c.bytesReceived,
        PacketsReceived: c.packetsReceived,
        PacketsLost:     uint64(srtStats.PacketsLost),
        PacketsDropped:  uint64(srtStats.PacketsDropped),
        RTT:            time.Duration(srtStats.RTT) * time.Microsecond,
        Bandwidth:      srtStats.Bandwidth,
        SendRate:       srtStats.SendRate,
        ReceiveRate:    srtStats.ReceiveRate,
        PacketLossRate: float64(srtStats.PacketsLost) / float64(srtStats.PacketsSent) * 100,
    }
}
```

## Error Recovery and Resilience

### Automatic Reconnection

```go
type ReconnectionManager struct {
    config       ReconnectionConfig
    attempt      int
    lastAttempt  time.Time
    backoff      time.Duration
    maxBackoff   time.Duration
}

func (rm *ReconnectionManager) shouldReconnect() bool {
    if rm.attempt >= rm.config.MaxAttempts {
        return false
    }
    
    if time.Since(rm.lastAttempt) < rm.backoff {
        return false
    }
    
    return true
}

func (rm *ReconnectionManager) nextBackoff() {
    rm.backoff = time.Duration(float64(rm.backoff) * rm.config.BackoffMultiplier)
    if rm.backoff > rm.maxBackoff {
        rm.backoff = rm.maxBackoff
    }
}
```

### Stream Recovery

```go
func (c *Connection) handleDisconnection() error {
    c.connected = false
    
    // Notify pipeline of disconnection
    c.notifyDisconnection()
    
    // Attempt reconnection if configured
    if c.config.AutoReconnect {
        return c.reconnect()
    }
    
    return nil
}

func (c *Connection) reconnect() error {
    for attempt := 1; attempt <= c.config.MaxReconnectAttempts; attempt++ {
        log.Printf("Reconnection attempt %d/%d for stream %s", 
            attempt, c.config.MaxReconnectAttempts, c.streamID)
        
        if err := c.establishConnection(); err == nil {
            log.Printf("Successfully reconnected stream %s", c.streamID)
            return nil
        }
        
        // Exponential backoff
        backoff := time.Duration(attempt*attempt) * time.Second
        time.Sleep(backoff)
    }
    
    return fmt.Errorf("failed to reconnect after %d attempts", c.config.MaxReconnectAttempts)
}
```

## Performance Optimization

### Zero-Copy Operations

```go
func (c *Connection) receiveLoop() error {
    buffer := make([]byte, c.config.ReceiveBufferSize)
    
    for {
        select {
        case <-c.stopChan:
            return nil
        default:
            // Zero-copy receive
            n, err := c.socket.Read(buffer)
            if err != nil {
                return err
            }
            
            // Process without copying
            if err := c.processData(buffer[:n]); err != nil {
                log.Printf("Process error: %v", err)
            }
        }
    }
}
```

### Buffer Pool Management

```go
var bufferPool = sync.Pool{
    New: func() interface{} {
        return make([]byte, 0, 65536) // 64KB initial capacity
    },
}

func getBuffer() []byte {
    return bufferPool.Get().([]byte)[:0]
}

func putBuffer(buf []byte) {
    if cap(buf) <= 65536 {
        bufferPool.Put(buf)
    }
}
```

## Security Features

### Encryption Configuration

```go
type EncryptionConfig struct {
    Enabled    bool   // Enable encryption
    Passphrase string // Pre-shared key
    KeyLength  int    // Key length (128, 192, 256)
    KeyRefresh int    // Key refresh interval (packets)
}

func (c *Connection) configureEncryption(config EncryptionConfig) error {
    if !config.Enabled {
        return nil
    }
    
    // Set SRT encryption options
    if err := c.socket.SetOption(srt.SRTO_PASSPHRASE, config.Passphrase); err != nil {
        return fmt.Errorf("failed to set passphrase: %w", err)
    }
    
    if err := c.socket.SetOption(srt.SRTO_PBKEYLEN, config.KeyLength); err != nil {
        return fmt.Errorf("failed to set key length: %w", err)
    }
    
    return nil
}
```

### Access Control

```go
type AccessControl struct {
    allowedIPs    []net.IP       // Allowed IP addresses
    allowedRanges []*net.IPNet   // Allowed IP ranges
    streamKeys    map[string]string // Stream authentication keys
}

func (ac *AccessControl) IsAllowed(addr *net.UDPAddr, streamID string) bool {
    // Check IP whitelist
    if !ac.isIPAllowed(addr.IP) {
        return false
    }
    
    // Check stream authentication
    if !ac.isStreamAuthorized(streamID) {
        return false
    }
    
    return true
}
```

## Integration with Video Pipeline

### Pipeline Adapter

```go
type PipelineAdapter struct {
    streamHandler *ingestion.StreamHandler
    codecDetector *codec.Detector
    frameAssembler *frame.Assembler
}

func (pa *PipelineAdapter) ProcessSRTData(data []byte, streamID string) error {
    // Parse MPEG-TS
    packets, err := pa.tsParser.Parse(data)
    if err != nil {
        return err
    }
    
    for _, packet := range packets {
        // Convert to pipeline packet
        pipelinePacket := &types.TimestampedPacket{
            Data:        packet.Payload,
            StreamID:    streamID,
            PTS:         packet.PTS,
            CaptureTime: time.Now(),
        }
        
        // Forward to frame assembler
        if err := pa.frameAssembler.AddPacket(pipelinePacket); err != nil {
            return err
        }
    }
    
    return nil
}
```

## Testing

The package includes comprehensive tests for:

### Protocol Compliance
- SRT handshake procedures
- Encryption/decryption
- Error recovery mechanisms
- Latency measurements

### Performance Testing
- High bitrate streaming
- Multiple concurrent connections
- Memory usage optimization
- CPU utilization

### Integration Testing
- MPEG-TS parsing accuracy
- Video pipeline integration
- Metrics collection
- Error scenarios

```go
func TestSRTConnection(t *testing.T) {
    // Setup test listener
    listener, err := NewListener(Config{
        Port:    9998,
        Latency: 200 * time.Millisecond,
    })
    require.NoError(t, err)
    defer listener.Close()
    
    // Connect test client
    client, err := srt.Dial("srt://localhost:9998?streamid=test_stream")
    require.NoError(t, err)
    defer client.Close()
    
    // Test data transmission
    testData := generateTestMPEGTS()
    n, err := client.Write(testData)
    require.NoError(t, err)
    assert.Equal(t, len(testData), n)
    
    // Verify processing
    time.Sleep(100 * time.Millisecond)
    stats := listener.GetStreamStats("test_stream")
    assert.Greater(t, stats.BytesReceived, uint64(0))
}
```

## Configuration Examples

### Production Configuration

```yaml
srt:
  port: 30000
  latency: 200ms
  max_bandwidth: 100000000  # 100 Mbps
  encryption:
    enabled: true
    passphrase: "secure_key_here"
    key_length: 256
  performance:
    receive_buffer: 1048576   # 1MB
    mss: 1456
    flight_flag: true
    too_late_packet_drop: true
```

### Low-Latency Configuration

```yaml
srt:
  port: 30000
  latency: 40ms             # Ultra-low latency
  max_bandwidth: 50000000   # 50 Mbps
  loss_max_ttl: 0          # No retransmission
  too_late_packet_drop: true
  performance:
    receive_buffer: 524288   # 512KB
    send_buffer: 524288      # 512KB
```

## Best Practices

1. **Latency Tuning**: Balance latency vs reliability based on use case
2. **Buffer Sizing**: Configure buffers based on network conditions
3. **Encryption**: Always use encryption for production streams
4. **Monitoring**: Track connection quality metrics continuously
5. **Error Handling**: Implement robust reconnection logic

## Future Enhancements

Potential improvements for the SRT package:

- **Adaptive Bitrate**: Dynamic quality adjustment based on network conditions
- **Multi-path**: Support for multiple network paths
- **FEC Integration**: Forward Error Correction for additional reliability
- **Statistics Export**: Enhanced metrics for network monitoring
- **Load Balancing**: Distribution across multiple SRT listeners
