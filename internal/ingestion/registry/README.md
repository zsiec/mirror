# Stream Registry Package

This package provides centralized stream management and registry functionality for the Mirror video streaming platform. It maintains authoritative state for all active streams with Redis-based persistence and distributed coordination.

## Overview

The stream registry serves as the single source of truth for stream state across the Mirror platform. It provides:

- **Stream Lifecycle Management**: Track streams from creation to termination
- **Distributed State**: Redis-based storage for multi-instance deployments
- **State Synchronization**: Consistent state across all platform components
- **Stream Discovery**: Query and filter active streams
- **Metadata Management**: Store and retrieve stream metadata and statistics

## Architecture

```
┌─────────────────┐
│ Registry API    │ ← HTTP/gRPC Interface
├─────────────────┤
│ • Create Stream │
│ • Update State  │
│ • Query Streams │
│ • Delete Stream │
└─────────────────┘
         │
         ▼
┌─────────────────┐
│ Stream Manager  │
├─────────────────┤
│ • Validation    │
│ • State Machine │
│ • Event Pub/Sub │
│ • TTL Management│
└─────────────────┘
         │
         ▼
┌─────────────────┐
│ Redis Backend   │ ← Persistence Layer
├─────────────────┤
│ • Stream Data   │
│ • Indexes       │
│ • Pub/Sub       │
│ • Expiration    │
└─────────────────┘
```

## Stream Model

### Stream Structure
```go
type Stream struct {
    // Identity
    ID          string    `json:"id" redis:"id"`
    Name        string    `json:"name,omitempty" redis:"name"`
    
    // Type and protocol
    Type        StreamType `json:"type" redis:"type"`
    Protocol    string     `json:"protocol" redis:"protocol"`
    Codec       string     `json:"codec,omitempty" redis:"codec"`
    
    // Network information
    SourceAddr  string     `json:"source_addr" redis:"source_addr"`
    SourcePort  int        `json:"source_port,omitempty" redis:"source_port"`
    
    // State management
    Status      StreamStatus `json:"status" redis:"status"`
    State       StreamState  `json:"state,omitempty" redis:"state"`
    
    // Timing
    CreatedAt   time.Time  `json:"created_at" redis:"created_at"`
    UpdatedAt   time.Time  `json:"updated_at" redis:"updated_at"`
    StartedAt   *time.Time `json:"started_at,omitempty" redis:"started_at"`
    EndedAt     *time.Time `json:"ended_at,omitempty" redis:"ended_at"`
    
    // Metadata
    Metadata    StreamMetadata `json:"metadata,omitempty" redis:"metadata"`
    Tags        []string       `json:"tags,omitempty" redis:"tags"`
    
    // Statistics (cached from stream handler)
    Statistics  *StreamStatistics `json:"statistics,omitempty" redis:"-"`
    
    // Configuration
    Config      StreamConfig `json:"config,omitempty" redis:"config"`
    
    // Expiration
    TTL         time.Duration `json:"ttl,omitempty" redis:"ttl"`
}

type StreamType string

const (
    StreamTypeSRT  StreamType = "srt"
    StreamTypeRTP  StreamType = "rtp"
    StreamTypeRTMP StreamType = "rtmp"  // Future
    StreamTypeWebRTC StreamType = "webrtc" // Future
)

type StreamStatus string

const (
    StatusPending     StreamStatus = "pending"
    StatusActive      StreamStatus = "active"
    StatusPaused      StreamStatus = "paused"
    StatusTerminating StreamStatus = "terminating"
    StatusTerminated  StreamStatus = "terminated"
    StatusError       StreamStatus = "error"
)
```

### Stream State Machine
```go
type StreamState struct {
    // Processing state
    CurrentState    ProcessingState `json:"current_state"`
    PreviousState   ProcessingState `json:"previous_state"`
    StateTransitions []StateTransition `json:"state_transitions"`
    
    // Error information
    LastError       *StreamError    `json:"last_error,omitempty"`
    ErrorCount      int             `json:"error_count"`
    
    // Performance data
    Health          HealthStatus    `json:"health"`
    Quality         QualityMetrics  `json:"quality"`
    
    // Resource usage
    Resources       ResourceUsage   `json:"resources"`
}

type ProcessingState string

const (
    StateInitializing ProcessingState = "initializing"
    StateConnecting   ProcessingState = "connecting"
    StateBuffering    ProcessingState = "buffering"
    StateStreaming    ProcessingState = "streaming"
    StateRecovering   ProcessingState = "recovering"
    StateDraining     ProcessingState = "draining"
    StateStopping     ProcessingState = "stopping"
)
```

## Redis Registry Implementation

### Stream Storage
```go
type RedisRegistry struct {
    client      redis.Client
    keyPrefix   string
    pubsub      *redis.PubSub
    ttlManager  *TTLManager
    indexManager *IndexManager
}

// Store stream in Redis with proper indexing
func (r *RedisRegistry) Create(ctx context.Context, stream *Stream) error {
    // Validate stream
    if err := r.validateStream(stream); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }
    
    // Set timestamps
    now := time.Now()
    stream.CreatedAt = now
    stream.UpdatedAt = now
    
    // Generate ID if not provided
    if stream.ID == "" {
        stream.ID = r.generateStreamID()
    }
    
    // Serialize stream data
    data, err := json.Marshal(stream)
    if err != nil {
        return fmt.Errorf("serialization failed: %w", err)
    }
    
    // Redis transaction for atomic create
    pipe := r.client.TxPipeline()
    
    // Store main stream data
    streamKey := r.getStreamKey(stream.ID)
    pipe.Set(ctx, streamKey, data, stream.TTL)
    
    // Update indexes
    r.indexManager.AddToIndexes(pipe, stream)
    
    // Publish creation event
    r.publishEvent(pipe, "stream.created", stream)
    
    // Execute transaction
    _, err = pipe.Exec(ctx)
    if err != nil {
        return fmt.Errorf("redis transaction failed: %w", err)
    }
    
    return nil
}

// Retrieve stream by ID
func (r *RedisRegistry) Get(ctx context.Context, streamID string) (*Stream, error) {
    streamKey := r.getStreamKey(streamID)
    
    data, err := r.client.Get(ctx, streamKey).Result()
    if err != nil {
        if err == redis.Nil {
            return nil, ErrStreamNotFound
        }
        return nil, fmt.Errorf("redis get failed: %w", err)
    }
    
    var stream Stream
    if err := json.Unmarshal([]byte(data), &stream); err != nil {
        return nil, fmt.Errorf("deserialization failed: %w", err)
    }
    
    return &stream, nil
}
```

### Stream Indexing
```go
type IndexManager struct {
    client redis.Client
    prefix string
}

// Maintain multiple indexes for efficient querying
func (im *IndexManager) AddToIndexes(pipe redis.Pipeliner, stream *Stream) {
    ctx := context.Background()
    
    // Status index: status:active -> [stream1, stream2, ...]
    statusKey := im.getStatusIndexKey(stream.Status)
    pipe.SAdd(ctx, statusKey, stream.ID)
    
    // Type index: type:srt -> [stream1, stream3, ...]
    typeKey := im.getTypeIndexKey(stream.Type)
    pipe.SAdd(ctx, typeKey, stream.ID)
    
    // Protocol index: protocol:srt -> [stream1, stream2, ...]
    protocolKey := im.getProtocolIndexKey(stream.Protocol)
    pipe.SAdd(ctx, protocolKey, stream.ID)
    
    // Source address index
    sourceKey := im.getSourceIndexKey(stream.SourceAddr)
    pipe.SAdd(ctx, sourceKey, stream.ID)
    
    // Tag indexes
    for _, tag := range stream.Tags {
        tagKey := im.getTagIndexKey(tag)
        pipe.SAdd(ctx, tagKey, stream.ID)
    }
    
    // Time-based index (sorted set by creation time)
    timeKey := im.getTimeIndexKey()
    pipe.ZAdd(ctx, timeKey, &redis.Z{
        Score:  float64(stream.CreatedAt.Unix()),
        Member: stream.ID,
    })
}

// Query streams by multiple criteria
func (im *IndexManager) Query(ctx context.Context, filter StreamFilter) ([]string, error) {
    var keys []string
    
    // Build list of index keys to intersect
    if filter.Status != "" {
        keys = append(keys, im.getStatusIndexKey(filter.Status))
    }
    if filter.Type != "" {
        keys = append(keys, im.getTypeIndexKey(filter.Type))
    }
    if filter.Protocol != "" {
        keys = append(keys, im.getProtocolIndexKey(filter.Protocol))
    }
    
    if len(keys) == 0 {
        return im.getAllStreamIDs(ctx)
    }
    
    if len(keys) == 1 {
        return im.client.SMembers(ctx, keys[0]).Result()
    }
    
    // Intersect multiple indexes
    tempKey := fmt.Sprintf("temp:query:%d", time.Now().UnixNano())
    defer im.client.Del(ctx, tempKey)
    
    pipe := im.client.Pipeline()
    pipe.SInterStore(ctx, tempKey, keys...)
    pipe.SMembers(ctx, tempKey)
    
    results, err := pipe.Exec(ctx)
    if err != nil {
        return nil, err
    }
    
    return results[1].(*redis.StringSliceCmd).Result()
}
```

## Stream Query Interface

### Query Filters
```go
type StreamFilter struct {
    // Basic filters
    Status      StreamStatus `json:"status,omitempty"`
    Type        StreamType   `json:"type,omitempty"`
    Protocol    string       `json:"protocol,omitempty"`
    
    // Source filters
    SourceAddr  string       `json:"source_addr,omitempty"`
    SourcePort  int          `json:"source_port,omitempty"`
    
    // Time filters
    CreatedAfter  *time.Time `json:"created_after,omitempty"`
    CreatedBefore *time.Time `json:"created_before,omitempty"`
    
    // Tag filters
    Tags          []string   `json:"tags,omitempty"`
    TagsAny       bool       `json:"tags_any,omitempty"` // OR vs AND
    
    // State filters
    Health        *HealthStatus `json:"health,omitempty"`
    
    // Pagination
    Limit         int        `json:"limit,omitempty"`
    Offset        int        `json:"offset,omitempty"`
    SortBy        string     `json:"sort_by,omitempty"`
    SortOrder     string     `json:"sort_order,omitempty"`
}

// Query interface
type RegistryQuery interface {
    Filter(filter StreamFilter) RegistryQuery
    Limit(limit int) RegistryQuery
    Offset(offset int) RegistryQuery
    SortBy(field string, order SortOrder) RegistryQuery
    Execute(ctx context.Context) ([]*Stream, error)
    Count(ctx context.Context) (int, error)
}

// Example usage
func (r *RedisRegistry) Query() RegistryQuery {
    return &redisQuery{
        registry: r,
        filter:   StreamFilter{},
    }
}

// Find active SRT streams
activeStreams, err := registry.Query().
    Filter(StreamFilter{
        Status: StatusActive,
        Type:   StreamTypeSRT,
    }).
    SortBy("created_at", SortDesc).
    Limit(10).
    Execute(ctx)
```

### Advanced Queries
```go
// Complex query builder
type QueryBuilder struct {
    registry *RedisRegistry
    filters  []QueryFilter
    joins    []QueryJoin
    aggregations []QueryAggregation
}

type QueryFilter struct {
    Field    string      `json:"field"`
    Operator string      `json:"operator"` // eq, ne, gt, lt, in, like
    Value    interface{} `json:"value"`
}

// Example: Find streams with high error rates
highErrorStreams, err := registry.QueryBuilder().
    Where("error_count", "gt", 10).
    Where("status", "eq", StatusActive).
    Where("created_at", "gt", time.Now().Add(-1*time.Hour)).
    Execute(ctx)

// Aggregation queries
stats, err := registry.QueryBuilder().
    GroupBy("type").
    Aggregate("count", "*").
    Aggregate("avg", "error_count").
    Execute(ctx)
```

## Event System

### Stream Events
```go
type StreamEvent struct {
    Type      EventType     `json:"type"`
    StreamID  string        `json:"stream_id"`
    Timestamp time.Time     `json:"timestamp"`
    Data      interface{}   `json:"data"`
    Source    string        `json:"source"`
    Version   int           `json:"version"`
}

type EventType string

const (
    EventStreamCreated    EventType = "stream.created"
    EventStreamUpdated    EventType = "stream.updated"
    EventStreamStarted    EventType = "stream.started"
    EventStreamPaused     EventType = "stream.paused"
    EventStreamResumed    EventType = "stream.resumed"
    EventStreamTerminated EventType = "stream.terminated"
    EventStreamError      EventType = "stream.error"
    EventStreamHealthChange EventType = "stream.health.changed"
)

// Event publisher
func (r *RedisRegistry) publishEvent(pipe redis.Pipeliner, eventType EventType, stream *Stream) {
    event := StreamEvent{
        Type:      eventType,
        StreamID:  stream.ID,
        Timestamp: time.Now(),
        Data:      stream,
        Source:    r.instanceID,
        Version:   1,
    }
    
    eventData, _ := json.Marshal(event)
    
    // Publish to global channel
    pipe.Publish(context.Background(), "streams:events", eventData)
    
    // Publish to stream-specific channel
    streamChannel := fmt.Sprintf("stream:%s:events", stream.ID)
    pipe.Publish(context.Background(), streamChannel, eventData)
}

// Event subscription
func (r *RedisRegistry) Subscribe(ctx context.Context, eventTypes []EventType) (<-chan StreamEvent, error) {
    pubsub := r.client.Subscribe(ctx, "streams:events")
    
    eventChan := make(chan StreamEvent, 100)
    
    go func() {
        defer close(eventChan)
        defer pubsub.Close()
        
        for {
            select {
            case msg := <-pubsub.Channel():
                var event StreamEvent
                if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
                    continue
                }
                
                // Filter by event type
                if len(eventTypes) > 0 && !contains(eventTypes, event.Type) {
                    continue
                }
                
                select {
                case eventChan <- event:
                case <-ctx.Done():
                    return
                }
                
            case <-ctx.Done():
                return
            }
        }
    }()
    
    return eventChan, nil
}
```

## TTL Management

### Automatic Cleanup
```go
type TTLManager struct {
    registry     *RedisRegistry
    cleanupInterval time.Duration
    defaultTTL      time.Duration
}

func (tm *TTLManager) Start(ctx context.Context) {
    ticker := time.NewTicker(tm.cleanupInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            tm.performCleanup(ctx)
            
        case <-ctx.Done():
            return
        }
    }
}

func (tm *TTLManager) performCleanup(ctx context.Context) {
    // Find expired streams
    expiredStreams := tm.findExpiredStreams(ctx)
    
    for _, streamID := range expiredStreams {
        tm.cleanupExpiredStream(ctx, streamID)
    }
    
    // Update TTL for active streams
    tm.refreshActiveTTLs(ctx)
}

// Stream-specific TTL
func (r *RedisRegistry) ExtendTTL(ctx context.Context, streamID string, duration time.Duration) error {
    streamKey := r.getStreamKey(streamID)
    
    return r.client.Expire(ctx, streamKey, duration).Err()
}

// Heartbeat mechanism
func (r *RedisRegistry) Heartbeat(ctx context.Context, streamID string) error {
    // Update last seen timestamp
    updateData := map[string]interface{}{
        "last_heartbeat": time.Now(),
        "updated_at":     time.Now(),
    }
    
    streamKey := r.getStreamKey(streamID)
    
    pipe := r.client.Pipeline()
    pipe.HMSet(ctx, streamKey, updateData)
    pipe.Expire(ctx, streamKey, r.ttlManager.defaultTTL)
    
    _, err := pipe.Exec(ctx)
    return err
}
```

## Distributed Coordination

### Leader Election
```go
type LeaderElection struct {
    client     redis.Client
    leaderKey  string
    instanceID string
    leaseTTL   time.Duration
    isLeader   bool
    mu         sync.RWMutex
}

func (le *LeaderElection) Campaign(ctx context.Context) error {
    ticker := time.NewTicker(le.leaseTTL / 3)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            acquired, err := le.tryAcquireLease(ctx)
            if err != nil {
                return err
            }
            
            le.mu.Lock()
            le.isLeader = acquired
            le.mu.Unlock()
            
        case <-ctx.Done():
            le.releaseLease(ctx)
            return ctx.Err()
        }
    }
}

func (le *LeaderElection) tryAcquireLease(ctx context.Context) (bool, error) {
    result, err := le.client.SetNX(ctx, le.leaderKey, le.instanceID, le.leaseTTL).Result()
    if err != nil {
        return false, err
    }
    
    if result {
        return true, nil
    }
    
    // Check if we already hold the lease
    current, err := le.client.Get(ctx, le.leaderKey).Result()
    if err != nil && err != redis.Nil {
        return false, err
    }
    
    return current == le.instanceID, nil
}

// Leader-only operations
func (r *RedisRegistry) performLeaderOnlyTasks(ctx context.Context) {
    if !r.leaderElection.IsLeader() {
        return
    }
    
    // Cleanup orphaned streams
    r.cleanupOrphanedStreams(ctx)
    
    // Update global statistics
    r.updateGlobalStats(ctx)
    
    // Perform maintenance tasks
    r.performMaintenance(ctx)
}
```

## Integration Examples

### Stream Handler Integration
```go
// Register stream when handler starts
func (h *StreamHandler) Start() error {
    stream := &registry.Stream{
        ID:         h.streamID,
        Type:       registry.StreamTypeSRT,
        Protocol:   "srt",
        Status:     registry.StatusActive,
        SourceAddr: h.conn.RemoteAddr().String(),
        Metadata: registry.StreamMetadata{
            "codec":      h.codec.String(),
            "resolution": h.resolution.String(),
            "bitrate":    h.bitrate,
        },
        TTL: 5 * time.Minute,
    }
    
    return h.registry.Create(h.ctx, stream)
}

// Update stream status
func (h *StreamHandler) updateStatus(status registry.StreamStatus) {
    stream, err := h.registry.Get(h.ctx, h.streamID)
    if err != nil {
        return
    }
    
    stream.Status = status
    stream.UpdatedAt = time.Now()
    
    h.registry.Update(h.ctx, stream)
}

// Heartbeat mechanism
func (h *StreamHandler) sendHeartbeat() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            h.registry.Heartbeat(h.ctx, h.streamID)
            
        case <-h.ctx.Done():
            return
        }
    }
}
```

### API Integration
```go
// List streams endpoint
func (a *API) handleListStreams(w http.ResponseWriter, r *http.Request) {
    filter := registry.StreamFilter{
        Status: registry.StreamStatus(r.URL.Query().Get("status")),
        Type:   registry.StreamType(r.URL.Query().Get("type")),
    }
    
    streams, err := a.registry.Query().
        Filter(filter).
        Limit(50).
        Execute(r.Context())
    
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(streams)
}
```

## Monitoring and Metrics

### Registry Metrics
```go
type RegistryMetrics struct {
    StreamCount      *prometheus.GaugeVec   // by status, type
    OperationDuration *prometheus.HistogramVec // by operation
    OperationErrors  *prometheus.CounterVec  // by operation, error_type
    EventsPublished  *prometheus.CounterVec  // by event_type
    RedisConnections prometheus.Gauge
    IndexSize       *prometheus.GaugeVec    // by index_type
}

func (r *RedisRegistry) updateMetrics() {
    // Count streams by status
    for _, status := range []registry.StreamStatus{
        registry.StatusActive,
        registry.StatusPaused,
        registry.StatusError,
    } {
        count := r.countStreamsByStatus(status)
        r.metrics.StreamCount.WithLabelValues(string(status)).Set(float64(count))
    }
    
    // Redis connection pool stats
    poolStats := r.client.PoolStats()
    r.metrics.RedisConnections.Set(float64(poolStats.TotalConns))
}
```

## Configuration

### Registry Configuration
```go
type RegistryConfig struct {
    // Redis configuration
    Redis      RedisConfig      `yaml:"redis"`
    
    // TTL settings
    DefaultTTL     time.Duration `yaml:"default_ttl"`
    CleanupInterval time.Duration `yaml:"cleanup_interval"`
    
    // Event system
    EventBuffer    int           `yaml:"event_buffer"`
    EventRetention time.Duration `yaml:"event_retention"`
    
    // Leader election
    LeaderElection LeaderElectionConfig `yaml:"leader_election"`
    
    // Performance
    IndexCacheSize int           `yaml:"index_cache_size"`
    QueryTimeout   time.Duration `yaml:"query_timeout"`
    BatchSize      int           `yaml:"batch_size"`
}

type RedisConfig struct {
    Addresses    []string      `yaml:"addresses"`
    Password     string        `yaml:"password"`
    DB           int           `yaml:"db"`
    PoolSize     int           `yaml:"pool_size"`
    DialTimeout  time.Duration `yaml:"dial_timeout"`
    ReadTimeout  time.Duration `yaml:"read_timeout"`
    WriteTimeout time.Duration `yaml:"write_timeout"`
}
```

## Testing

### Registry Testing
```bash
# Test registry operations
go test ./internal/ingestion/registry/...

# Test with Redis
go test -tags=redis ./internal/ingestion/registry/...

# Test distributed scenarios
go test -run TestDistributed ./internal/ingestion/registry/...
```

### Mock Registry
```go
// Mock for testing
type MockRegistry struct {
    streams map[string]*Stream
    events  []StreamEvent
    mu      sync.RWMutex
}

func (m *MockRegistry) Create(ctx context.Context, stream *Stream) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    if _, exists := m.streams[stream.ID]; exists {
        return ErrStreamExists
    }
    
    m.streams[stream.ID] = stream
    return nil
}
```

## Best Practices

1. **Use Transactions**: Ensure atomic operations for complex updates
2. **Implement Heartbeats**: Detect and clean up dead streams
3. **Index Strategically**: Balance query performance with storage overhead
4. **Handle Redis Failures**: Implement fallback mechanisms
5. **Monitor Performance**: Track query times and Redis metrics
6. **Version Events**: Support event schema evolution
7. **Implement Leader Election**: Coordinate distributed cleanup tasks
8. **Use TTL Wisely**: Prevent memory leaks with appropriate timeouts
