# Phase 2: Stream Ingestion Layer

## Overview
This phase implements the stream ingestion layer supporting both SRT and RTP protocols. We'll create robust listeners with automatic reconnection, stream registry management, and efficient buffer handling for 25 concurrent 50Mbps HEVC streams.

## Goals
- Implement SRT listener with datarhei/gosrt library
- Implement RTP listener with pion/rtp library
- Create unified stream registry with Redis backend
- Design ring buffer system for stream data (4MB per stream)
- Implement heartbeat monitoring and auto-reconnection
- Add stream authentication and validation
- Create ingestion metrics and monitoring

## New Components Structure
```
internal/
├── ingestion/
│   ├── srt/
│   │   ├── listener.go         # SRT server implementation
│   │   ├── connection.go       # SRT connection handler
│   │   └── config.go          # SRT-specific configuration
│   ├── rtp/
│   │   ├── listener.go         # RTP server implementation
│   │   ├── session.go          # RTP session handler
│   │   └── depacketizer.go     # RTP depacketization
│   ├── registry/
│   │   ├── registry.go         # Stream registry interface
│   │   ├── redis_registry.go   # Redis-backed implementation
│   │   └── stream.go           # Stream metadata structures
│   ├── buffer/
│   │   ├── ring.go             # Ring buffer implementation
│   │   ├── pool.go             # Buffer pool management
│   │   └── metrics.go          # Buffer metrics
│   └── manager.go              # Unified ingestion manager
```

## Implementation Details

### 1. Updated Configuration
```go
// internal/config/config.go (additions)
type Config struct {
    // ... existing fields ...
    Ingestion IngestionConfig `mapstructure:"ingestion"`
}

type IngestionConfig struct {
    SRT      SRTConfig      `mapstructure:"srt"`
    RTP      RTPConfig      `mapstructure:"rtp"`
    Buffer   BufferConfig   `mapstructure:"buffer"`
    Registry RegistryConfig `mapstructure:"registry"`
}

type SRTConfig struct {
    Enabled         bool          `mapstructure:"enabled"`
    ListenAddr      string        `mapstructure:"listen_addr"`
    Port            int           `mapstructure:"port"`
    Latency         time.Duration `mapstructure:"latency"`         // Default 120ms
    MaxBandwidth    int64         `mapstructure:"max_bandwidth"`   // Bits per second
    InputBandwidth  int64         `mapstructure:"input_bandwidth"` // Expected bandwidth
    PayloadSize     int           `mapstructure:"payload_size"`    // MTU-friendly (1316)
    FlowControlWindow int         `mapstructure:"fc_window"`       // Flow control window
    PeerIdleTimeout time.Duration `mapstructure:"peer_idle_timeout"`
    MaxConnections  int           `mapstructure:"max_connections"`
}

type RTPConfig struct {
    Enabled        bool          `mapstructure:"enabled"`
    ListenAddr     string        `mapstructure:"listen_addr"`
    Port           int           `mapstructure:"port"`
    RTCPPort       int           `mapstructure:"rtcp_port"`
    BufferSize     int           `mapstructure:"buffer_size"`
    MaxSessions    int           `mapstructure:"max_sessions"`
    SessionTimeout time.Duration `mapstructure:"session_timeout"`
}

type BufferConfig struct {
    RingSize       int           `mapstructure:"ring_size"`        // Per stream (4MB default)
    PoolSize       int           `mapstructure:"pool_size"`        // Pre-allocated buffers
    WriteTimeout   time.Duration `mapstructure:"write_timeout"`
    ReadTimeout    time.Duration `mapstructure:"read_timeout"`
    MetricsEnabled bool          `mapstructure:"metrics_enabled"`
}

type RegistryConfig struct {
    HeartbeatInterval   time.Duration `mapstructure:"heartbeat_interval"`
    HeartbeatTimeout    time.Duration `mapstructure:"heartbeat_timeout"`
    CleanupInterval     time.Duration `mapstructure:"cleanup_interval"`
    MaxStreamsPerSource int           `mapstructure:"max_streams_per_source"`
}
```

### 2. Stream Registry Implementation
```go
// internal/ingestion/registry/stream.go
package registry

import (
    "time"
    "sync"
)

type StreamType string

const (
    StreamTypeSRT StreamType = "srt"
    StreamTypeRTP StreamType = "rtp"
)

type StreamStatus string

const (
    StatusConnecting StreamStatus = "connecting"
    StatusActive     StreamStatus = "active"
    StatusPaused     StreamStatus = "paused"
    StatusError      StreamStatus = "error"
    StatusClosed     StreamStatus = "closed"
)

type Stream struct {
    ID              string       `json:"id"`
    Type            StreamType   `json:"type"`
    SourceAddr      string       `json:"source_addr"`
    Status          StreamStatus `json:"status"`
    CreatedAt       time.Time    `json:"created_at"`
    LastHeartbeat   time.Time    `json:"last_heartbeat"`
    
    // Stream metadata
    VideoCodec      string       `json:"video_codec"`      // HEVC
    Resolution      string       `json:"resolution"`       // 1920x1080
    Bitrate         int64        `json:"bitrate"`          // bits per second
    FrameRate       float64      `json:"frame_rate"`
    
    // Statistics
    BytesReceived   int64        `json:"bytes_received"`
    PacketsReceived int64        `json:"packets_received"`
    PacketsLost     int64        `json:"packets_lost"`
    
    // Internal
    buffer          *RingBuffer
    mu              sync.RWMutex
}

// internal/ingestion/registry/registry.go
package registry

import (
    "context"
    "fmt"
)

type Registry interface {
    Register(ctx context.Context, stream *Stream) error
    Unregister(ctx context.Context, streamID string) error
    Get(ctx context.Context, streamID string) (*Stream, error)
    List(ctx context.Context) ([]*Stream, error)
    UpdateHeartbeat(ctx context.Context, streamID string) error
    UpdateStatus(ctx context.Context, streamID string, status StreamStatus) error
    UpdateStats(ctx context.Context, streamID string, stats *StreamStats) error
}

type StreamStats struct {
    BytesReceived   int64
    PacketsReceived int64
    PacketsLost     int64
    Bitrate         int64
}

// internal/ingestion/registry/redis_registry.go
package registry

import (
    "context"
    "encoding/json"
    "fmt"
    "time"
    
    "github.com/redis/go-redis/v9"
    "github.com/sirupsen/logrus"
)

type RedisRegistry struct {
    client  *redis.Client
    logger  *logrus.Logger
    prefix  string
    ttl     time.Duration
}

func NewRedisRegistry(client *redis.Client, logger *logrus.Logger) *RedisRegistry {
    return &RedisRegistry{
        client: client,
        logger: logger,
        prefix: "mirror:streams:",
        ttl:    5 * time.Minute,
    }
}

func (r *RedisRegistry) Register(ctx context.Context, stream *Stream) error {
    stream.CreatedAt = time.Now()
    stream.LastHeartbeat = time.Now()
    
    data, err := json.Marshal(stream)
    if err != nil {
        return fmt.Errorf("failed to marshal stream: %w", err)
    }
    
    key := r.prefix + stream.ID
    
    // Use SET with NX to prevent overwriting existing streams
    ok, err := r.client.SetNX(ctx, key, data, r.ttl).Result()
    if err != nil {
        return fmt.Errorf("failed to register stream: %w", err)
    }
    if !ok {
        return fmt.Errorf("stream %s already exists", stream.ID)
    }
    
    // Add to active streams set
    if err := r.client.SAdd(ctx, r.prefix+"active", stream.ID).Err(); err != nil {
        return fmt.Errorf("failed to add to active set: %w", err)
    }
    
    r.logger.WithFields(logrus.Fields{
        "stream_id": stream.ID,
        "type":      stream.Type,
        "source":    stream.SourceAddr,
    }).Info("Stream registered")
    
    return nil
}

func (r *RedisRegistry) UpdateHeartbeat(ctx context.Context, streamID string) error {
    key := r.prefix + streamID
    
    // Get current stream data
    data, err := r.client.Get(ctx, key).Bytes()
    if err != nil {
        return fmt.Errorf("stream not found: %w", err)
    }
    
    var stream Stream
    if err := json.Unmarshal(data, &stream); err != nil {
        return fmt.Errorf("failed to unmarshal stream: %w", err)
    }
    
    // Update heartbeat
    stream.LastHeartbeat = time.Now()
    
    updatedData, err := json.Marshal(stream)
    if err != nil {
        return fmt.Errorf("failed to marshal stream: %w", err)
    }
    
    // Update with new TTL
    if err := r.client.Set(ctx, key, updatedData, r.ttl).Err(); err != nil {
        return fmt.Errorf("failed to update heartbeat: %w", err)
    }
    
    return nil
}
```

### 3. Ring Buffer Implementation
```go
// internal/ingestion/buffer/ring.go
package buffer

import (
    "errors"
    "sync"
    "sync/atomic"
    "time"
)

var (
    ErrBufferFull    = errors.New("buffer full")
    ErrBufferClosed  = errors.New("buffer closed")
    ErrTimeout       = errors.New("operation timeout")
)

type RingBuffer struct {
    data       []byte
    size       int64
    writePos   int64
    readPos    int64
    written    int64
    read       int64
    closed     int32
    
    writeCond  *sync.Cond
    readCond   *sync.Cond
    mu         sync.Mutex
    
    // Metrics
    drops      int64
    maxLatency time.Duration
}

func NewRingBuffer(size int) *RingBuffer {
    rb := &RingBuffer{
        data: make([]byte, size),
        size: int64(size),
    }
    rb.writeCond = sync.NewCond(&rb.mu)
    rb.readCond = sync.NewCond(&rb.mu)
    return rb
}

func (rb *RingBuffer) Write(data []byte) (int, error) {
    if atomic.LoadInt32(&rb.closed) == 1 {
        return 0, ErrBufferClosed
    }
    
    rb.mu.Lock()
    defer rb.mu.Unlock()
    
    dataLen := int64(len(data))
    if dataLen > rb.size {
        return 0, errors.New("data too large for buffer")
    }
    
    // Check available space
    available := rb.size - (rb.written - rb.read)
    if available < dataLen {
        // Drop oldest data if buffer is full
        atomic.AddInt64(&rb.drops, 1)
        rb.read += dataLen - available
        rb.readPos = (rb.readPos + dataLen - available) % rb.size
    }
    
    // Write data (may wrap around)
    written := int64(0)
    for written < dataLen {
        writeSize := min(dataLen-written, rb.size-rb.writePos)
        copy(rb.data[rb.writePos:rb.writePos+writeSize], data[written:written+writeSize])
        rb.writePos = (rb.writePos + writeSize) % rb.size
        written += writeSize
    }
    
    rb.written += dataLen
    rb.readCond.Broadcast()
    
    return int(dataLen), nil
}

func (rb *RingBuffer) Read(data []byte) (int, error) {
    if atomic.LoadInt32(&rb.closed) == 1 && rb.written == rb.read {
        return 0, ErrBufferClosed
    }
    
    rb.mu.Lock()
    defer rb.mu.Unlock()
    
    // Wait for data
    for rb.written == rb.read && atomic.LoadInt32(&rb.closed) == 0 {
        rb.readCond.Wait()
    }
    
    if rb.written == rb.read {
        return 0, ErrBufferClosed
    }
    
    // Read available data
    available := rb.written - rb.read
    toRead := min(int64(len(data)), available)
    
    read := int64(0)
    for read < toRead {
        readSize := min(toRead-read, rb.size-rb.readPos)
        copy(data[read:read+readSize], rb.data[rb.readPos:rb.readPos+readSize])
        rb.readPos = (rb.readPos + readSize) % rb.size
        read += readSize
    }
    
    rb.read += read
    rb.writeCond.Broadcast()
    
    return int(read), nil
}

func (rb *RingBuffer) Close() error {
    if !atomic.CompareAndSwapInt32(&rb.closed, 0, 1) {
        return errors.New("buffer already closed")
    }
    
    rb.mu.Lock()
    rb.readCond.Broadcast()
    rb.writeCond.Broadcast()
    rb.mu.Unlock()
    
    return nil
}

func (rb *RingBuffer) Stats() BufferStats {
    rb.mu.Lock()
    defer rb.mu.Unlock()
    
    return BufferStats{
        Size:        rb.size,
        Written:     rb.written,
        Read:        rb.read,
        Available:   rb.written - rb.read,
        Drops:       atomic.LoadInt64(&rb.drops),
        MaxLatency:  rb.maxLatency,
    }
}
```

### 4. SRT Listener Implementation
```go
// internal/ingestion/srt/listener.go
package srt

import (
    "context"
    "fmt"
    "net"
    "sync"
    "time"
    
    "github.com/datarhei/gosrt"
    "github.com/sirupsen/logrus"
)

type Listener struct {
    config    *SRTConfig
    listener  net.Listener
    registry  registry.Registry
    bufferPool *buffer.BufferPool
    logger    *logrus.Logger
    
    connections sync.Map // streamID -> *Connection
    wg          sync.WaitGroup
    ctx         context.Context
    cancel      context.CancelFunc
}

func NewListener(cfg *SRTConfig, reg registry.Registry, pool *buffer.BufferPool, logger *logrus.Logger) *Listener {
    ctx, cancel := context.WithCancel(context.Background())
    
    return &Listener{
        config:     cfg,
        registry:   reg,
        bufferPool: pool,
        logger:     logger,
        ctx:        ctx,
        cancel:     cancel,
    }
}

func (l *Listener) Start() error {
    srtConfig := srt.Config{
        FC:          int32(l.config.FlowControlWindow),
        InputBW:     l.config.InputBandwidth,
        MaxBW:       l.config.MaxBandwidth,
        Latency:     l.config.Latency,
        PayloadSize: l.config.PayloadSize,
        PeerIdleTimeout: l.config.PeerIdleTimeout,
    }
    
    addr := fmt.Sprintf("%s:%d", l.config.ListenAddr, l.config.Port)
    listener, err := srt.Listen("srt", addr, srtConfig)
    if err != nil {
        return fmt.Errorf("failed to start SRT listener: %w", err)
    }
    
    l.listener = listener
    l.logger.Infof("SRT listener started on %s", addr)
    
    // Start accept loop
    l.wg.Add(1)
    go l.acceptLoop()
    
    // Start monitoring loop
    l.wg.Add(1)
    go l.monitorConnections()
    
    return nil
}

func (l *Listener) acceptLoop() {
    defer l.wg.Done()
    
    for {
        conn, err := l.listener.Accept()
        if err != nil {
            select {
            case <-l.ctx.Done():
                return
            default:
                l.logger.Errorf("Failed to accept SRT connection: %v", err)
                continue
            }
        }
        
        // Get stream ID from connection
        streamID := l.extractStreamID(conn)
        if streamID == "" {
            l.logger.Warn("Connection without stream ID, closing")
            conn.Close()
            continue
        }
        
        // Create connection handler
        l.wg.Add(1)
        go l.handleConnection(streamID, conn)
    }
}

func (l *Listener) handleConnection(streamID string, conn net.Conn) {
    defer l.wg.Done()
    defer conn.Close()
    
    // Get or create buffer
    ringBuffer := l.bufferPool.Get(streamID)
    defer l.bufferPool.Put(streamID, ringBuffer)
    
    // Register stream
    stream := &registry.Stream{
        ID:         streamID,
        Type:       registry.StreamTypeSRT,
        SourceAddr: conn.RemoteAddr().String(),
        Status:     registry.StatusActive,
        VideoCodec: "HEVC",
        Bitrate:    l.config.InputBandwidth,
    }
    
    if err := l.registry.Register(l.ctx, stream); err != nil {
        l.logger.Errorf("Failed to register stream %s: %v", streamID, err)
        return
    }
    defer l.registry.Unregister(l.ctx, streamID)
    
    // Create connection wrapper
    connection := &Connection{
        streamID:   streamID,
        conn:       conn,
        buffer:     ringBuffer,
        registry:   l.registry,
        logger:     l.logger,
        lastActive: time.Now(),
    }
    
    // Store connection
    l.connections.Store(streamID, connection)
    defer l.connections.Delete(streamID)
    
    // Start reading
    if err := connection.ReadLoop(l.ctx); err != nil {
        l.logger.Errorf("Stream %s read error: %v", streamID, err)
    }
}

// internal/ingestion/srt/connection.go
package srt

import (
    "context"
    "io"
    "time"
    
    "github.com/sirupsen/logrus"
)

type Connection struct {
    streamID   string
    conn       net.Conn
    buffer     *buffer.RingBuffer
    registry   registry.Registry
    logger     *logrus.Logger
    
    lastActive time.Time
    stats      ConnectionStats
}

type ConnectionStats struct {
    BytesReceived   int64
    PacketsReceived int64
    PacketsLost     int64
    Bitrate         int64
    LastUpdate      time.Time
}

func (c *Connection) ReadLoop(ctx context.Context) error {
    const bufferSize = 65536 // 64KB read buffer
    readBuffer := make([]byte, bufferSize)
    
    // Stats ticker
    statsTicker := time.NewTicker(5 * time.Second)
    defer statsTicker.Stop()
    
    // Heartbeat ticker
    heartbeatTicker := time.NewTicker(10 * time.Second)
    defer heartbeatTicker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
            
        case <-statsTicker.C:
            c.updateStats()
            
        case <-heartbeatTicker.C:
            if err := c.registry.UpdateHeartbeat(ctx, c.streamID); err != nil {
                c.logger.Warnf("Failed to update heartbeat for %s: %v", c.streamID, err)
            }
            
        default:
            // Set read deadline
            c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
            
            n, err := c.conn.Read(readBuffer)
            if err != nil {
                if err == io.EOF {
                    c.logger.Infof("Stream %s closed by peer", c.streamID)
                    return nil
                }
                return err
            }
            
            // Write to ring buffer
            if _, err := c.buffer.Write(readBuffer[:n]); err != nil {
                c.logger.Warnf("Buffer write error for %s: %v", c.streamID, err)
            }
            
            // Update stats
            c.stats.BytesReceived += int64(n)
            c.stats.PacketsReceived++
            c.lastActive = time.Now()
        }
    }
}

func (c *Connection) updateStats() {
    now := time.Now()
    duration := now.Sub(c.stats.LastUpdate).Seconds()
    
    if duration > 0 {
        c.stats.Bitrate = int64(float64(c.stats.BytesReceived*8) / duration)
    }
    
    stats := &registry.StreamStats{
        BytesReceived:   c.stats.BytesReceived,
        PacketsReceived: c.stats.PacketsReceived,
        PacketsLost:     c.stats.PacketsLost,
        Bitrate:         c.stats.Bitrate,
    }
    
    if err := c.registry.UpdateStats(context.Background(), c.streamID, stats); err != nil {
        c.logger.Warnf("Failed to update stats for %s: %v", c.streamID, err)
    }
    
    c.stats.LastUpdate = now
}
```

### 5. RTP Listener Implementation
```go
// internal/ingestion/rtp/listener.go
package rtp

import (
    "context"
    "fmt"
    "net"
    "sync"
    "time"
    
    "github.com/pion/rtp"
    "github.com/sirupsen/logrus"
)

type Listener struct {
    config     *RTPConfig
    conn       *net.UDPConn
    rtcpConn   *net.UDPConn
    registry   registry.Registry
    bufferPool *buffer.BufferPool
    logger     *logrus.Logger
    
    sessions   sync.Map // sessionID -> *Session
    wg         sync.WaitGroup
    ctx        context.Context
    cancel     context.CancelFunc
}

func NewListener(cfg *RTPConfig, reg registry.Registry, pool *buffer.BufferPool, logger *logrus.Logger) *Listener {
    ctx, cancel := context.WithCancel(context.Background())
    
    return &Listener{
        config:     cfg,
        registry:   reg,
        bufferPool: pool,
        logger:     logger,
        ctx:        ctx,
        cancel:     cancel,
    }
}

func (l *Listener) Start() error {
    // RTP socket
    rtpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", l.config.ListenAddr, l.config.Port))
    if err != nil {
        return fmt.Errorf("failed to resolve RTP address: %w", err)
    }
    
    rtpConn, err := net.ListenUDP("udp", rtpAddr)
    if err != nil {
        return fmt.Errorf("failed to listen on RTP port: %w", err)
    }
    
    // Set buffer sizes
    rtpConn.SetReadBuffer(l.config.BufferSize)
    rtpConn.SetWriteBuffer(l.config.BufferSize)
    
    l.conn = rtpConn
    
    // RTCP socket
    rtcpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", l.config.ListenAddr, l.config.RTCPPort))
    if err != nil {
        return fmt.Errorf("failed to resolve RTCP address: %w", err)
    }
    
    rtcpConn, err := net.ListenUDP("udp", rtcpAddr)
    if err != nil {
        return fmt.Errorf("failed to listen on RTCP port: %w", err)
    }
    
    l.rtcpConn = rtcpConn
    
    l.logger.Infof("RTP listener started on %s:%d (RTCP: %d)", 
        l.config.ListenAddr, l.config.Port, l.config.RTCPPort)
    
    // Start receive loops
    l.wg.Add(2)
    go l.receiveRTP()
    go l.receiveRTCP()
    
    // Start session cleanup
    l.wg.Add(1)
    go l.cleanupSessions()
    
    return nil
}

func (l *Listener) receiveRTP() {
    defer l.wg.Done()
    
    buffer := make([]byte, 1500) // MTU size
    
    for {
        select {
        case <-l.ctx.Done():
            return
        default:
            n, addr, err := l.conn.ReadFromUDP(buffer)
            if err != nil {
                l.logger.Errorf("RTP read error: %v", err)
                continue
            }
            
            // Parse RTP packet
            packet := &rtp.Packet{}
            if err := packet.Unmarshal(buffer[:n]); err != nil {
                l.logger.Warnf("Invalid RTP packet from %s: %v", addr, err)
                continue
            }
            
            // Get or create session
            sessionID := fmt.Sprintf("%s:%d", addr.IP, addr.Port)
            session := l.getOrCreateSession(sessionID, addr, packet.SSRC)
            
            // Process packet
            session.ProcessPacket(packet)
        }
    }
}

// internal/ingestion/rtp/session.go
package rtp

import (
    "context"
    "net"
    "sync"
    "time"
    
    "github.com/pion/rtp"
)

type Session struct {
    ID         string
    RemoteAddr *net.UDPAddr
    SSRC       uint32
    StreamID   string
    
    buffer     *buffer.RingBuffer
    registry   registry.Registry
    logger     *logrus.Logger
    
    lastPacket time.Time
    sequence   uint16
    
    mu         sync.Mutex
    stats      SessionStats
}

type SessionStats struct {
    PacketsReceived uint64
    BytesReceived   uint64
    PacketsLost     uint64
    Jitter          float64
}

func (s *Session) ProcessPacket(packet *rtp.Packet) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // Update timestamp
    s.lastPacket = time.Now()
    
    // Check sequence number
    if s.sequence != 0 {
        expected := s.sequence + 1
        if packet.SequenceNumber != expected {
            // Handle packet loss
            lost := int(packet.SequenceNumber - expected)
            if lost > 0 && lost < 1000 { // Sanity check
                s.stats.PacketsLost += uint64(lost)
            }
        }
    }
    s.sequence = packet.SequenceNumber
    
    // Update stats
    s.stats.PacketsReceived++
    s.stats.BytesReceived += uint64(len(packet.Payload))
    
    // Write payload to buffer
    if _, err := s.buffer.Write(packet.Payload); err != nil {
        s.logger.Warnf("Buffer write error for session %s: %v", s.ID, err)
    }
}
```

### 6. Unified Ingestion Manager
```go
// internal/ingestion/manager.go
package ingestion

import (
    "context"
    "fmt"
    "sync"
    
    "github.com/sirupsen/logrus"
)

type Manager struct {
    config     *config.IngestionConfig
    srtListener *srt.Listener
    rtpListener *rtp.Listener
    registry   registry.Registry
    bufferPool *buffer.BufferPool
    logger     *logrus.Logger
    
    wg         sync.WaitGroup
    ctx        context.Context
    cancel     context.CancelFunc
}

func NewManager(cfg *config.IngestionConfig, redis *redis.Client, logger *logrus.Logger) *Manager {
    ctx, cancel := context.WithCancel(context.Background())
    
    // Create registry
    reg := registry.NewRedisRegistry(redis, logger)
    
    // Create buffer pool
    pool := buffer.NewBufferPool(cfg.Buffer.RingSize, cfg.Buffer.PoolSize)
    
    m := &Manager{
        config:     cfg,
        registry:   reg,
        bufferPool: pool,
        logger:     logger,
        ctx:        ctx,
        cancel:     cancel,
    }
    
    // Create listeners
    if cfg.SRT.Enabled {
        m.srtListener = srt.NewListener(&cfg.SRT, reg, pool, logger)
    }
    
    if cfg.RTP.Enabled {
        m.rtpListener = rtp.NewListener(&cfg.RTP, reg, pool, logger)
    }
    
    return m
}

func (m *Manager) Start() error {
    // Start registry cleanup
    m.wg.Add(1)
    go m.registryMaintenance()
    
    // Start SRT listener
    if m.srtListener != nil {
        if err := m.srtListener.Start(); err != nil {
            return fmt.Errorf("failed to start SRT listener: %w", err)
        }
        m.logger.Info("SRT ingestion started")
    }
    
    // Start RTP listener
    if m.rtpListener != nil {
        if err := m.rtpListener.Start(); err != nil {
            return fmt.Errorf("failed to start RTP listener: %w", err)
        }
        m.logger.Info("RTP ingestion started")
    }
    
    return nil
}

func (m *Manager) GetStream(streamID string) (*registry.Stream, error) {
    return m.registry.Get(m.ctx, streamID)
}

func (m *Manager) GetBuffer(streamID string) *buffer.RingBuffer {
    return m.bufferPool.Get(streamID)
}

func (m *Manager) ListStreams() ([]*registry.Stream, error) {
    return m.registry.List(m.ctx)
}

func (m *Manager) registryMaintenance() {
    defer m.wg.Done()
    
    ticker := time.NewTicker(m.config.Registry.CleanupInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-m.ctx.Done():
            return
            
        case <-ticker.C:
            // Clean up stale streams
            streams, err := m.registry.List(m.ctx)
            if err != nil {
                m.logger.Errorf("Failed to list streams: %v", err)
                continue
            }
            
            for _, stream := range streams {
                if time.Since(stream.LastHeartbeat) > m.config.Registry.HeartbeatTimeout {
                    m.logger.Infof("Removing stale stream %s", stream.ID)
                    m.registry.Unregister(m.ctx, stream.ID)
                    m.bufferPool.Remove(stream.ID)
                }
            }
        }
    }
}
```

### 7. Updated Main Application
```go
// cmd/mirror/main.go (additions)
import (
    "github.com/yourusername/mirror/internal/ingestion"
)

func main() {
    // ... existing code ...
    
    // Create ingestion manager
    ingestionMgr := ingestion.NewManager(&cfg.Ingestion, redisClient, log)
    
    // Start ingestion
    if err := ingestionMgr.Start(); err != nil {
        log.Fatalf("Failed to start ingestion: %v", err)
    }
    log.Info("Ingestion layer started")
    
    // Update server with ingestion manager
    srv := server.New(&cfg.Server, log, redisClient, ingestionMgr)
    
    // ... rest of existing code ...
}
```

### 8. Ingestion API Endpoints
```go
// internal/server/routes.go (additions)
func (s *Server) setupRoutes() {
    // ... existing routes ...
    
    // Ingestion endpoints
    api := s.router.PathPrefix("/api/v1").Subrouter()
    api.HandleFunc("/streams", s.handleListStreams).Methods("GET")
    api.HandleFunc("/streams/{id}", s.handleGetStream).Methods("GET")
    api.HandleFunc("/streams/{id}/stats", s.handleStreamStats).Methods("GET")
}

func (s *Server) handleListStreams(w http.ResponseWriter, r *http.Request) {
    streams, err := s.ingestionMgr.ListStreams()
    if err != nil {
        s.handleError(w, err, http.StatusInternalServerError)
        return
    }
    
    s.respondJSON(w, map[string]interface{}{
        "streams": streams,
        "count":   len(streams),
    })
}

func (s *Server) handleGetStream(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["id"]
    
    stream, err := s.ingestionMgr.GetStream(streamID)
    if err != nil {
        s.handleError(w, err, http.StatusNotFound)
        return
    }
    
    s.respondJSON(w, stream)
}
```

### 9. Updated Configuration
```yaml
# configs/default.yaml (additions)
ingestion:
  srt:
    enabled: true
    listen_addr: "0.0.0.0"
    port: 6000
    latency: 120ms
    max_bandwidth: 60000000    # 60 Mbps
    input_bandwidth: 55000000  # 55 Mbps
    payload_size: 1316
    fc_window: 25600
    peer_idle_timeout: 30s
    max_connections: 30
    
  rtp:
    enabled: true
    listen_addr: "0.0.0.0"
    port: 5004
    rtcp_port: 5005
    buffer_size: 2097152  # 2MB
    max_sessions: 30
    session_timeout: 30s
    
  buffer:
    ring_size: 4194304    # 4MB per stream
    pool_size: 30         # Pre-allocate for 30 streams
    write_timeout: 100ms
    read_timeout: 100ms
    metrics_enabled: true
    
  registry:
    heartbeat_interval: 10s
    heartbeat_timeout: 30s
    cleanup_interval: 60s
    max_streams_per_source: 5
```

## Testing Requirements

### Unit Tests
- Ring buffer operations (concurrent read/write)
- Stream registry CRUD operations
- SRT connection handling
- RTP packet parsing and session management
- Buffer pool allocation and cleanup

### Integration Tests
- Multiple concurrent SRT connections
- RTP session creation and timeout
- Registry heartbeat and cleanup
- Buffer overflow handling
- Graceful shutdown with active streams

### Load Tests
- 25 concurrent 50Mbps streams
- Sustained throughput over 1 hour
- Memory usage stability
- CPU usage under 50%

## Monitoring and Metrics

### Prometheus Metrics
```go
// Stream ingestion metrics
stream_ingestion_active_total{protocol="srt|rtp"}
stream_ingestion_bytes_total{stream_id, protocol}
stream_ingestion_packets_total{stream_id, protocol}
stream_ingestion_errors_total{stream_id, error_type}
stream_ingestion_bitrate{stream_id}

// Buffer metrics
buffer_usage_bytes{stream_id}
buffer_drops_total{stream_id}
buffer_read_latency_seconds{stream_id}
buffer_write_latency_seconds{stream_id}

// Connection metrics
connection_duration_seconds{stream_id, protocol}
connection_reconnects_total{stream_id}
```

## Deliverables
1. Complete SRT and RTP listener implementations
2. Unified stream registry with Redis backend
3. Efficient ring buffer system with metrics
4. Stream authentication and validation
5. RESTful API for stream management
6. Comprehensive test suite
7. Prometheus metrics integration
8. Load test results showing 25 concurrent streams

## Success Criteria
- Support 25 concurrent 50Mbps HEVC streams
- Less than 10ms buffer latency
- Zero packet loss under normal conditions
- Automatic reconnection within 5 seconds
- Memory usage under 1GB per stream
- CPU usage under 50% for ingestion
- All integration tests passing
- Graceful handling of stream disconnections

## Next Phase Preview
Phase 3 will implement the GPU-accelerated transcoding pipeline using FFmpeg/NVENC integration, with resource pooling and pipeline orchestration for converting HEVC to H.264.
