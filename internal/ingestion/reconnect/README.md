# Reconnection Package

This package provides automatic reconnection capabilities for the Mirror video streaming platform. It handles network disconnections, protocol failures, and other transient issues with intelligent retry strategies.

## Overview

Network connections in video streaming are inherently unreliable. This package provides:

- **Automatic Reconnection**: Transparent handling of connection failures
- **Exponential Backoff**: Intelligent retry timing to avoid overwhelming servers
- **Connection Health Monitoring**: Proactive detection of connection issues
- **State Preservation**: Maintain stream state across reconnections
- **Configurable Strategies**: Multiple reconnection policies for different scenarios

## Architecture

```
┌─────────────────┐
│ Connection      │ ← Original Connection
│ Monitor         │
├─────────────────┤
│ • Health Checks │
│ • Failure       │
│   Detection     │
│ • State Tracking│
└─────────────────┘
         │
         ▼ (failure detected)
┌─────────────────┐
│ Reconnection    │
│ Manager         │
├─────────────────┤
│ • Strategy      │
│   Selection     │
│ • Backoff       │
│   Calculation   │
│ • State         │
│   Restoration   │
└─────────────────┘
         │
         ▼
┌─────────────────┐
│ New Connection  │ ← Reconnected
└─────────────────┘
```

## Connection Monitoring

### Health Checker
```go
type ConnectionHealth struct {
    IsConnected     bool          `json:"is_connected"`
    LastSeen        time.Time     `json:"last_seen"`
    PacketCount     uint64        `json:"packet_count"`
    ErrorCount      uint64        `json:"error_count"`
    Latency         time.Duration `json:"latency"`
    PacketLoss      float64       `json:"packet_loss"`
    ConnectionAge   time.Duration `json:"connection_age"`
}

type HealthChecker struct {
    connection     Connection
    config         HealthConfig
    lastPacketTime time.Time
    lastErrorTime  time.Time
    packetCount    uint64
    errorCount     uint64
    mu             sync.RWMutex
}

func (hc *HealthChecker) CheckHealth() ConnectionHealth {
    hc.mu.RLock()
    defer hc.mu.RUnlock()
    
    now := time.Now()
    
    // Check if connection is alive based on recent packet activity
    timeSinceLastPacket := now.Sub(hc.lastPacketTime)
    isConnected := timeSinceLastPacket < hc.config.Timeout
    
    // Calculate packet loss rate
    totalExpected := hc.calculateExpectedPackets()
    packetLoss := float64(totalExpected - hc.packetCount) / float64(totalExpected)
    if packetLoss < 0 {
        packetLoss = 0
    }
    
    return ConnectionHealth{
        IsConnected:   isConnected,
        LastSeen:      hc.lastPacketTime,
        PacketCount:   hc.packetCount,
        ErrorCount:    hc.errorCount,
        Latency:       hc.measureLatency(),
        PacketLoss:    packetLoss,
        ConnectionAge: now.Sub(hc.connection.GetStartTime()),
    }
}

func (hc *HealthChecker) OnPacketReceived() {
    hc.mu.Lock()
    defer hc.mu.Unlock()
    
    hc.lastPacketTime = time.Now()
    hc.packetCount++
}

func (hc *HealthChecker) OnError(err error) {
    hc.mu.Lock()
    defer hc.mu.Unlock()
    
    hc.lastErrorTime = time.Now()
    hc.errorCount++
}
```

### Failure Detection
```go
type FailureDetector struct {
    healthChecker  *HealthChecker
    thresholds     FailureThresholds
    callbacks      []FailureCallback
    lastCheck      time.Time
    checkInterval  time.Duration
}

type FailureThresholds struct {
    MaxPacketLoss    float64       `yaml:"max_packet_loss"`     // 0.05 = 5%
    MaxLatency       time.Duration `yaml:"max_latency"`         // 1s
    MaxErrorRate     float64       `yaml:"max_error_rate"`      // 0.1 = 10%
    TimeoutDuration  time.Duration `yaml:"timeout_duration"`    // 30s
    MinPacketRate    float64       `yaml:"min_packet_rate"`     // packets/sec
}

type FailureReason string

const (
    FailureTimeout      FailureReason = "timeout"
    FailurePacketLoss   FailureReason = "packet_loss"
    FailureHighLatency  FailureReason = "high_latency"
    FailureHighErrors   FailureReason = "high_error_rate"
    FailureLowPacketRate FailureReason = "low_packet_rate"
    FailureNetworkError FailureReason = "network_error"
)

func (fd *FailureDetector) CheckForFailure() *FailureEvent {
    health := fd.healthChecker.CheckHealth()
    
    // Check for timeout
    if !health.IsConnected {
        return &FailureEvent{
            Reason:    FailureTimeout,
            Timestamp: time.Now(),
            Health:    health,
            Severity:  SeverityHigh,
        }
    }
    
    // Check packet loss
    if health.PacketLoss > fd.thresholds.MaxPacketLoss {
        return &FailureEvent{
            Reason:    FailurePacketLoss,
            Timestamp: time.Now(),
            Health:    health,
            Severity:  SeverityMedium,
            Details:   map[string]interface{}{"packet_loss": health.PacketLoss},
        }
    }
    
    // Check latency
    if health.Latency > fd.thresholds.MaxLatency {
        return &FailureEvent{
            Reason:    FailureHighLatency,
            Timestamp: time.Now(),
            Health:    health,
            Severity:  SeverityMedium,
            Details:   map[string]interface{}{"latency": health.Latency},
        }
    }
    
    // Check error rate
    errorRate := fd.calculateRecentErrorRate()
    if errorRate > fd.thresholds.MaxErrorRate {
        return &FailureEvent{
            Reason:    FailureHighErrors,
            Timestamp: time.Now(),
            Health:    health,
            Severity:  SeverityHigh,
            Details:   map[string]interface{}{"error_rate": errorRate},
        }
    }
    
    return nil // No failure detected
}
```

## Reconnection Strategies

### Exponential Backoff
```go
type ExponentialBackoff struct {
    initialDelay time.Duration
    maxDelay     time.Duration
    multiplier   float64
    jitter       bool
    attempt      int
    mu           sync.Mutex
}

func (eb *ExponentialBackoff) NextDelay() time.Duration {
    eb.mu.Lock()
    defer eb.mu.Unlock()
    
    if eb.attempt == 0 {
        eb.attempt = 1
        return eb.initialDelay
    }
    
    // Calculate exponential delay
    delay := time.Duration(float64(eb.initialDelay) * math.Pow(eb.multiplier, float64(eb.attempt-1)))
    
    // Apply maximum delay limit
    if delay > eb.maxDelay {
        delay = eb.maxDelay
    }
    
    // Add jitter to prevent thundering herd
    if eb.jitter {
        jitterAmount := time.Duration(rand.Float64() * float64(delay) * 0.1) // 10% jitter
        delay += jitterAmount
    }
    
    eb.attempt++
    return delay
}

func (eb *ExponentialBackoff) Reset() {
    eb.mu.Lock()
    defer eb.mu.Unlock()
    
    eb.attempt = 0
}
```

### Linear Backoff
```go
type LinearBackoff struct {
    initialDelay time.Duration
    increment    time.Duration
    maxDelay     time.Duration
    attempt      int
    mu           sync.Mutex
}

func (lb *LinearBackoff) NextDelay() time.Duration {
    lb.mu.Lock()
    defer lb.mu.Unlock()
    
    delay := lb.initialDelay + time.Duration(lb.attempt) * lb.increment
    
    if delay > lb.maxDelay {
        delay = lb.maxDelay
    }
    
    lb.attempt++
    return delay
}
```

### Fixed Interval Backoff
```go
type FixedBackoff struct {
    interval time.Duration
}

func (fb *FixedBackoff) NextDelay() time.Duration {
    return fb.interval
}
```

## Reconnection Manager

### Manager Implementation
```go
type ReconnectionManager struct {
    streamID       string
    connection     Connection
    backoffStrategy BackoffStrategy
    maxRetries     int
    retryCount     int
    config         ReconnectionConfig
    
    // State management
    originalState  ConnectionState
    isReconnecting bool
    
    // Monitoring
    healthChecker    *HealthChecker
    failureDetector  *FailureDetector
    
    // Events
    eventChannel   chan ReconnectionEvent
    callbacks      []ReconnectionCallback
    
    mu sync.RWMutex
}

type ReconnectionEvent struct {
    Type      EventType     `json:"type"`
    StreamID  string        `json:"stream_id"`
    Timestamp time.Time     `json:"timestamp"`
    Attempt   int           `json:"attempt"`
    Error     error         `json:"error,omitempty"`
    Duration  time.Duration `json:"duration,omitempty"`
}

type EventType string

const (
    EventReconnectionStarted   EventType = "reconnection_started"
    EventReconnectionAttempt   EventType = "reconnection_attempt"
    EventReconnectionSucceeded EventType = "reconnection_succeeded"
    EventReconnectionFailed    EventType = "reconnection_failed"
    EventReconnectionAborted   EventType = "reconnection_aborted"
)

func (rm *ReconnectionManager) Start(ctx context.Context) error {
    // Start health monitoring
    go rm.monitorHealth(ctx)
    
    // Start failure detection
    go rm.detectFailures(ctx)
    
    return nil
}

func (rm *ReconnectionManager) detectFailures(ctx context.Context) {
    ticker := time.NewTicker(rm.config.CheckInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            if failure := rm.failureDetector.CheckForFailure(); failure != nil {
                rm.handleFailure(ctx, failure)
            }
            
        case <-ctx.Done():
            return
        }
    }
}

func (rm *ReconnectionManager) handleFailure(ctx context.Context, failure *FailureEvent) {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    
    if rm.isReconnecting {
        return // Already reconnecting
    }
    
    rm.isReconnecting = true
    rm.retryCount = 0
    
    // Save current state
    rm.originalState = rm.connection.GetState()
    
    // Start reconnection process
    go rm.performReconnection(ctx, failure)
}

func (rm *ReconnectionManager) performReconnection(ctx context.Context, failure *FailureEvent) {
    defer func() {
        rm.mu.Lock()
        rm.isReconnecting = false
        rm.mu.Unlock()
    }()
    
    // Notify reconnection started
    rm.publishEvent(ReconnectionEvent{
        Type:      EventReconnectionStarted,
        StreamID:  rm.streamID,
        Timestamp: time.Now(),
        Attempt:   0,
    })
    
    for rm.retryCount < rm.maxRetries {
        rm.retryCount++
        
        // Calculate backoff delay
        delay := rm.backoffStrategy.NextDelay()
        
        // Wait before attempting reconnection
        select {
        case <-time.After(delay):
        case <-ctx.Done():
            rm.publishEvent(ReconnectionEvent{
                Type:      EventReconnectionAborted,
                StreamID:  rm.streamID,
                Timestamp: time.Now(),
                Attempt:   rm.retryCount,
            })
            return
        }
        
        // Attempt reconnection
        startTime := time.Now()
        err := rm.attemptReconnection(ctx)
        duration := time.Since(startTime)
        
        rm.publishEvent(ReconnectionEvent{
            Type:      EventReconnectionAttempt,
            StreamID:  rm.streamID,
            Timestamp: time.Now(),
            Attempt:   rm.retryCount,
            Error:     err,
            Duration:  duration,
        })
        
        if err == nil {
            // Reconnection succeeded
            rm.backoffStrategy.Reset()
            rm.publishEvent(ReconnectionEvent{
                Type:      EventReconnectionSucceeded,
                StreamID:  rm.streamID,
                Timestamp: time.Now(),
                Attempt:   rm.retryCount,
                Duration:  duration,
            })
            return
        }
        
        // Log the error
        rm.config.Logger.WithError(err).
            WithField("attempt", rm.retryCount).
            WithField("delay", delay).
            Warn("Reconnection attempt failed")
    }
    
    // All attempts failed
    rm.publishEvent(ReconnectionEvent{
        Type:      EventReconnectionFailed,
        StreamID:  rm.streamID,
        Timestamp: time.Now(),
        Attempt:   rm.retryCount,
    })
}
```

### Connection Restoration
```go
func (rm *ReconnectionManager) attemptReconnection(ctx context.Context) error {
    // Close existing connection
    if err := rm.connection.Close(); err != nil {
        rm.config.Logger.WithError(err).Warn("Error closing existing connection")
    }
    
    // Create new connection based on original parameters
    newConnection, err := rm.createNewConnection(ctx)
    if err != nil {
        return fmt.Errorf("failed to create new connection: %w", err)
    }
    
    // Restore connection state
    if err := rm.restoreConnectionState(newConnection); err != nil {
        newConnection.Close()
        return fmt.Errorf("failed to restore connection state: %w", err)
    }
    
    // Verify connection health
    if err := rm.verifyConnectionHealth(newConnection); err != nil {
        newConnection.Close()
        return fmt.Errorf("connection health check failed: %w", err)
    }
    
    // Replace connection
    rm.connection = newConnection
    
    // Update health checker
    rm.healthChecker.SetConnection(newConnection)
    
    return nil
}

func (rm *ReconnectionManager) restoreConnectionState(conn Connection) error {
    // Restore stream parameters
    if err := conn.SetStreamID(rm.originalState.StreamID); err != nil {
        return err
    }
    
    // Restore codec settings
    if err := conn.SetCodec(rm.originalState.Codec); err != nil {
        return err
    }
    
    // Restore other state as needed
    return nil
}

func (rm *ReconnectionManager) verifyConnectionHealth(conn Connection) error {
    // Wait for initial packets
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    packetReceived := make(chan bool, 1)
    
    // Set up temporary packet handler
    conn.SetPacketHandler(func(packet []byte) {
        select {
        case packetReceived <- true:
        default:
        }
    })
    
    // Wait for packet or timeout
    select {
    case <-packetReceived:
        return nil
    case <-ctx.Done():
        return fmt.Errorf("no packets received within timeout")
    }
}
```

## State Preservation

### Connection State
```go
type ConnectionState struct {
    StreamID     string                 `json:"stream_id"`
    Protocol     string                 `json:"protocol"`
    Codec        string                 `json:"codec"`
    RemoteAddr   string                 `json:"remote_addr"`
    LocalAddr    string                 `json:"local_addr"`
    Parameters   map[string]interface{} `json:"parameters"`
    Statistics   ConnectionStatistics   `json:"statistics"`
    StartTime    time.Time              `json:"start_time"`
    LastActivity time.Time              `json:"last_activity"`
}

type ConnectionStatistics struct {
    PacketsReceived uint64        `json:"packets_received"`
    BytesReceived   uint64        `json:"bytes_received"`
    PacketErrors    uint64        `json:"packet_errors"`
    Uptime          time.Duration `json:"uptime"`
}

func (cs *ConnectionState) Save() ([]byte, error) {
    return json.Marshal(cs)
}

func (cs *ConnectionState) Load(data []byte) error {
    return json.Unmarshal(data, cs)
}
```

### Stream State Persistence
```go
type StatePersistence struct {
    storage StateStorage
    ttl     time.Duration
}

type StateStorage interface {
    Save(streamID string, state ConnectionState) error
    Load(streamID string) (ConnectionState, error)
    Delete(streamID string) error
}

// Redis-based state storage
type RedisStateStorage struct {
    client redis.Client
    prefix string
}

func (rss *RedisStateStorage) Save(streamID string, state ConnectionState) error {
    key := rss.prefix + ":" + streamID
    
    data, err := state.Save()
    if err != nil {
        return err
    }
    
    return rss.client.Set(context.Background(), key, data, rss.ttl).Err()
}

func (rss *RedisStateStorage) Load(streamID string) (ConnectionState, error) {
    key := rss.prefix + ":" + streamID
    
    data, err := rss.client.Get(context.Background(), key).Result()
    if err != nil {
        return ConnectionState{}, err
    }
    
    var state ConnectionState
    err = state.Load([]byte(data))
    return state, err
}
```

## Protocol-Specific Reconnection

### SRT Reconnection
```go
type SRTReconnector struct {
    config    SRTConfig
    connector SRTConnector
}

func (sr *SRTReconnector) Reconnect(ctx context.Context, state ConnectionState) (Connection, error) {
    // Parse SRT-specific parameters
    srtParams := sr.parseSRTParameters(state.Parameters)
    
    // Create new SRT connection
    conn, err := sr.connector.Connect(ctx, ConnectOptions{
        RemoteAddr: state.RemoteAddr,
        StreamID:   state.StreamID,
        Latency:    srtParams.Latency,
        Encryption: srtParams.Encryption,
        Passphrase: srtParams.Passphrase,
    })
    
    if err != nil {
        return nil, fmt.Errorf("SRT reconnection failed: %w", err)
    }
    
    return conn, nil
}
```

### RTP Reconnection
```go
type RTPReconnector struct {
    config    RTPConfig
    connector RTPConnector
}

func (rr *RTPReconnector) Reconnect(ctx context.Context, state ConnectionState) (Connection, error) {
    // Parse RTP-specific parameters
    rtpParams := rr.parseRTPParameters(state.Parameters)
    
    // Create new RTP session
    session, err := rr.connector.CreateSession(ctx, SessionOptions{
        LocalAddr:  state.LocalAddr,
        RemoteAddr: state.RemoteAddr,
        SSRC:       rtpParams.SSRC,
        PayloadType: rtpParams.PayloadType,
    })
    
    if err != nil {
        return nil, fmt.Errorf("RTP reconnection failed: %w", err)
    }
    
    return session, nil
}
```

## Integration Examples

### Stream Handler Integration
```go
type StreamHandler struct {
    reconnectionManager *ReconnectionManager
    // ... other fields
}

func (h *StreamHandler) Start(ctx context.Context) error {
    // Start reconnection manager
    if err := h.reconnectionManager.Start(ctx); err != nil {
        return err
    }
    
    // Subscribe to reconnection events
    h.reconnectionManager.OnEvent(func(event ReconnectionEvent) {
        switch event.Type {
        case EventReconnectionStarted:
            h.handleReconnectionStarted(event)
        case EventReconnectionSucceeded:
            h.handleReconnectionSucceeded(event)
        case EventReconnectionFailed:
            h.handleReconnectionFailed(event)
        }
    })
    
    return nil
}

func (h *StreamHandler) handleReconnectionSucceeded(event ReconnectionEvent) {
    h.logger.Info("Stream reconnected successfully",
        "stream_id", event.StreamID,
        "attempts", event.Attempt,
        "duration", event.Duration)
    
    // Resume stream processing
    h.resumeProcessing()
}
```

### Manager Integration
```go
func (m *Manager) createStreamHandler(streamID string, conn Connection) *StreamHandler {
    // Create reconnection manager for this stream
    reconnectionManager := reconnect.NewManager(reconnect.Config{
        StreamID:    streamID,
        Connection:  conn,
        MaxRetries:  5,
        BackoffStrategy: reconnect.NewExponentialBackoff(
            1*time.Second,  // initial delay
            30*time.Second, // max delay
            2.0,           // multiplier
            true,          // jitter
        ),
        CheckInterval: 5*time.Second,
    })
    
    handler := &StreamHandler{
        streamID:            streamID,
        connection:          conn,
        reconnectionManager: reconnectionManager,
    }
    
    return handler
}
```

## Monitoring and Metrics

### Reconnection Metrics
```go
type ReconnectionMetrics struct {
    ReconnectionAttempts *prometheus.CounterVec // by stream_id, reason
    ReconnectionSuccesses *prometheus.CounterVec // by stream_id
    ReconnectionDuration *prometheus.HistogramVec // by stream_id
    ConnectionUptime     *prometheus.GaugeVec   // by stream_id
    FailureDetection     *prometheus.CounterVec // by reason
}

func (rm *ReconnectionManager) recordMetrics(event ReconnectionEvent) {
    labels := prometheus.Labels{"stream_id": event.StreamID}
    
    switch event.Type {
    case EventReconnectionAttempt:
        rm.metrics.ReconnectionAttempts.With(labels).Inc()
        
    case EventReconnectionSucceeded:
        rm.metrics.ReconnectionSuccesses.With(labels).Inc()
        if event.Duration > 0 {
            rm.metrics.ReconnectionDuration.With(labels).Observe(event.Duration.Seconds())
        }
        
    case EventReconnectionFailed:
        // Update failure metrics
        break
    }
}
```

## Configuration

### Reconnection Configuration
```go
type ReconnectionConfig struct {
    // Basic settings
    MaxRetries    int           `yaml:"max_retries"`
    CheckInterval time.Duration `yaml:"check_interval"`
    
    // Backoff strategy
    BackoffType   string        `yaml:"backoff_type"` // "exponential", "linear", "fixed"
    InitialDelay  time.Duration `yaml:"initial_delay"`
    MaxDelay      time.Duration `yaml:"max_delay"`
    Multiplier    float64       `yaml:"multiplier"`
    UseJitter     bool          `yaml:"use_jitter"`
    
    // Health monitoring
    HealthThresholds FailureThresholds `yaml:"health_thresholds"`
    
    // State persistence
    PersistState  bool          `yaml:"persist_state"`
    StateTTL      time.Duration `yaml:"state_ttl"`
    
    // Protocol-specific
    SRT SRTReconnectionConfig `yaml:"srt"`
    RTP RTPReconnectionConfig `yaml:"rtp"`
}

type SRTReconnectionConfig struct {
    ConnectTimeout time.Duration `yaml:"connect_timeout"`
    RetryOnClose   bool          `yaml:"retry_on_close"`
}

type RTPReconnectionConfig struct {
    SessionTimeout time.Duration `yaml:"session_timeout"`
    RetryOnTimeout bool          `yaml:"retry_on_timeout"`
}
```

## Testing

### Reconnection Testing
```bash
# Test reconnection mechanisms
go test ./internal/ingestion/reconnect/...

# Test with network simulation
go test -tags=network_simulation ./internal/ingestion/reconnect/...

# Test backoff strategies
go test -run TestBackoff ./internal/ingestion/reconnect/...
```

### Network Simulation Testing
```go
// Network failure simulation for testing
type NetworkSimulator struct {
    failureRate    float64
    latencyRange   time.Duration
    packetLossRate float64
}

func (ns *NetworkSimulator) SimulateFailure() bool {
    return rand.Float64() < ns.failureRate
}

func (ns *NetworkSimulator) SimulateLatency() time.Duration {
    return time.Duration(rand.Float64() * float64(ns.latencyRange))
}

func TestReconnectionUnderNetworkFailure(t *testing.T) {
    simulator := &NetworkSimulator{
        failureRate:    0.1,  // 10% failure rate
        latencyRange:   100 * time.Millisecond,
        packetLossRate: 0.05, // 5% packet loss
    }
    
    // Test reconnection with simulated failures
    // ...
}
```

## Best Practices

1. **Monitor Connection Health**: Continuously monitor for early failure detection
2. **Use Appropriate Backoff**: Choose exponential backoff for most scenarios
3. **Preserve State**: Save connection state for seamless reconnection
4. **Limit Retries**: Prevent infinite reconnection attempts
5. **Protocol-Specific Logic**: Implement protocol-specific reconnection strategies
6. **Log Events**: Comprehensive logging for debugging reconnection issues
7. **Test Failure Scenarios**: Regularly test with simulated network failures
8. **Graceful Degradation**: Handle cases where reconnection is not possible
