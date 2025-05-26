# Rate Limiting Package

This package provides comprehensive rate limiting functionality for the Mirror video streaming platform. It implements various rate limiting algorithms to protect system resources and ensure fair usage across streams and clients.

## Overview

Rate limiting is critical for maintaining system stability under load. This package provides:

- **Multiple Algorithms**: Token bucket, sliding window, fixed window, leaky bucket
- **Multi-Level Limiting**: Global, per-stream, per-client, per-IP rate limits
- **Resource-Aware**: CPU, memory, bandwidth-based limiting
- **Dynamic Adjustment**: Adaptive limits based on system performance
- **Distributed Support**: Redis-based limits for multi-instance deployments

## Rate Limiting Algorithms

### Token Bucket Algorithm
```go
type TokenBucket struct {
    capacity    int64         // Maximum tokens
    tokens      int64         // Current tokens
    refillRate  int64         // Tokens per second
    lastRefill  time.Time     // Last refill time
    mu          sync.Mutex
}

func (tb *TokenBucket) Allow(tokens int64) bool {
    tb.mu.Lock()
    defer tb.mu.Unlock()
    
    // Refill tokens based on elapsed time
    now := time.Now()
    elapsed := now.Sub(tb.lastRefill)
    
    tokensToAdd := int64(elapsed.Seconds() * float64(tb.refillRate))
    tb.tokens = min(tb.capacity, tb.tokens + tokensToAdd)
    tb.lastRefill = now
    
    // Check if we have enough tokens
    if tb.tokens >= tokens {
        tb.tokens -= tokens
        return true
    }
    
    return false
}

// Rate limit based on bytes for bandwidth limiting
func (tb *TokenBucket) AllowBytes(bytes int64) bool {
    return tb.Allow(bytes)
}

// Rate limit based on requests for connection limiting
func (tb *TokenBucket) AllowRequest() bool {
    return tb.Allow(1)
}
```

### Sliding Window Algorithm
```go
type SlidingWindow struct {
    windowSize  time.Duration
    maxRequests int64
    requests    []time.Time
    mu          sync.Mutex
}

func (sw *SlidingWindow) Allow() bool {
    sw.mu.Lock()
    defer sw.mu.Unlock()
    
    now := time.Now()
    cutoff := now.Add(-sw.windowSize)
    
    // Remove old requests outside the window
    validRequests := 0
    for i, reqTime := range sw.requests {
        if reqTime.After(cutoff) {
            sw.requests = sw.requests[i:]
            validRequests = len(sw.requests)
            break
        }
    }
    
    if validRequests == 0 {
        sw.requests = sw.requests[:0]
    }
    
    // Check if we can accept another request
    if int64(len(sw.requests)) < sw.maxRequests {
        sw.requests = append(sw.requests, now)
        return true
    }
    
    return false
}
```

### Leaky Bucket Algorithm
```go
type LeakyBucket struct {
    capacity    int64
    leakRate    float64       // items per second
    volume      float64       // current volume
    lastLeak    time.Time
    mu          sync.Mutex
}

func (lb *LeakyBucket) Allow(items int64) bool {
    lb.mu.Lock()
    defer lb.mu.Unlock()
    
    // Leak items based on elapsed time
    now := time.Now()
    elapsed := now.Sub(lb.lastLeak).Seconds()
    
    leak := elapsed * lb.leakRate
    lb.volume = math.Max(0, lb.volume - leak)
    lb.lastLeak = now
    
    // Check if we can add more items
    if lb.volume + float64(items) <= float64(lb.capacity) {
        lb.volume += float64(items)
        return true
    }
    
    return false
}
```

## Multi-Level Rate Limiting

### Global Rate Limiter
```go
type GlobalRateLimiter struct {
    // Connection limits
    maxConnections    int64
    currentConnections int64
    connectionLimiter *TokenBucket
    
    // Bandwidth limits
    maxBandwidth      int64  // bytes per second
    bandwidthLimiter  *TokenBucket
    
    // Request limits
    maxRequestsPerSec int64
    requestLimiter    *SlidingWindow
    
    // Resource-based limits
    cpuLimiter        *ResourceLimiter
    memoryLimiter     *ResourceLimiter
    
    mu sync.RWMutex
}

func (grl *GlobalRateLimiter) AllowNewConnection() bool {
    grl.mu.Lock()
    defer grl.mu.Unlock()
    
    // Check connection limit
    if grl.currentConnections >= grl.maxConnections {
        return false
    }
    
    // Check token bucket for connection rate
    if !grl.connectionLimiter.AllowRequest() {
        return false
    }
    
    // Check system resources
    if !grl.cpuLimiter.Allow() || !grl.memoryLimiter.Allow() {
        return false
    }
    
    grl.currentConnections++
    return true
}

func (grl *GlobalRateLimiter) AllowBandwidth(bytes int64) bool {
    return grl.bandwidthLimiter.AllowBytes(bytes)
}

func (grl *GlobalRateLimiter) ReleaseConnection() {
    grl.mu.Lock()
    defer grl.mu.Unlock()
    
    if grl.currentConnections > 0 {
        grl.currentConnections--
    }
}
```

### Per-Stream Rate Limiter
```go
type StreamRateLimiter struct {
    streamID    string
    
    // Stream-specific limits
    maxBitrate     int64  // bits per second
    bitrateWindow  time.Duration
    bitrateHistory []BitratePoint
    
    // Frame rate limiting
    maxFPS         float64
    frameInterval  time.Duration
    lastFrame      time.Time
    
    // Packet rate limiting
    maxPacketsPerSec int64
    packetLimiter    *TokenBucket
    
    mu sync.Mutex
}

type BitratePoint struct {
    Timestamp time.Time
    Bytes     int64
}

func (srl *StreamRateLimiter) AllowFrame() bool {
    srl.mu.Lock()
    defer srl.mu.Unlock()
    
    now := time.Now()
    
    // Check frame rate limit
    if now.Sub(srl.lastFrame) < srl.frameInterval {
        return false
    }
    
    srl.lastFrame = now
    return true
}

func (srl *StreamRateLimiter) AllowBitrate(bytes int64) bool {
    srl.mu.Lock()
    defer srl.mu.Unlock()
    
    now := time.Now()
    cutoff := now.Add(-srl.bitrateWindow)
    
    // Remove old entries
    validEntries := 0
    for i, point := range srl.bitrateHistory {
        if point.Timestamp.After(cutoff) {
            srl.bitrateHistory = srl.bitrateHistory[i:]
            validEntries = len(srl.bitrateHistory)
            break
        }
    }
    
    if validEntries == 0 {
        srl.bitrateHistory = srl.bitrateHistory[:0]
    }
    
    // Calculate current bitrate
    var totalBytes int64
    for _, point := range srl.bitrateHistory {
        totalBytes += point.Bytes
    }
    
    currentBitrate := totalBytes * 8 / int64(srl.bitrateWindow.Seconds()) // bits per second
    
    // Check if adding these bytes would exceed limit
    newBitrate := (totalBytes + bytes) * 8 / int64(srl.bitrateWindow.Seconds())
    if newBitrate > srl.maxBitrate {
        return false
    }
    
    // Add new entry
    srl.bitrateHistory = append(srl.bitrateHistory, BitratePoint{
        Timestamp: now,
        Bytes:     bytes,
    })
    
    return true
}
```

### Per-Client Rate Limiter
```go
type ClientRateLimiter struct {
    clients map[string]*ClientLimits
    mu      sync.RWMutex
}

type ClientLimits struct {
    ClientID       string
    MaxStreams     int
    CurrentStreams int
    MaxBandwidth   int64
    RequestLimiter *SlidingWindow
    LastActivity   time.Time
}

func (crl *ClientRateLimiter) AllowClientStream(clientID string) bool {
    crl.mu.Lock()
    defer crl.mu.Unlock()
    
    limits, exists := crl.clients[clientID]
    if !exists {
        limits = &ClientLimits{
            ClientID:       clientID,
            MaxStreams:     crl.getDefaultMaxStreams(clientID),
            MaxBandwidth:   crl.getDefaultMaxBandwidth(clientID),
            RequestLimiter: NewSlidingWindow(time.Minute, 100),
            LastActivity:   time.Now(),
        }
        crl.clients[clientID] = limits
    }
    
    // Check stream limit
    if limits.CurrentStreams >= limits.MaxStreams {
        return false
    }
    
    // Check request rate limit
    if !limits.RequestLimiter.Allow() {
        return false
    }
    
    limits.CurrentStreams++
    limits.LastActivity = time.Now()
    return true
}

func (crl *ClientRateLimiter) ReleaseClientStream(clientID string) {
    crl.mu.Lock()
    defer crl.mu.Unlock()
    
    if limits, exists := crl.clients[clientID]; exists {
        if limits.CurrentStreams > 0 {
            limits.CurrentStreams--
        }
        limits.LastActivity = time.Now()
    }
}
```

## Resource-Aware Rate Limiting

### CPU-Based Rate Limiting
```go
type ResourceLimiter struct {
    threshold    float64  // 0.0 to 1.0
    windowSize   time.Duration
    samples      []float64
    sampleIndex  int
    sampleCount  int
    mu           sync.Mutex
}

func (rl *ResourceLimiter) Allow() bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    // Get current CPU usage
    cpuUsage := rl.getCurrentCPUUsage()
    
    // Add sample to sliding window
    if rl.sampleCount < len(rl.samples) {
        rl.samples[rl.sampleCount] = cpuUsage
        rl.sampleCount++
    } else {
        rl.samples[rl.sampleIndex] = cpuUsage
        rl.sampleIndex = (rl.sampleIndex + 1) % len(rl.samples)
    }
    
    // Calculate average CPU usage
    var sum float64
    count := rl.sampleCount
    for i := 0; i < count; i++ {
        sum += rl.samples[i]
    }
    
    avgUsage := sum / float64(count)
    
    // Allow request if CPU usage is below threshold
    return avgUsage < rl.threshold
}

func (rl *ResourceLimiter) getCurrentCPUUsage() float64 {
    // Implementation depends on platform
    // This could use /proc/stat on Linux, or other platform-specific methods
    return getCPUUsage()
}
```

### Memory-Based Rate Limiting
```go
type MemoryLimiter struct {
    maxMemory     int64
    checkInterval time.Duration
    lastCheck     time.Time
    mu            sync.Mutex
}

func (ml *MemoryLimiter) Allow() bool {
    ml.mu.Lock()
    defer ml.mu.Unlock()
    
    now := time.Now()
    
    // Check memory usage periodically
    if now.Sub(ml.lastCheck) >= ml.checkInterval {
        ml.lastCheck = now
        
        var memStats runtime.MemStats
        runtime.ReadMemStats(&memStats)
        
        if int64(memStats.HeapInuse) > ml.maxMemory {
            return false
        }
    }
    
    return true
}
```

## Distributed Rate Limiting

### Redis-Based Rate Limiter
```go
type RedisRateLimiter struct {
    client   redis.Client
    keyPrefix string
    algorithm string // "token_bucket", "sliding_window", "fixed_window"
}

// Distributed token bucket using Redis
func (rrl *RedisRateLimiter) AllowTokenBucket(ctx context.Context, key string, capacity, refillRate int64, window time.Duration) (bool, error) {
    script := `
        local key = KEYS[1]
        local capacity = tonumber(ARGV[1])
        local refillRate = tonumber(ARGV[2])
        local window = tonumber(ARGV[3])
        local tokens = tonumber(ARGV[4])
        local now = tonumber(ARGV[5])
        
        local bucket = redis.call('HMGET', key, 'tokens', 'lastRefill')
        local currentTokens = tonumber(bucket[1]) or capacity
        local lastRefill = tonumber(bucket[2]) or now
        
        -- Calculate tokens to add based on elapsed time
        local elapsed = (now - lastRefill) / 1000 -- convert to seconds
        local tokensToAdd = elapsed * refillRate
        currentTokens = math.min(capacity, currentTokens + tokensToAdd)
        
        -- Check if we have enough tokens
        if currentTokens >= tokens then
            currentTokens = currentTokens - tokens
            
            -- Update bucket state
            redis.call('HMSET', key, 'tokens', currentTokens, 'lastRefill', now)
            redis.call('EXPIRE', key, window)
            
            return 1 -- allowed
        else
            -- Update last refill time even if request is denied
            redis.call('HMSET', key, 'tokens', currentTokens, 'lastRefill', now)
            redis.call('EXPIRE', key, window)
            
            return 0 -- denied
        end
    `
    
    bucketKey := rrl.keyPrefix + ":" + key
    now := time.Now().UnixMilli()
    
    result, err := rrl.client.Eval(ctx, script, []string{bucketKey}, capacity, refillRate, int64(window.Seconds()), 1, now).Result()
    if err != nil {
        return false, err
    }
    
    return result.(int64) == 1, nil
}

// Distributed sliding window using Redis
func (rrl *RedisRateLimiter) AllowSlidingWindow(ctx context.Context, key string, maxRequests int64, window time.Duration) (bool, error) {
    script := `
        local key = KEYS[1]
        local maxRequests = tonumber(ARGV[1])
        local window = tonumber(ARGV[2])
        local now = tonumber(ARGV[3])
        
        -- Remove expired entries
        local cutoff = now - window * 1000
        redis.call('ZREMRANGEBYSCORE', key, 0, cutoff)
        
        -- Count current requests in window
        local current = redis.call('ZCARD', key)
        
        if current < maxRequests then
            -- Add current request
            redis.call('ZADD', key, now, now)
            redis.call('EXPIRE', key, window)
            return 1 -- allowed
        else
            return 0 -- denied
        end
    `
    
    windowKey := rrl.keyPrefix + ":window:" + key
    now := time.Now().UnixMilli()
    
    result, err := rrl.client.Eval(ctx, script, []string{windowKey}, maxRequests, int64(window.Seconds()), now).Result()
    if err != nil {
        return false, err
    }
    
    return result.(int64) == 1, nil
}
```

## Adaptive Rate Limiting

### Dynamic Adjustment
```go
type AdaptiveRateLimiter struct {
    baseLimiter    RateLimiter
    performanceMonitor *PerformanceMonitor
    adjustmentFactor   float64
    minLimit           int64
    maxLimit           int64
    adjustmentInterval time.Duration
    lastAdjustment     time.Time
    mu                 sync.Mutex
}

func (arl *AdaptiveRateLimiter) Allow() bool {
    arl.maybeAdjustLimits()
    return arl.baseLimiter.Allow()
}

func (arl *AdaptiveRateLimiter) maybeAdjustLimits() {
    arl.mu.Lock()
    defer arl.mu.Unlock()
    
    now := time.Now()
    if now.Sub(arl.lastAdjustment) < arl.adjustmentInterval {
        return
    }
    
    arl.lastAdjustment = now
    
    // Get current performance metrics
    metrics := arl.performanceMonitor.GetMetrics()
    
    // Adjust limits based on system performance
    if metrics.ErrorRate > 0.05 { // 5% error rate
        // Decrease limits
        arl.decreaseLimits(0.9)
    } else if metrics.ResponseTime > 500*time.Millisecond {
        // Decrease limits for high latency
        arl.decreaseLimits(0.95)
    } else if metrics.CPUUsage > 0.8 {
        // Decrease limits for high CPU
        arl.decreaseLimits(0.9)
    } else if metrics.AllPerformingWell() {
        // Increase limits gradually
        arl.increaseLimits(1.05)
    }
}

func (arl *AdaptiveRateLimiter) decreaseLimits(factor float64) {
    currentLimit := arl.baseLimiter.GetLimit()
    newLimit := int64(float64(currentLimit) * factor)
    
    if newLimit < arl.minLimit {
        newLimit = arl.minLimit
    }
    
    arl.baseLimiter.SetLimit(newLimit)
}

func (arl *AdaptiveRateLimiter) increaseLimits(factor float64) {
    currentLimit := arl.baseLimiter.GetLimit()
    newLimit := int64(float64(currentLimit) * factor)
    
    if newLimit > arl.maxLimit {
        newLimit = arl.maxLimit
    }
    
    arl.baseLimiter.SetLimit(newLimit)
}
```

## Rate Limiter Manager

### Centralized Management
```go
type RateLimiterManager struct {
    globalLimiter  *GlobalRateLimiter
    streamLimiters map[string]*StreamRateLimiter
    clientLimiters map[string]*ClientLimits
    ipLimiters     map[string]*IPLimiter
    
    // Distributed limiters
    redisLimiter   *RedisRateLimiter
    
    // Configuration
    config         RateLimitConfig
    
    mu sync.RWMutex
}

func (rlm *RateLimiterManager) AllowRequest(req *Request) (*Decision, error) {
    decision := &Decision{
        Allowed: true,
        Reason:  "",
        RetryAfter: 0,
    }
    
    // Global limits
    if !rlm.globalLimiter.AllowRequest() {
        decision.Allowed = false
        decision.Reason = "global_rate_limit_exceeded"
        return decision, nil
    }
    
    // IP-based limits
    if !rlm.allowIP(req.RemoteAddr) {
        decision.Allowed = false
        decision.Reason = "ip_rate_limit_exceeded"
        return decision, nil
    }
    
    // Client-based limits
    if req.ClientID != "" && !rlm.allowClient(req.ClientID) {
        decision.Allowed = false
        decision.Reason = "client_rate_limit_exceeded"
        return decision, nil
    }
    
    // Stream-based limits
    if req.StreamID != "" && !rlm.allowStream(req.StreamID, req) {
        decision.Allowed = false
        decision.Reason = "stream_rate_limit_exceeded"
        return decision, nil
    }
    
    return decision, nil
}

type Decision struct {
    Allowed    bool          `json:"allowed"`
    Reason     string        `json:"reason,omitempty"`
    RetryAfter time.Duration `json:"retry_after,omitempty"`
    RemainingQuota int64     `json:"remaining_quota,omitempty"`
}

type Request struct {
    RemoteAddr  string
    ClientID    string
    StreamID    string
    RequestType string
    DataSize    int64
    Timestamp   time.Time
}
```

## Integration Examples

### HTTP Middleware Integration
```go
func RateLimitMiddleware(rlm *RateLimiterManager) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            req := &Request{
                RemoteAddr:  r.RemoteAddr,
                ClientID:    r.Header.Get("X-Client-ID"),
                RequestType: r.URL.Path,
                Timestamp:   time.Now(),
            }
            
            decision, err := rlm.AllowRequest(req)
            if err != nil {
                http.Error(w, "Rate limiter error", http.StatusInternalServerError)
                return
            }
            
            if !decision.Allowed {
                w.Header().Set("X-RateLimit-Reason", decision.Reason)
                if decision.RetryAfter > 0 {
                    w.Header().Set("Retry-After", strconv.Itoa(int(decision.RetryAfter.Seconds())))
                }
                http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
                return
            }
            
            next.ServeHTTP(w, r)
        })
    }
}
```

### Stream Handler Integration
```go
func (h *StreamHandler) processPacket(packet *Packet) error {
    // Check stream-specific rate limits
    if !h.rateLimiter.AllowBitrate(int64(len(packet.Data))) {
        return fmt.Errorf("stream bitrate limit exceeded")
    }
    
    if !h.rateLimiter.AllowPacket() {
        return fmt.Errorf("stream packet rate limit exceeded")
    }
    
    // Process packet
    return h.actuallyProcessPacket(packet)
}
```

## Monitoring and Metrics

### Rate Limit Metrics
```go
type RateLimitMetrics struct {
    RequestsAllowed   *prometheus.CounterVec // by limiter_type
    RequestsDenied    *prometheus.CounterVec // by limiter_type, reason
    LimiterUtilization *prometheus.GaugeVec  // by limiter_type
    AdjustmentCount   *prometheus.CounterVec // by limiter_type, direction
    ResponseTime      *prometheus.HistogramVec // by limiter_type
}

func (rlm *RateLimiterManager) recordMetrics(decision *Decision, limiterType string) {
    if decision.Allowed {
        rlm.metrics.RequestsAllowed.WithLabelValues(limiterType).Inc()
    } else {
        rlm.metrics.RequestsDenied.WithLabelValues(limiterType, decision.Reason).Inc()
    }
}
```

## Configuration

### Rate Limit Configuration
```go
type RateLimitConfig struct {
    Global   GlobalLimitConfig   `yaml:"global"`
    Stream   StreamLimitConfig   `yaml:"stream"`
    Client   ClientLimitConfig   `yaml:"client"`
    IP       IPLimitConfig       `yaml:"ip"`
    Redis    RedisLimitConfig    `yaml:"redis"`
    Adaptive AdaptiveLimitConfig `yaml:"adaptive"`
}

type GlobalLimitConfig struct {
    MaxConnections    int64         `yaml:"max_connections"`
    MaxBandwidth      int64         `yaml:"max_bandwidth"` // bytes/sec
    MaxRequestsPerSec int64         `yaml:"max_requests_per_sec"`
    CPUThreshold      float64       `yaml:"cpu_threshold"`
    MemoryThreshold   int64         `yaml:"memory_threshold"`
}

type StreamLimitConfig struct {
    MaxBitrate        int64         `yaml:"max_bitrate"` // bits/sec
    MaxFPS            float64       `yaml:"max_fps"`
    MaxPacketsPerSec  int64         `yaml:"max_packets_per_sec"`
    BitrateWindow     time.Duration `yaml:"bitrate_window"`
}
```

## Testing

### Rate Limiter Testing
```bash
# Test rate limiting algorithms
go test ./internal/ingestion/ratelimit/...

# Test with Redis
go test -tags=redis ./internal/ingestion/ratelimit/...

# Benchmark rate limiters
go test -bench=. ./internal/ingestion/ratelimit/...
```

### Load Testing
```go
func TestRateLimiterUnderLoad(t *testing.T) {
    limiter := NewTokenBucket(100, 10) // 100 capacity, 10/sec refill
    
    // Simulate high load
    const numGoroutines = 100
    const requestsPerGoroutine = 1000
    
    var wg sync.WaitGroup
    allowed := int64(0)
    denied := int64(0)
    
    for i := 0; i < numGoroutines; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            
            for j := 0; j < requestsPerGoroutine; j++ {
                if limiter.Allow(1) {
                    atomic.AddInt64(&allowed, 1)
                } else {
                    atomic.AddInt64(&denied, 1)
                }
                time.Sleep(time.Microsecond) // Small delay
            }
        }()
    }
    
    wg.Wait()
    
    total := allowed + denied
    expectedTotal := int64(numGoroutines * requestsPerGoroutine)
    assert.Equal(t, expectedTotal, total)
    
    // Should deny most requests under high load
    assert.Greater(t, denied, allowed)
}
```

## Best Practices

1. **Choose Appropriate Algorithms**: Token bucket for burst handling, sliding window for precise limits
2. **Multi-Layer Defense**: Implement multiple levels of rate limiting
3. **Monitor Resource Usage**: Adjust limits based on system performance
4. **Graceful Degradation**: Provide meaningful error messages and retry guidance
5. **Distributed Coordination**: Use Redis for consistent limits across instances
6. **Performance Testing**: Regularly test rate limiters under load
7. **Adaptive Limits**: Implement dynamic adjustment based on system health
8. **Proper Cleanup**: Remove expired rate limit entries to prevent memory leaks
