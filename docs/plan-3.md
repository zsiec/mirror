# Mirror Streaming Platform - Part 3: Stream Ingestion Components

## Table of Contents
1. [Ingestion Service Overview](#ingestion-service-overview)
2. [Stream Registry](#stream-registry)
3. [SRT Implementation](#srt-implementation)
4. [RTP Implementation](#rtp-implementation)
5. [Stream Models](#stream-models)
6. [Auto-Reconnection Logic](#auto-reconnection-logic)

## Ingestion Service Overview

### internal/ingestion/service.go
```go
package ingestion

import (
    "context"
    "errors"
    "fmt"
    "sync"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
    "github.com/redis/go-redis/v9"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/config"
    "github.com/zsiec/mirror/pkg/models"
    "github.com/zsiec/mirror/pkg/utils"
)

var (
    // Metrics
    activeStreamsGauge = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "mirror_active_streams_total",
        Help: "Total number of active streams",
    })
    
    streamBytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "mirror_stream_bytes_total",
        Help: "Total bytes received per stream",
    }, []string{"stream_id", "protocol"})
    
    streamErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "mirror_stream_errors_total",
        Help: "Total stream errors",
    }, []string{"stream_id", "protocol", "error_type"})
)

// Service handles all stream ingestion
type Service struct {
    ctx    context.Context
    cancel context.CancelFunc
    cfg    config.IngestionConfig
    redis  *redis.Client

    // Stream management
    registry *StreamRegistry
    streams  sync.Map // map[string]*Stream

    // Protocol handlers
    srtHandler *SRTHandler
    rtpHandler *RTPHandler

    // Buffer pool for memory efficiency
    bufferPool *utils.MediaBufferPool

    // Control
    wg      sync.WaitGroup
    errChan chan error
}

// New creates a new ingestion service
func New(ctx context.Context, cfg config.IngestionConfig, redisClient *redis.Client) (*Service, error) {
    ctx, cancel := context.WithCancel(ctx)

    s := &Service{
        ctx:        ctx,
        cancel:     cancel,
        cfg:        cfg,
        redis:      redisClient,
        bufferPool: utils.NewMediaBufferPool(),
        errChan:    make(chan error, 10),
    }

    // Initialize stream registry
    s.registry = NewStreamRegistry(redisClient, cfg.MaxStreams)

    // Initialize protocol handlers
    if cfg.SRT.Enabled {
        s.srtHandler = NewSRTHandler(s, cfg.SRT)
    }

    if cfg.RTP.Enabled {
        s.rtpHandler = NewRTPHandler(s, cfg.RTP)
    }

    return s, nil
}

// Start begins accepting stream connections
func (s *Service) Start() error {
    log.Info().Msg("Starting ingestion service")

    // Start protocol listeners
    if s.srtHandler != nil {
        s.wg.Add(1)
        go func() {
            defer s.wg.Done()
            if err := s.srtHandler.Start(); err != nil {
                s.errChan <- fmt.Errorf("SRT handler failed: %w", err)
            }
        }()
    }

    if s.rtpHandler != nil {
        s.wg.Add(1)
        go func() {
            defer s.wg.Done()
            if err := s.rtpHandler.Start(); err != nil {
                s.errChan <- fmt.Errorf("RTP handler failed: %w", err)
            }
        }()
    }

    // Monitor for errors
    select {
    case err := <-s.errChan:
        return err
    case <-s.ctx.Done():
        return s.ctx.Err()
    }
}

// Stop gracefully shuts down the service
func (s *Service) Stop(ctx context.Context) error {
    log.Info().Msg("Stopping ingestion service")

    // Cancel context to signal shutdown
    s.cancel()

    // Stop all active streams
    s.streams.Range(func(key, value interface{}) bool {
        stream := value.(*Stream)
        if err := stream.Stop(); err != nil {
            log.Error().Err(err).Str("stream_id", stream.ID).Msg("Error stopping stream")
        }
        return true
    })

    // Stop protocol handlers
    if s.srtHandler != nil {
        s.srtHandler.Stop()
    }

    if s.rtpHandler != nil {
        s.rtpHandler.Stop()
    }

    // Wait for all goroutines
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        log.Info().Msg("Ingestion service stopped")
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

// RegisterStream registers a new stream
func (s *Service) RegisterStream(streamID string, protocol string, metadata models.StreamMetadata) (*Stream, error) {
    // Check capacity
    if !s.registry.CanAddStream() {
        streamErrorsTotal.WithLabelValues(streamID, protocol, "capacity_exceeded").Inc()
        return nil, utils.ErrCapacityExceeded
    }

    // Check if stream already exists
    if _, exists := s.streams.Load(streamID); exists {
        streamErrorsTotal.WithLabelValues(streamID, protocol, "already_exists").Inc()
        return nil, utils.ErrStreamExists
    }

    // Create new stream
    stream := NewStream(streamID, protocol, metadata, s.bufferPool)

    // Register in registry
    if err := s.registry.Register(s.ctx, streamID, metadata); err != nil {
        return nil, fmt.Errorf("failed to register stream: %w", err)
    }

    // Store stream
    s.streams.Store(streamID, stream)
    activeStreamsGauge.Inc()

    log.Info().
        Str("stream_id", streamID).
        Str("protocol", protocol).
        Msg("Stream registered")

    return stream, nil
}

// UnregisterStream removes a stream
func (s *Service) UnregisterStream(streamID string) error {
    stream, exists := s.streams.LoadAndDelete(streamID)
    if !exists {
        return utils.ErrStreamNotFound
    }

    // Stop stream
    str := stream.(*Stream)
    if err := str.Stop(); err != nil {
        log.Error().Err(err).Str("stream_id", streamID).Msg("Error stopping stream")
    }

    // Remove from registry
    if err := s.registry.Unregister(s.ctx, streamID); err != nil {
        log.Error().Err(err).Str("stream_id", streamID).Msg("Error unregistering stream")
    }

    activeStreamsGauge.Dec()

    log.Info().Str("stream_id", streamID).Msg("Stream unregistered")
    return nil
}

// GetStream retrieves a stream by ID
func (s *Service) GetStream(streamID string) (*Stream, error) {
    stream, exists := s.streams.Load(streamID)
    if !exists {
        return nil, utils.ErrStreamNotFound
    }
    return stream.(*Stream), nil
}

// ListStreams returns all active streams
func (s *Service) ListStreams() []models.StreamInfo {
    var streams []models.StreamInfo

    s.streams.Range(func(key, value interface{}) bool {
        stream := value.(*Stream)
        info := models.StreamInfo{
            ID:       stream.ID,
            Protocol: stream.Protocol,
            Status:   stream.GetStatus(),
            Metadata: stream.Metadata,
            Stats:    stream.GetStats(),
        }
        streams = append(streams, info)
        return true
    })

    return streams
}

// Health returns service health status
func (s *Service) Health() models.ServiceHealth {
    activeCount := 0
    s.streams.Range(func(_, _ interface{}) bool {
        activeCount++
        return true
    })

    return models.ServiceHealth{
        Healthy: true,
        Status:  "operational",
        Details: map[string]interface{}{
            "active_streams": activeCount,
            "max_streams":    s.cfg.MaxStreams,
            "srt_enabled":    s.cfg.SRT.Enabled,
            "rtp_enabled":    s.cfg.RTP.Enabled,
        },
    }
}
```

## Stream Registry

### internal/ingestion/registry.go
```go
package ingestion

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"
    "time"

    "github.com/redis/go-redis/v9"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/pkg/models"
)

const (
    streamKeyPrefix = "mirror:stream:"
    streamListKey   = "mirror:streams:active"
    streamLockTTL   = 30 * time.Second
)

// StreamRegistry manages stream registration in Redis
type StreamRegistry struct {
    redis      *redis.Client
    maxStreams int
    mu         sync.RWMutex
}

// NewStreamRegistry creates a new registry
func NewStreamRegistry(redis *redis.Client, maxStreams int) *StreamRegistry {
    return &StreamRegistry{
        redis:      redis,
        maxStreams: maxStreams,
    }
}

// Register adds a stream to the registry
func (r *StreamRegistry) Register(ctx context.Context, streamID string, metadata models.StreamMetadata) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    // Check capacity
    count, err := r.redis.SCard(ctx, streamListKey).Result()
    if err != nil {
        return fmt.Errorf("failed to get stream count: %w", err)
    }

    if int(count) >= r.maxStreams {
        return utils.ErrCapacityExceeded
    }

    // Prepare stream data
    streamData := models.StreamData{
        ID:         streamID,
        Metadata:   metadata,
        StartedAt:  time.Now(),
        LastUpdate: time.Now(),
    }

    data, err := json.Marshal(streamData)
    if err != nil {
        return fmt.Errorf("failed to marshal stream data: %w", err)
    }

    // Store in Redis with transaction
    pipe := r.redis.TxPipeline()
    
    // Add to stream hash
    pipe.Set(ctx, streamKeyPrefix+streamID, data, 0)
    
    // Add to active set
    pipe.SAdd(ctx, streamListKey, streamID)
    
    // Execute transaction
    _, err = pipe.Exec(ctx)
    if err != nil {
        return fmt.Errorf("failed to register stream: %w", err)
    }

    return nil
}

// Unregister removes a stream from the registry
func (r *StreamRegistry) Unregister(ctx context.Context, streamID string) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    pipe := r.redis.TxPipeline()
    
    // Remove from hash
    pipe.Del(ctx, streamKeyPrefix+streamID)
    
    // Remove from active set
    pipe.SRem(ctx, streamListKey, streamID)
    
    _, err := pipe.Exec(ctx)
    if err != nil {
        return fmt.Errorf("failed to unregister stream: %w", err)
    }

    return nil
}

// UpdateHeartbeat updates stream heartbeat
func (r *StreamRegistry) UpdateHeartbeat(ctx context.Context, streamID string) error {
    key := streamKeyPrefix + streamID
    
    // Get current data
    data, err := r.redis.Get(ctx, key).Bytes()
    if err != nil {
        return fmt.Errorf("failed to get stream data: %w", err)
    }

    var streamData models.StreamData
    if err := json.Unmarshal(data, &streamData); err != nil {
        return fmt.Errorf("failed to unmarshal stream data: %w", err)
    }

    // Update timestamp
    streamData.LastUpdate = time.Now()

    // Save back
    newData, err := json.Marshal(streamData)
    if err != nil {
        return fmt.Errorf("failed to marshal stream data: %w", err)
    }

    return r.redis.Set(ctx, key, newData, 0).Err()
}

// CanAddStream checks if a new stream can be added
func (r *StreamRegistry) CanAddStream() bool {
    r.mu.RLock()
    defer r.mu.RUnlock()

    count, err := r.redis.SCard(context.Background(), streamListKey).Result()
    if err != nil {
        log.Error().Err(err).Msg("Failed to get stream count")
        return false
    }

    return int(count) < r.maxStreams
}

// GetStreamInfo retrieves stream information
func (r *StreamRegistry) GetStreamInfo(ctx context.Context, streamID string) (*models.StreamData, error) {
    data, err := r.redis.Get(ctx, streamKeyPrefix+streamID).Bytes()
    if err != nil {
        if err == redis.Nil {
            return nil, utils.ErrStreamNotFound
        }
        return nil, fmt.Errorf("failed to get stream data: %w", err)
    }

    var streamData models.StreamData
    if err := json.Unmarshal(data, &streamData); err != nil {
        return nil, fmt.Errorf("failed to unmarshal stream data: %w", err)
    }

    return &streamData, nil
}

// ListActiveStreams returns all active stream IDs
func (r *StreamRegistry) ListActiveStreams(ctx context.Context) ([]string, error) {
    return r.redis.SMembers(ctx, streamListKey).Result()
}
```

## SRT Implementation

### internal/ingestion/srt.go
```go
package ingestion

import (
    "context"
    "fmt"
    "io"
    "net"
    "sync"
    "time"

    "github.com/datarhei/gosrt"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/config"
    "github.com/zsiec/mirror/pkg/models"
    "github.com/zsiec/mirror/pkg/utils"
)

// SRTHandler manages SRT stream connections
type SRTHandler struct {
    service  *Service
    cfg      config.SRTConfig
    listener net.Listener
    
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

// NewSRTHandler creates a new SRT handler
func NewSRTHandler(service *Service, cfg config.SRTConfig) *SRTHandler {
    ctx, cancel := context.WithCancel(service.ctx)
    
    return &SRTHandler{
        service: service,
        cfg:     cfg,
        ctx:     ctx,
        cancel:  cancel,
    }
}

// Start begins listening for SRT connections
func (h *SRTHandler) Start() error {
    srtConfig := srt.Config{
        FC:          25600,                           // Flow control window
        InputBW:     int64(h.cfg.MaxBitrate),        // Max input bandwidth
        MaxBW:       int64(h.cfg.MaxBitrate * 1.2),  // 20% overhead
        Latency:     h.cfg.Latency,
        RecvBuf:     h.cfg.BufferSize,
        PayloadSize: 1316, // MTU-friendly
        
        // Connection callbacks
        OnListenerCallback: h.onListenerCallback,
        OnConnectCallback:  h.onConnectCallback,
        OnCloseCallback:    h.onCloseCallback,
    }

    addr := fmt.Sprintf(":%d", h.cfg.Port)
    ln, err := srt.Listen("srt", addr, srtConfig)
    if err != nil {
        return fmt.Errorf("failed to create SRT listener: %w", err)
    }

    h.listener = ln
    log.Info().Str("addr", addr).Msg("SRT listener started")

    // Accept connections
    h.wg.Add(1)
    go h.acceptLoop()

    return nil
}

// Stop closes the SRT listener
func (h *SRTHandler) Stop() {
    log.Info().Msg("Stopping SRT handler")
    
    h.cancel()
    
    if h.listener != nil {
        h.listener.Close()
    }
    
    h.wg.Wait()
}

func (h *SRTHandler) acceptLoop() {
    defer h.wg.Done()

    for {
        conn, err := h.listener.Accept()
        if err != nil {
            select {
            case <-h.ctx.Done():
                return
            default:
                log.Error().Err(err).Msg("Failed to accept SRT connection")
                continue
            }
        }

        h.wg.Add(1)
        go h.handleConnection(conn)
    }
}

func (h *SRTHandler) handleConnection(conn net.Conn) {
    defer h.wg.Done()
    defer conn.Close()

    srtConn, ok := conn.(*srt.Conn)
    if !ok {
        log.Error().Msg("Invalid SRT connection type")
        return
    }

    // Get stream ID from SRT socket
    streamID := srtConn.StreamId()
    if streamID == "" {
        log.Error().Msg("No stream ID provided in SRT connection")
        return
    }

    log.Info().
        Str("stream_id", streamID).
        Str("remote_addr", conn.RemoteAddr().String()).
        Msg("New SRT connection")

    // Create stream metadata
    metadata := models.StreamMetadata{
        Protocol:   "SRT",
        RemoteAddr: conn.RemoteAddr().String(),
        Bitrate:    h.cfg.MaxBitrate,
        Resolution: "1920x1080", // Will be detected from stream
        Codec:      "HEVC",
    }

    // Register stream
    stream, err := h.service.RegisterStream(streamID, "SRT", metadata)
    if err != nil {
        log.Error().Err(err).Str("stream_id", streamID).Msg("Failed to register stream")
        return
    }
    defer h.service.UnregisterStream(streamID)

    // Update stream status
    stream.SetStatus(models.StreamStatusActive)
    streamBytesTotal.WithLabelValues(streamID, "SRT").Add(0) // Initialize counter

    // Start reading data
    buffer := h.service.bufferPool.GetBuffer(h.cfg.BufferSize)
    defer h.service.bufferPool.PutBuffer(buffer)

    // Auto-reconnection context
    reconnectCtx, reconnectCancel := context.WithCancel(h.ctx)
    defer reconnectCancel()

    // Start heartbeat
    h.wg.Add(1)
    go h.heartbeatLoop(reconnectCtx, streamID)

    for {
        select {
        case <-h.ctx.Done():
            return
        default:
            // Set read deadline
            conn.SetReadDeadline(time.Now().Add(10 * time.Second))
            
            n, err := conn.Read(buffer)
            if err != nil {
                if err == io.EOF {
                    log.Info().Str("stream_id", streamID).Msg("SRT stream ended")
                } else {
                    log.Error().Err(err).Str("stream_id", streamID).Msg("SRT read error")
                    streamErrorsTotal.WithLabelValues(streamID, "SRT", "read_error").Inc()
                }
                
                // Handle reconnection
                if h.shouldReconnect(stream) {
                    stream.SetStatus(models.StreamStatusReconnecting)
                    log.Info().Str("stream_id", streamID).Msg("Waiting for stream reconnection")
                    
                    // Wait for reconnection timeout
                    select {
                    case <-time.After(h.service.cfg.ReconnectDelay):
                        log.Warn().Str("stream_id", streamID).Msg("Stream reconnection timeout")
                        return
                    case <-h.ctx.Done():
                        return
                    }
                }
                return
            }

            // Write to stream buffer
            if err := stream.Write(buffer[:n]); err != nil {
                log.Error().Err(err).Str("stream_id", streamID).Msg("Failed to write to stream buffer")
                streamErrorsTotal.WithLabelValues(streamID, "SRT", "buffer_error").Inc()
                return
            }

            // Update metrics
            streamBytesTotal.WithLabelValues(streamID, "SRT").Add(float64(n))
            stream.UpdateStats(int64(n))
        }
    }
}

func (h *SRTHandler) heartbeatLoop(ctx context.Context, streamID string) {
    defer h.wg.Done()
    
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if err := h.service.registry.UpdateHeartbeat(context.Background(), streamID); err != nil {
                log.Error().Err(err).Str("stream_id", streamID).Msg("Failed to update heartbeat")
            }
        }
    }
}

func (h *SRTHandler) shouldReconnect(stream *Stream) bool {
    // Check if stream has auto-reconnect enabled
    return stream.Metadata.AutoReconnect
}

// Callbacks for SRT events
func (h *SRTHandler) onListenerCallback(addr net.Addr) bool {
    log.Debug().Str("addr", addr.String()).Msg("SRT listener callback")
    return true
}

func (h *SRTHandler) onConnectCallback(req srt.ConnectRequest) bool {
    log.Debug().
        Str("stream_id", req.StreamId()).
        Str("remote", req.RemoteAddr().String()).
        Msg("SRT connect request")
    
    // Validate stream ID
    if req.StreamId() == "" {
        log.Warn().Msg("Rejecting SRT connection without stream ID")
        return false
    }
    
    // Check capacity
    if !h.service.registry.CanAddStream() {
        log.Warn().Msg("Rejecting SRT connection: capacity exceeded")
        return false
    }
    
    return true
}

func (h *SRTHandler) onCloseCallback(addr net.Addr) {
    log.Debug().Str("addr", addr.String()).Msg("SRT connection closed")
}
```

## RTP Implementation

### internal/ingestion/rtp.go
```go
package ingestion

import (
    "context"
    "fmt"
    "net"
    "sync"
    "time"

    "github.com/pion/rtp"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/config"
    "github.com/zsiec/mirror/pkg/models"
)

// RTPHandler manages RTP stream connections
type RTPHandler struct {
    service *Service
    cfg     config.RTPConfig
    conn    *net.UDPConn
    
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
    
    // Active RTP sessions indexed by SSRC
    sessions sync.Map // map[uint32]*RTPSession
}

// RTPSession represents an active RTP stream
type RTPSession struct {
    streamID   string
    ssrc       uint32
    stream     *Stream
    lastPacket time.Time
    sequence   uint16
    timestamp  uint32
}

// NewRTPHandler creates a new RTP handler
func NewRTPHandler(service *Service, cfg config.RTPConfig) *RTPHandler {
    ctx, cancel := context.WithCancel(service.ctx)
    
    return &RTPHandler{
        service: service,
        cfg:     cfg,
        ctx:     ctx,
        cancel:  cancel,
    }
}

// Start begins listening for RTP packets
func (h *RTPHandler) Start() error {
    addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", h.cfg.Port))
    if err != nil {
        return fmt.Errorf("failed to resolve UDP address: %w", err)
    }

    conn, err := net.ListenUDP("udp", addr)
    if err != nil {
        return fmt.Errorf("failed to create UDP listener: %w", err)
    }

    // Set buffer sizes
    if err := conn.SetReadBuffer(h.cfg.BufferSize); err != nil {
        log.Warn().Err(err).Msg("Failed to set UDP read buffer size")
    }

    h.conn = conn
    log.Info().Str("addr", addr.String()).Msg("RTP listener started")

    // Start packet reader
    h.wg.Add(1)
    go h.readLoop()

    // Start session cleanup
    h.wg.Add(1)
    go h.cleanupLoop()

    return nil
}

// Stop closes the RTP listener
func (h *RTPHandler) Stop() {
    log.Info().Msg("Stopping RTP handler")
    
    h.cancel()
    
    if h.conn != nil {
        h.conn.Close()
    }
    
    // Close all sessions
    h.sessions.Range(func(key, value interface{}) bool {
        session := value.(*RTPSession)
        h.service.UnregisterStream(session.streamID)
        return true
    })
    
    h.wg.Wait()
}

func (h *RTPHandler) readLoop() {
    defer h.wg.Done()

    buffer := make([]byte, 1500) // Max RTP packet size
    packet := &rtp.Packet{}

    for {
        select {
        case <-h.ctx.Done():
            return
        default:
            // Set read deadline
            h.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
            
            n, addr, err := h.conn.ReadFromUDP(buffer)
            if err != nil {
                if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
                    continue
                }
                if h.ctx.Err() == nil {
                    log.Error().Err(err).Msg("RTP read error")
                }
                continue
            }

            // Parse RTP packet
            if err := packet.Unmarshal(buffer[:n]); err != nil {
                log.Debug().Err(err).Msg("Failed to parse RTP packet")
                continue
            }

            // Handle packet
            h.handlePacket(packet, addr)
        }
    }
}

func (h *RTPHandler) handlePacket(packet *rtp.Packet, addr *net.UDPAddr) {
    // Get or create session
    sessionI, _ := h.sessions.LoadOrStore(packet.SSRC, &RTPSession{
        ssrc:       packet.SSRC,
        lastPacket: time.Now(),
    })
    
    session := sessionI.(*RTPSession)
    session.lastPacket = time.Now()

    // Check for new stream
    if session.stream == nil {
        streamID := fmt.Sprintf("rtp_%d", packet.SSRC)
        
        // Create stream metadata
        metadata := models.StreamMetadata{
            Protocol:   "RTP",
            RemoteAddr: addr.String(),
            Codec:      h.detectCodec(packet.PayloadType),
        }

        // Register stream
        stream, err := h.service.RegisterStream(streamID, "RTP", metadata)
        if err != nil {
            log.Error().Err(err).Uint32("ssrc", packet.SSRC).Msg("Failed to register RTP stream")
            h.sessions.Delete(packet.SSRC)
            return
        }

        session.streamID = streamID
        session.stream = stream
        session.sequence = packet.SequenceNumber
        session.timestamp = packet.Timestamp

        stream.SetStatus(models.StreamStatusActive)
        streamBytesTotal.WithLabelValues(streamID, "RTP").Add(0)

        log.Info().
            Str("stream_id", streamID).
            Uint32("ssrc", packet.SSRC).
            Str("remote_addr", addr.String()).
            Msg("New RTP stream")
    }

    // Check sequence number
    expectedSeq := session.sequence + 1
    if packet.SequenceNumber != expectedSeq && session.sequence != 0 {
        gap := int(packet.SequenceNumber) - int(expectedSeq)
        if gap > 0 {
            log.Warn().
                Str("stream_id", session.streamID).
                Int("gap", gap).
                Msg("RTP packet loss detected")
            streamErrorsTotal.WithLabelValues(session.streamID, "RTP", "packet_loss").Add(float64(gap))
        }
    }
    session.sequence = packet.SequenceNumber

    // Write payload to stream
    if err := session.stream.Write(packet.Payload); err != nil {
        log.Error().Err(err).Str("stream_id", session.streamID).Msg("Failed to write RTP payload")
        streamErrorsTotal.WithLabelValues(session.streamID, "RTP", "buffer_error").Inc()
        return
    }

    // Update metrics
    streamBytesTotal.WithLabelValues(session.streamID, "RTP").Add(float64(len(packet.Payload)))
    session.stream.UpdateStats(int64(len(packet.Payload)))
}

func (h *RTPHandler) cleanupLoop() {
    defer h.wg.Done()
    
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-h.ctx.Done():
            return
        case <-ticker.C:
            h.cleanupSessions()
        }
    }
}

func (h *RTPHandler) cleanupSessions() {
    timeout := 60 * time.Second
    now := time.Now()

    h.sessions.Range(func(key, value interface{}) bool {
        session := value.(*RTPSession)
        
        if now.Sub(session.lastPacket) > timeout {
            log.Info().
                Str("stream_id", session.streamID).
                Uint32("ssrc", session.ssrc).
                Msg("RTP session timeout")
            
            h.service.UnregisterStream(session.streamID)
            h.sessions.Delete(key)
        }
        
        return true
    })
}

func (h *RTPHandler) detectCodec(payloadType uint8) string {
    // Common RTP payload types
    switch payloadType {
    case 96: // Dynamic, often H.264
        return "H264"
    case 97: // Dynamic, often H.265/HEVC
        return "HEVC"
    case 98: // Dynamic, often VP8
        return "VP8"
    case 99: // Dynamic, often VP9
        return "VP9"
    default:
        return fmt.Sprintf("PT%d", payloadType)
    }
}
```

## Stream Models

### internal/ingestion/stream.go
```go
package ingestion

import (
    "sync"
    "sync/atomic"
    "time"

    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/pkg/models"
    "github.com/zsiec/mirror/pkg/utils"
)

// Stream represents an active media stream
type Stream struct {
    ID       string
    Protocol string
    Metadata models.StreamMetadata
    
    // Status
    status atomic.Value // models.StreamStatus
    
    // Statistics
    stats struct {
        sync.RWMutex
        BytesReceived int64
        PacketsReceived int64
        LastUpdate    time.Time
        Bitrate       float64
    }
    
    // Data flow
    buffer     *utils.RingBuffer
    bufferPool *utils.MediaBufferPool
    
    // Output channels for downstream processing
    outputMu sync.RWMutex
    outputs  map[string]chan []byte
    
    // Control
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

// NewStream creates a new stream
func NewStream(id, protocol string, metadata models.StreamMetadata, bufferPool *utils.MediaBufferPool) *Stream {
    ctx, cancel := context.WithCancel(context.Background())
    
    s := &Stream{
        ID:         id,
        Protocol:   protocol,
        Metadata:   metadata,
        buffer:     utils.NewRingBuffer(4 * 1024 * 1024), // 4MB buffer
        bufferPool: bufferPool,
        outputs:    make(map[string]chan []byte),
        ctx:        ctx,
        cancel:     cancel,
    }
    
    s.status.Store(models.StreamStatusConnecting)
    s.stats.LastUpdate = time.Now()
    
    // Start output distributor
    s.wg.Add(1)
    go s.distributeOutput()
    
    return s
}

// Write adds data to the stream buffer
func (s *Stream) Write(data []byte) error {
    select {
    case <-s.ctx.Done():
        return s.ctx.Err()
    default:
        _, err := s.buffer.Write(data)
        return err
    }
}

// AddOutput adds a new output channel
func (s *Stream) AddOutput(id string, bufferSize int) chan []byte {
    s.outputMu.Lock()
    defer s.outputMu.Unlock()
    
    ch := make(chan []byte, bufferSize)
    s.outputs[id] = ch
    
    log.Debug().
        Str("stream_id", s.ID).
        Str("output_id", id).
        Msg("Added stream output")
    
    return ch
}

// RemoveOutput removes an output channel
func (s *Stream) RemoveOutput(id string) {
    s.outputMu.Lock()
    defer s.outputMu.Unlock()
    
    if ch, exists := s.outputs[id]; exists {
        close(ch)
        delete(s.outputs, id)
        
        log.Debug().
            Str("stream_id", s.ID).
            Str("output_id", id).
            Msg("Removed stream output")
    }
}

// Stop gracefully stops the stream
func (s *Stream) Stop() error {
    log.Info().Str("stream_id", s.ID).Msg("Stopping stream")
    
    s.SetStatus(models.StreamStatusStopped)
    s.cancel()
    
    // Close all outputs
    s.outputMu.Lock()
    for id, ch := range s.outputs {
        close(ch)
        delete(s.outputs, id)
    }
    s.outputMu.Unlock()
    
    s.wg.Wait()
    return nil
}

// SetStatus updates the stream status
func (s *Stream) SetStatus(status models.StreamStatus) {
    s.status.Store(status)
    log.Debug().
        Str("stream_id", s.ID).
        Str("status", string(status)).
        Msg("Stream status updated")
}

// GetStatus returns the current stream status
func (s *Stream) GetStatus() models.StreamStatus {
    return s.status.Load().(models.StreamStatus)
}

// UpdateStats updates stream statistics
func (s *Stream) UpdateStats(bytes int64) {
    s.stats.Lock()
    defer s.stats.Unlock()
    
    now := time.Now()
    duration := now.Sub(s.stats.LastUpdate).Seconds()
    
    s.stats.BytesReceived += bytes
    s.stats.PacketsReceived++
    
    if duration > 0 {
        s.stats.Bitrate = float64(bytes) * 8 / duration
    }
    
    s.stats.LastUpdate = now
}

// GetStats returns current stream statistics
func (s *Stream) GetStats() models.StreamStats {
    s.stats.RLock()
    defer s.stats.RUnlock()
    
    return models.StreamStats{
        BytesReceived:   s.stats.BytesReceived,
        PacketsReceived: s.stats.PacketsReceived,
        Bitrate:         s.stats.Bitrate,
        LastUpdate:      s.stats.LastUpdate,
    }
}

func (s *Stream) distributeOutput() {
    defer s.wg.Done()
    
    buffer := s.bufferPool.GetBuffer(64 * 1024) // 64KB chunks
    defer s.bufferPool.PutBuffer(buffer)
    
    for {
        select {
        case <-s.ctx.Done():
            return
        default:
            // Read from ring buffer
            n, err := s.buffer.Read(buffer)
            if err != nil {
                continue
            }
            
            // Copy data for distribution
            data := make([]byte, n)
            copy(data, buffer[:n])
            
            // Distribute to outputs
            s.outputMu.RLock()
            for id, ch := range s.outputs {
                select {
                case ch <- data:
                case <-time.After(100 * time.Millisecond):
                    log.Warn().
                        Str("stream_id", s.ID).
                        Str("output_id", id).
                        Msg("Output channel blocked")
                }
            }
            s.outputMu.RUnlock()
        }
    }
}
```

## Auto-Reconnection Logic

### internal/ingestion/reconnect.go
```go
package ingestion

import (
    "context"
    "sync"
    "time"

    "github.com/rs/zerolog/log"
)

// ReconnectManager handles stream auto-reconnection
type ReconnectManager struct {
    service        *Service
    reconnectDelay time.Duration
    maxAttempts    int
    
    // Track reconnection attempts
    attempts sync.Map // map[string]int
}

// NewReconnectManager creates a new reconnect manager
func NewReconnectManager(service *Service, delay time.Duration, maxAttempts int) *ReconnectManager {
    return &ReconnectManager{
        service:        service,
        reconnectDelay: delay,
        maxAttempts:    maxAttempts,
    }
}

// HandleDisconnect manages stream disconnection
func (rm *ReconnectManager) HandleDisconnect(streamID string) {
    stream, err := rm.service.GetStream(streamID)
    if err != nil {
        return
    }

    // Check if auto-reconnect is enabled
    if !stream.Metadata.AutoReconnect {
        log.Info().Str("stream_id", streamID).Msg("Auto-reconnect disabled for stream")
        rm.service.UnregisterStream(streamID)
        return
    }

    // Get current attempt count
    attemptI, _ := rm.attempts.LoadOrStore(streamID, 0)
    attempt := attemptI.(int)

    if attempt >= rm.maxAttempts {
        log.Error().
            Str("stream_id", streamID).
            Int("attempts", attempt).
            Msg("Max reconnection attempts exceeded")
        rm.service.UnregisterStream(streamID)
        rm.attempts.Delete(streamID)
        return
    }

    // Update attempt count
    attempt++
    rm.attempts.Store(streamID, attempt)

    // Set reconnecting status
    stream.SetStatus(models.StreamStatusReconnecting)

    log.Info().
        Str("stream_id", streamID).
        Int("attempt", attempt).
        Int("max_attempts", rm.maxAttempts).
        Msg("Attempting stream reconnection")

    // Wait for reconnection
    ctx, cancel := context.WithTimeout(context.Background(), rm.reconnectDelay)
    defer cancel()

    select {
    case <-ctx.Done():
        // Timeout - try again or give up
        rm.HandleDisconnect(streamID)
    case <-stream.ctx.Done():
        // Stream stopped
        rm.attempts.Delete(streamID)
    }
}

// HandleReconnect processes successful reconnection
func (rm *ReconnectManager) HandleReconnect(streamID string) {
    rm.attempts.Delete(streamID)
    
    log.Info().
        Str("stream_id", streamID).
        Msg("Stream reconnected successfully")
}
```

## Next Steps

Continue to:
- [Part 4: HLS Packaging and Delivery](plan-4.md) - LL-HLS implementation with CMAF
- [Part 5: Admin API and Monitoring](plan-5.md) - Control endpoints and dashboard
- [Part 6: Infrastructure and Deployment](plan-6.md) - AWS setup and configuration
- [Part 7: Development Setup and CI/CD](plan-7.md) - Local development and automation