# Security Package

This package provides security utilities and validation functions for the Mirror video streaming platform. It implements safe parsing, input validation, and protection against various security vulnerabilities.

## Overview

Video streaming applications handle untrusted input from multiple sources. This package provides:

- **Safe Parsing**: Secure parsing of video data with bounds checking
- **Input Validation**: Comprehensive validation of stream parameters
- **DoS Protection**: Guards against resource exhaustion attacks
- **Buffer Overflow Prevention**: Safe memory operations
- **LEB128 Decoding**: Secure variable-length integer decoding

## Core Security Components

### LEB128 (Little Endian Base 128) Decoder
```go
// LEB128 decoder with security bounds checking
type LEB128Decoder struct {
    data     []byte
    position int
    maxBytes int // Maximum bytes to read for a single value
}

func NewLEB128Decoder(data []byte) *LEB128Decoder {
    return &LEB128Decoder{
        data:     data,
        position: 0,
        maxBytes: 5, // Default maximum for 32-bit values
    }
}

// ReadUint64 reads an unsigned 64-bit LEB128 value with bounds checking
func (d *LEB128Decoder) ReadUint64() (uint64, error) {
    if d.position >= len(d.data) {
        return 0, ErrInsufficientData
    }
    
    var result uint64
    var shift uint
    bytesRead := 0
    
    for {
        if d.position >= len(d.data) {
            return 0, ErrInsufficientData
        }
        
        if bytesRead >= d.maxBytes {
            return 0, ErrLEB128TooLong
        }
        
        b := d.data[d.position]
        d.position++
        bytesRead++
        
        // Check for potential overflow
        if shift >= 64 {
            return 0, ErrLEB128Overflow
        }
        
        // Extract 7 bits of data
        result |= uint64(b&0x7F) << shift
        
        // Check if this is the last byte
        if (b & 0x80) == 0 {
            break
        }
        
        shift += 7
    }
    
    return result, nil
}

// ReadInt64 reads a signed 64-bit LEB128 value with bounds checking
func (d *LEB128Decoder) ReadInt64() (int64, error) {
    unsigned, err := d.ReadUint64()
    if err != nil {
        return 0, err
    }
    
    // Convert unsigned to signed using zigzag encoding
    return int64(unsigned>>1) ^ -(int64(unsigned) & 1), nil
}

// SetMaxBytes configures maximum bytes to read for a single value
func (d *LEB128Decoder) SetMaxBytes(max int) {
    if max > 0 && max <= 10 { // Reasonable bounds
        d.maxBytes = max
    }
}
```

### Size Limits and Validation
```go
// Security limits for various data types
type SecurityLimits struct {
    MaxFrameSize     int64 `yaml:"max_frame_size"`     // 10MB default
    MaxPacketSize    int64 `yaml:"max_packet_size"`    // 64KB default
    MaxGOPSize       int64 `yaml:"max_gop_size"`       // 100MB default
    MaxNALUnitSize   int64 `yaml:"max_nal_unit_size"`  // 1MB default
    MaxStringLength  int   `yaml:"max_string_length"`  // 1024 default
    MaxArrayElements int   `yaml:"max_array_elements"` // 10000 default
    
    // Rate limits
    MaxFramesPerSecond   float64 `yaml:"max_frames_per_second"`   // 120 FPS
    MaxPacketsPerSecond  int64   `yaml:"max_packets_per_second"`  // 10000 PPS
    MaxBytesPerSecond    int64   `yaml:"max_bytes_per_second"`    // 1GB/s
    
    // Parser limits
    MaxParseDepth        int     `yaml:"max_parse_depth"`         // 100 levels
    MaxParseTime         time.Duration `yaml:"max_parse_time"`    // 1 second
}

var DefaultSecurityLimits = SecurityLimits{
    MaxFrameSize:        10 * 1024 * 1024,  // 10MB
    MaxPacketSize:       64 * 1024,         // 64KB
    MaxGOPSize:          100 * 1024 * 1024, // 100MB
    MaxNALUnitSize:      1 * 1024 * 1024,   // 1MB
    MaxStringLength:     1024,
    MaxArrayElements:    10000,
    MaxFramesPerSecond:  120,
    MaxPacketsPerSecond: 10000,
    MaxBytesPerSecond:   1024 * 1024 * 1024, // 1GB/s
    MaxParseDepth:       100,
    MaxParseTime:        1 * time.Second,
}

// ValidateFrameSize checks if frame size is within security limits
func ValidateFrameSize(size int64, limits SecurityLimits) error {
    if size < 0 {
        return ErrInvalidFrameSize
    }
    
    if size > limits.MaxFrameSize {
        return fmt.Errorf("frame size %d exceeds maximum %d", size, limits.MaxFrameSize)
    }
    
    return nil
}

// ValidatePacketSize checks if packet size is within security limits
func ValidatePacketSize(size int64, limits SecurityLimits) error {
    if size < 0 {
        return ErrInvalidPacketSize
    }
    
    if size > limits.MaxPacketSize {
        return fmt.Errorf("packet size %d exceeds maximum %d", size, limits.MaxPacketSize)
    }
    
    return nil
}
```

### Safe Buffer Operations
```go
// SafeBuffer provides bounds-checked buffer operations
type SafeBuffer struct {
    data     []byte
    position int
    limit    int
}

func NewSafeBuffer(data []byte) *SafeBuffer {
    return &SafeBuffer{
        data:     data,
        position: 0,
        limit:    len(data),
    }
}

// ReadBytes safely reads n bytes from the buffer
func (sb *SafeBuffer) ReadBytes(n int) ([]byte, error) {
    if n < 0 {
        return nil, ErrInvalidLength
    }
    
    if sb.position+n > sb.limit {
        return nil, ErrBufferOverrun
    }
    
    result := make([]byte, n)
    copy(result, sb.data[sb.position:sb.position+n])
    sb.position += n
    
    return result, nil
}

// ReadUint32 safely reads a 32-bit unsigned integer
func (sb *SafeBuffer) ReadUint32() (uint32, error) {
    if sb.position+4 > sb.limit {
        return 0, ErrBufferOverrun
    }
    
    value := binary.BigEndian.Uint32(sb.data[sb.position:])
    sb.position += 4
    
    return value, nil
}

// ReadUint16 safely reads a 16-bit unsigned integer
func (sb *SafeBuffer) ReadUint16() (uint16, error) {
    if sb.position+2 > sb.limit {
        return 0, ErrBufferOverrun
    }
    
    value := binary.BigEndian.Uint16(sb.data[sb.position:])
    sb.position += 2
    
    return value, nil
}

// ReadUint8 safely reads an 8-bit unsigned integer
func (sb *SafeBuffer) ReadUint8() (uint8, error) {
    if sb.position+1 > sb.limit {
        return 0, ErrBufferOverrun
    }
    
    value := sb.data[sb.position]
    sb.position++
    
    return value, nil
}

// Skip safely advances the position by n bytes
func (sb *SafeBuffer) Skip(n int) error {
    if n < 0 {
        return ErrInvalidLength
    }
    
    if sb.position+n > sb.limit {
        return ErrBufferOverrun
    }
    
    sb.position += n
    return nil
}

// Remaining returns the number of bytes remaining in the buffer
func (sb *SafeBuffer) Remaining() int {
    return sb.limit - sb.position
}

// HasRemaining returns true if there are bytes remaining
func (sb *SafeBuffer) HasRemaining() bool {
    return sb.position < sb.limit
}
```

### Input Validation
```go
// Validator provides comprehensive input validation
type Validator struct {
    limits SecurityLimits
}

func NewValidator(limits SecurityLimits) *Validator {
    return &Validator{limits: limits}
}

// ValidateStreamID checks if stream ID is safe and valid
func (v *Validator) ValidateStreamID(streamID string) error {
    if len(streamID) == 0 {
        return ErrEmptyStreamID
    }
    
    if len(streamID) > v.limits.MaxStringLength {
        return fmt.Errorf("stream ID too long: %d > %d", len(streamID), v.limits.MaxStringLength)
    }
    
    // Check for dangerous characters
    if !isValidStreamID(streamID) {
        return ErrInvalidStreamIDCharacters
    }
    
    return nil
}

func isValidStreamID(streamID string) bool {
    // Allow alphanumeric, hyphen, underscore, and dot
    for _, r := range streamID {
        if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
            return false
        }
    }
    return true
}

// ValidateCodecType checks if codec type is supported and safe
func (v *Validator) ValidateCodecType(codec string) error {
    supportedCodecs := map[string]bool{
        "h264":   true,
        "h265":   true,
        "hevc":   true,
        "av1":    true,
        "jpegxs": true,
    }
    
    codecLower := strings.ToLower(codec)
    if !supportedCodecs[codecLower] {
        return fmt.Errorf("unsupported codec: %s", codec)
    }
    
    return nil
}

// ValidateNetworkAddress checks if network address is safe
func (v *Validator) ValidateNetworkAddress(addr string) error {
    if len(addr) == 0 {
        return ErrEmptyAddress
    }
    
    if len(addr) > v.limits.MaxStringLength {
        return fmt.Errorf("address too long: %d > %d", len(addr), v.limits.MaxStringLength)
    }
    
    // Parse and validate IP address
    if ip := net.ParseIP(addr); ip != nil {
        // Valid IP address
        if ip.IsLoopback() {
            return nil // Allow loopback
        }
        
        if ip.IsPrivate() {
            return nil // Allow private IPs
        }
        
        if ip.IsGlobalUnicast() {
            return nil // Allow public IPs
        }
        
        return ErrInvalidIPAddress
    }
    
    // Parse and validate hostname:port
    host, port, err := net.SplitHostPort(addr)
    if err != nil {
        return fmt.Errorf("invalid address format: %w", err)
    }
    
    // Validate hostname
    if err := v.validateHostname(host); err != nil {
        return err
    }
    
    // Validate port
    if err := v.validatePort(port); err != nil {
        return err
    }
    
    return nil
}

func (v *Validator) validateHostname(hostname string) error {
    if len(hostname) == 0 {
        return ErrEmptyHostname
    }
    
    if len(hostname) > 253 { // RFC 1035
        return ErrHostnameTooLong
    }
    
    // Basic hostname validation
    for _, r := range hostname {
        if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '.' {
            return ErrInvalidHostnameCharacters
        }
    }
    
    return nil
}

func (v *Validator) validatePort(port string) error {
    portNum, err := strconv.Atoi(port)
    if err != nil {
        return ErrInvalidPortNumber
    }
    
    if portNum < 1 || portNum > 65535 {
        return ErrPortOutOfRange
    }
    
    return nil
}
```

### DoS Protection
```go
// DoSProtector provides protection against denial of service attacks
type DoSProtector struct {
    limits     SecurityLimits
    rateLimits map[string]*RateLimit
    mu         sync.RWMutex
}

type RateLimit struct {
    requests  []time.Time
    maxRate   int
    window    time.Duration
    mu        sync.Mutex
}

func NewDoSProtector(limits SecurityLimits) *DoSProtector {
    return &DoSProtector{
        limits:     limits,
        rateLimits: make(map[string]*RateLimit),
    }
}

// CheckRateLimit verifies if client is within rate limits
func (dp *DoSProtector) CheckRateLimit(clientID string, requestType string) error {
    key := clientID + ":" + requestType
    
    dp.mu.RLock()
    rateLimit, exists := dp.rateLimits[key]
    dp.mu.RUnlock()
    
    if !exists {
        dp.mu.Lock()
        // Double-check after acquiring write lock
        if rateLimit, exists = dp.rateLimits[key]; !exists {
            rateLimit = &RateLimit{
                maxRate: dp.getMaxRateForType(requestType),
                window:  time.Minute,
            }
            dp.rateLimits[key] = rateLimit
        }
        dp.mu.Unlock()
    }
    
    return rateLimit.Allow()
}

func (rl *RateLimit) Allow() error {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    now := time.Now()
    cutoff := now.Add(-rl.window)
    
    // Remove old requests
    validRequests := 0
    for i, reqTime := range rl.requests {
        if reqTime.After(cutoff) {
            rl.requests = rl.requests[i:]
            validRequests = len(rl.requests)
            break
        }
    }
    
    if validRequests == 0 {
        rl.requests = rl.requests[:0]
    }
    
    // Check rate limit
    if len(rl.requests) >= rl.maxRate {
        return ErrRateLimitExceeded
    }
    
    // Add current request
    rl.requests = append(rl.requests, now)
    return nil
}

func (dp *DoSProtector) getMaxRateForType(requestType string) int {
    switch requestType {
    case "stream_create":
        return 10 // 10 stream creations per minute
    case "packet_send":
        return int(dp.limits.MaxPacketsPerSecond * 60) // Convert to per minute
    case "api_request":
        return 100 // 100 API requests per minute
    default:
        return 50 // Default rate limit
    }
}

// ValidateResourceUsage checks if resource usage is within limits
func (dp *DoSProtector) ValidateResourceUsage(usage ResourceUsage) error {
    if usage.CPUPercent > 95.0 {
        return ErrResourceExhaustion
    }
    
    if usage.MemoryBytes > dp.limits.MaxBytesPerSecond {
        return ErrMemoryLimitExceeded
    }
    
    if usage.NetworkBytesPerSec > dp.limits.MaxBytesPerSecond {
        return ErrBandwidthLimitExceeded
    }
    
    return nil
}

type ResourceUsage struct {
    CPUPercent         float64
    MemoryBytes        int64
    NetworkBytesPerSec int64
}
```

### Secure Video Data Parsing
```go
// SecureVideoParser provides safe parsing of video data
type SecureVideoParser struct {
    validator   *Validator
    dosProtector *DoSProtector
    limits      SecurityLimits
}

func NewSecureVideoParser(limits SecurityLimits) *SecureVideoParser {
    return &SecureVideoParser{
        validator:    NewValidator(limits),
        dosProtector: NewDoSProtector(limits),
        limits:      limits,
    }
}

// ParseFrame safely parses video frame data
func (svp *SecureVideoParser) ParseFrame(data []byte, clientID string) (*Frame, error) {
    // Check DoS protection
    if err := svp.dosProtector.CheckRateLimit(clientID, "frame_parse"); err != nil {
        return nil, err
    }
    
    // Validate frame size
    if err := ValidateFrameSize(int64(len(data)), svp.limits); err != nil {
        return nil, err
    }
    
    // Create safe buffer for parsing
    buffer := NewSafeBuffer(data)
    
    // Parse frame with timeout protection
    ctx, cancel := context.WithTimeout(context.Background(), svp.limits.MaxParseTime)
    defer cancel()
    
    frameChan := make(chan *Frame, 1)
    errChan := make(chan error, 1)
    
    go func() {
        frame, err := svp.parseFrameInternal(buffer)
        if err != nil {
            errChan <- err
        } else {
            frameChan <- frame
        }
    }()
    
    select {
    case frame := <-frameChan:
        return frame, nil
    case err := <-errChan:
        return nil, err
    case <-ctx.Done():
        return nil, ErrParseTimeout
    }
}

func (svp *SecureVideoParser) parseFrameInternal(buffer *SafeBuffer) (*Frame, error) {
    frame := &Frame{}
    
    // Parse frame header with bounds checking
    frameType, err := buffer.ReadUint8()
    if err != nil {
        return nil, fmt.Errorf("failed to read frame type: %w", err)
    }
    frame.Type = FrameType(frameType)
    
    // Validate frame type
    if !frame.Type.IsValid() {
        return nil, ErrInvalidFrameType
    }
    
    // Parse frame size
    frameSize, err := buffer.ReadUint32()
    if err != nil {
        return nil, fmt.Errorf("failed to read frame size: %w", err)
    }
    
    // Validate frame size consistency
    if int64(frameSize) != int64(buffer.Remaining()) {
        return nil, ErrFrameSizeMismatch
    }
    
    // Parse frame data
    frameData, err := buffer.ReadBytes(int(frameSize))
    if err != nil {
        return nil, fmt.Errorf("failed to read frame data: %w", err)
    }
    frame.Data = frameData
    
    return frame, nil
}

// ParseNALUnit safely parses H.264/HEVC NAL units
func (svp *SecureVideoParser) ParseNALUnit(data []byte, clientID string) (*NALUnit, error) {
    // Check DoS protection
    if err := svp.dosProtector.CheckRateLimit(clientID, "nal_parse"); err != nil {
        return nil, err
    }
    
    // Validate NAL unit size
    if int64(len(data)) > svp.limits.MaxNALUnitSize {
        return nil, ErrNALUnitTooLarge
    }
    
    if len(data) < 1 {
        return nil, ErrNALUnitTooSmall
    }
    
    nalUnit := &NALUnit{}
    
    // Parse NAL unit header
    header := data[0]
    nalUnit.Type = header & 0x1F
    nalUnit.Priority = (header >> 5) & 0x03
    
    // Validate NAL unit type
    if !isValidNALUnitType(nalUnit.Type) {
        return nil, ErrInvalidNALUnitType
    }
    
    // Parse payload
    if len(data) > 1 {
        nalUnit.Payload = make([]byte, len(data)-1)
        copy(nalUnit.Payload, data[1:])
    }
    
    return nalUnit, nil
}

func isValidNALUnitType(nalType uint8) bool {
    // H.264 NAL unit types (simplified)
    validTypes := map[uint8]bool{
        1:  true, // Coded slice of a non-IDR picture
        5:  true, // Coded slice of an IDR picture
        7:  true, // Sequence parameter set
        8:  true, // Picture parameter set
        9:  true, // Access unit delimiter
        12: true, // Filler data
    }
    
    return validTypes[nalType]
}
```

## Error Definitions

### Security Errors
```go
// Security-related errors
var (
    // Buffer errors
    ErrBufferOverrun     = errors.New("buffer overrun detected")
    ErrInsufficientData  = errors.New("insufficient data")
    ErrInvalidLength     = errors.New("invalid length")
    
    // LEB128 errors
    ErrLEB128TooLong     = errors.New("LEB128 value too long")
    ErrLEB128Overflow    = errors.New("LEB128 overflow")
    
    // Validation errors
    ErrInvalidFrameSize  = errors.New("invalid frame size")
    ErrInvalidPacketSize = errors.New("invalid packet size")
    ErrFrameSizeMismatch = errors.New("frame size mismatch")
    ErrInvalidFrameType  = errors.New("invalid frame type")
    
    // Stream validation errors
    ErrEmptyStreamID               = errors.New("empty stream ID")
    ErrInvalidStreamIDCharacters   = errors.New("invalid characters in stream ID")
    ErrEmptyAddress               = errors.New("empty network address")
    ErrInvalidIPAddress           = errors.New("invalid IP address")
    ErrEmptyHostname              = errors.New("empty hostname")
    ErrHostnameTooLong            = errors.New("hostname too long")
    ErrInvalidHostnameCharacters  = errors.New("invalid characters in hostname")
    ErrInvalidPortNumber          = errors.New("invalid port number")
    ErrPortOutOfRange             = errors.New("port number out of range")
    
    // DoS protection errors
    ErrRateLimitExceeded      = errors.New("rate limit exceeded")
    ErrResourceExhaustion     = errors.New("resource exhaustion detected")
    ErrMemoryLimitExceeded    = errors.New("memory limit exceeded")
    ErrBandwidthLimitExceeded = errors.New("bandwidth limit exceeded")
    
    // Parse errors
    ErrParseTimeout       = errors.New("parse timeout")
    ErrNALUnitTooLarge    = errors.New("NAL unit too large")
    ErrNALUnitTooSmall    = errors.New("NAL unit too small")
    ErrInvalidNALUnitType = errors.New("invalid NAL unit type")
)
```

## Integration Examples

### Stream Handler Integration
```go
func (h *StreamHandler) processPacket(packet []byte, clientID string) error {
    // Validate packet with security checks
    if err := h.securityValidator.ValidatePacketSize(int64(len(packet)), h.securityLimits); err != nil {
        return fmt.Errorf("packet validation failed: %w", err)
    }
    
    // Parse packet safely
    parsedPacket, err := h.secureParser.ParsePacket(packet, clientID)
    if err != nil {
        return fmt.Errorf("secure parsing failed: %w", err)
    }
    
    // Process parsed packet
    return h.actuallyProcessPacket(parsedPacket)
}
```

### API Handler Integration
```go
func (a *API) handleCreateStream(w http.ResponseWriter, r *http.Request) {
    // Extract client ID from request
    clientID := a.getClientID(r)
    
    // Check rate limits
    if err := a.dosProtector.CheckRateLimit(clientID, "stream_create"); err != nil {
        http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
        return
    }
    
    // Parse and validate request
    var req CreateStreamRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "Invalid request", http.StatusBadRequest)
        return
    }
    
    // Validate stream parameters
    if err := a.validator.ValidateStreamID(req.StreamID); err != nil {
        http.Error(w, fmt.Sprintf("Invalid stream ID: %v", err), http.StatusBadRequest)
        return
    }
    
    if err := a.validator.ValidateNetworkAddress(req.SourceAddr); err != nil {
        http.Error(w, fmt.Sprintf("Invalid source address: %v", err), http.StatusBadRequest)
        return
    }
    
    // Process request
    // ...
}
```

## Configuration

### Security Configuration
```go
type SecurityConfig struct {
    // Basic limits
    Limits SecurityLimits `yaml:"limits"`
    
    // DoS protection
    DoSProtection DoSConfig `yaml:"dos_protection"`
    
    // Validation settings
    Validation ValidationConfig `yaml:"validation"`
    
    // Logging
    LogSuspiciousActivity bool `yaml:"log_suspicious_activity"`
    LogFailedValidation   bool `yaml:"log_failed_validation"`
}

type DoSConfig struct {
    Enabled           bool          `yaml:"enabled"`
    RateLimitWindow   time.Duration `yaml:"rate_limit_window"`
    MaxRequestsPerIP  int           `yaml:"max_requests_per_ip"`
    BanDuration       time.Duration `yaml:"ban_duration"`
    MonitorInterval   time.Duration `yaml:"monitor_interval"`
}

type ValidationConfig struct {
    StrictMode        bool     `yaml:"strict_mode"`
    AllowedCodecs     []string `yaml:"allowed_codecs"`
    AllowPrivateIPs   bool     `yaml:"allow_private_ips"`
    AllowLoopback     bool     `yaml:"allow_loopback"`
    RequireEncryption bool     `yaml:"require_encryption"`
}
```

## Testing

### Security Testing
```bash
# Test security utilities
go test ./internal/ingestion/security/...

# Test with fuzzing
go test -fuzz=FuzzLEB128 ./internal/ingestion/security/...

# Test DoS protection
go test -run TestDoSProtection ./internal/ingestion/security/...
```

### Fuzzing Tests
```go
func FuzzLEB128Decoder(f *testing.F) {
    // Add seed corpus
    f.Add([]byte{0x00})        // Zero
    f.Add([]byte{0x7F})        // Maximum single byte
    f.Add([]byte{0x80, 0x01})  // Two byte value
    f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F}) // Maximum 32-bit
    
    f.Fuzz(func(t *testing.T, data []byte) {
        decoder := NewLEB128Decoder(data)
        
        // Should not panic
        _, err := decoder.ReadUint64()
        
        // Error is acceptable, panic is not
        if err != nil {
            // Expected for malformed data
            return
        }
    })
}

func FuzzSafeBuffer(f *testing.F) {
    f.Add([]byte{0x01, 0x02, 0x03, 0x04})
    
    f.Fuzz(func(t *testing.T, data []byte) {
        buffer := NewSafeBuffer(data)
        
        // Try various operations
        buffer.ReadUint8()
        buffer.ReadUint16()
        buffer.ReadUint32()
        buffer.ReadBytes(len(data))
        
        // Should not panic
    })
}
```

## Monitoring

### Security Metrics
```go
type SecurityMetrics struct {
    ValidationFailures   *prometheus.CounterVec // by type, reason
    RateLimitViolations  *prometheus.CounterVec // by client_id, type
    DoSAttemptsBlocked   *prometheus.CounterVec // by source_ip
    ParseTimeouts        prometheus.Counter
    BufferOverruns       prometheus.Counter
    SuspiciousActivity   *prometheus.CounterVec // by type
}

func (sm *SecurityMetrics) RecordValidationFailure(validationType, reason string) {
    sm.ValidationFailures.WithLabelValues(validationType, reason).Inc()
}

func (sm *SecurityMetrics) RecordRateLimitViolation(clientID, requestType string) {
    sm.RateLimitViolations.WithLabelValues(clientID, requestType).Inc()
}
```

## Best Practices

1. **Input Validation**: Always validate all external input
2. **Bounds Checking**: Use safe buffer operations for all parsing
3. **Rate Limiting**: Implement rate limits at multiple levels
4. **Resource Limits**: Enforce maximum sizes and timeouts
5. **Error Handling**: Log security violations for monitoring
6. **Regular Testing**: Use fuzzing to test edge cases
7. **Principle of Least Privilege**: Minimize permissions and access
8. **Defense in Depth**: Implement multiple layers of security controls
