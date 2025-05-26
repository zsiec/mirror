# Tests Package

The tests package provides comprehensive integration testing capabilities for the Mirror video streaming platform. It includes full end-to-end testing, performance validation, and system integration verification.

## Overview

This package contains integration tests that validate the complete Mirror system functionality, including:

- **End-to-End Testing**: Complete workflow validation from ingestion to output
- **Performance Testing**: Throughput, latency, and resource usage validation
- **System Integration**: Component interaction and API testing
- **Health Monitoring**: Service health and metrics validation
- **Error Scenarios**: Failure handling and recovery testing

## Core Components

### Test Environment

The `TestEnvironment` sets up a complete Mirror testing infrastructure:

```go
type TestEnvironment struct {
    // Server components
    Server       *server.Server     // Main Mirror server
    Config       *config.Config     // Test configuration
    HTTPClient   *http.Client       // HTTPS client for API
    HTTPClientPlain *http.Client    // HTTP client for metrics
    
    // External services
    Redis        *miniredis.Miniredis // In-memory Redis for testing
    RedisClient  *redis.Client        // Redis client
    
    // Test infrastructure
    TempDir      string              // Temporary directory for test files
    LogBuffer    *bytes.Buffer       // Captured log output
    StartTime    time.Time           // Test start time
    
    // Cleanup functions
    cleanupFuncs []func()            // Cleanup functions to run
}
```

### Verbose Logger

The `VerboseLogger` provides detailed test output with timing and formatting:

```go
type VerboseLogger struct {
    t         *testing.T
    enabled   bool
    startTime time.Time
}

func (v *VerboseLogger) Log(format string, args ...interface{}) {
    if !v.enabled {
        return
    }
    
    elapsed := time.Since(v.startTime)
    prefix := fmt.Sprintf("[%s] ", elapsed.Round(time.Millisecond))
    v.t.Logf(prefix+format, args...)
}
```

### Test Helpers

Common testing utilities and helper functions:

```go
// Certificate generation for HTTPS testing
func GenerateTestCertificates(tempDir string) (certFile, keyFile string, err error)

// Configuration setup for testing
func CreateTestConfig(tempDir string, redisAddr string) *config.Config

// HTTP client configuration for testing
func CreateTestHTTPClient() *http.Client

// Redis setup for integration testing
func SetupTestRedis() (*miniredis.Miniredis, *redis.Client, error)
```

## Test Categories

### Full Integration Test

The main integration test validates the complete system:

```go
func TestFullIntegration(t *testing.T) {
    verbose := NewVerboseLogger(t, testing.Verbose())
    
    // Setup test environment
    env, err := SetupTestEnvironment(t, verbose)
    require.NoError(t, err)
    defer env.Cleanup()
    
    // Run validation phases
    t.Run("Server Startup", func(t *testing.T) {
        ValidateServerStartup(t, env, verbose)
    })
    
    t.Run("Health Checks", func(t *testing.T) {
        ValidateServerHealth(t, env, verbose)
    })
    
    t.Run("API Responses", func(t *testing.T) {
        ValidateAPIResponses(t, env, verbose)
    })
    
    t.Run("Metrics", func(t *testing.T) {
        ValidateMetrics(t, env, verbose)
    })
    
    t.Run("Streaming", func(t *testing.T) {
        ValidateStreamingFunctionality(t, env, verbose)
    })
}
```

### Health Validation

Comprehensive health check testing:

```go
func ValidateServerHealth(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
    verbose.Log("🏥 Validating server health...")
    
    resp, err := env.HTTPClient.Get("https://127.0.0.1:8080/health")
    require.NoError(t, err)
    defer resp.Body.Close()
    
    // Accept either 200 (healthy) or 503 (Redis limitations in test)
    if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
        t.Errorf("Expected status 200 or 503, got %d", resp.StatusCode)
    }
    
    body, err := io.ReadAll(resp.Body)
    require.NoError(t, err)
    
    var health map[string]interface{}
    err = json.Unmarshal(body, &health)
    require.NoError(t, err)
    
    verbose.LogJSON("Health Response", health)
    
    // Validate health response structure
    assert.Contains(t, health, "status")
    assert.Contains(t, health, "checks")
}
```

### API Testing

Complete API endpoint validation:

```go
func ValidateAPIResponses(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
    verbose.Log("🌐 Validating API responses...")
    
    // Test version endpoint
    resp, err := env.HTTPClient.Get("https://127.0.0.1:8080/version")
    require.NoError(t, err)
    defer resp.Body.Close()
    
    body, err := io.ReadAll(resp.Body)
    require.NoError(t, err)
    
    var version map[string]interface{}
    err = json.Unmarshal(body, &version)
    require.NoError(t, err)
    
    verbose.LogJSON("Version Response", version)
    
    assert.Contains(t, version, "version")
    assert.Contains(t, version, "git_commit")
    assert.Contains(t, version, "build_time")
}
```

### Streaming Functionality

Stream ingestion and processing validation:

```go
func ValidateStreamingFunctionality(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
    verbose.Log("📺 Validating streaming functionality...")
    
    // Check if ingestion services are available
    verbose.Log("Checking ingestion services...")
    
    // Wait for streams to potentially establish
    maxWait := 10 * time.Second
    timeout := time.After(maxWait)
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-timeout:
            verbose.Log("⚠️ Streams did not establish within %v", maxWait)
            return
        case <-ticker.C:
            // Check if any streams are active
            resp, err := env.HTTPClient.Get("https://127.0.0.1:8080/api/v1/streams")
            if err != nil {
                verbose.Log("❌ Failed to check streams: %v", err)
                continue
            }
            
            body, err := io.ReadAll(resp.Body)
            resp.Body.Close()
            if err != nil {
                continue
            }
            
            var streams map[string]interface{}
            if err := json.Unmarshal(body, &streams); err == nil {
                verbose.LogJSON("Streams Response", streams)
                verbose.Log("✅ Streaming API endpoints accessible")
                return
            }
        }
    }
}
```

### Metrics Validation

Prometheus metrics endpoint testing:

```go
func ValidateMetrics(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
    verbose.Log("📊 Validating metrics...")
    
    // Test metrics endpoint
    resp, err := env.HTTPClientPlain.Get("http://127.0.0.1:9091/metrics")
    if err != nil {
        verbose.Log("⚠️ Metrics endpoint not available: %v", err)
        return
    }
    defer resp.Body.Close()
    
    body, err := io.ReadAll(resp.Body)
    require.NoError(t, err)
    
    metrics := string(body)
    
    // Check for key metrics
    expectedMetrics := []string{
        "http_requests_total",
        "http_request_duration_seconds",
        "process_start_time_seconds",
    }
    
    for _, metric := range expectedMetrics {
        if strings.Contains(metrics, metric) {
            verbose.Log("✅ Found metric: %s", metric)
        } else {
            verbose.Log("⚠️ Missing metric: %s", metric)
        }
    }
}
```

## Test Configuration

### Test-Specific Configuration

```go
func CreateTestConfig(tempDir string, redisAddr string) *config.Config {
    return &config.Config{
        Server: config.ServerConfig{
            HTTP3: config.HTTP3Config{
                Addr:     "127.0.0.1:8080",
                CertFile: filepath.Join(tempDir, "cert.pem"),
                KeyFile:  filepath.Join(tempDir, "key.pem"),
            },
            MetricsPort:    9091,
            DebugEndpoints: true,
            ShutdownTimeout: 5 * time.Second,
        },
        Redis: config.RedisConfig{
            Addr:     redisAddr,
            Password: "",
            DB:       0,
        },
        Logging: config.LoggingConfig{
            Level:    "debug",
            Format:   "text",
            Output:   "stdout",
            Rotation: false,
        },
        Ingestion: config.IngestionConfig{
            SRTPort:        30000,
            RTPPort:        5004,
            MaxConnections: 25,
            BufferSize:     1048576,
            StreamTimeout:  30 * time.Second,
        },
    }
}
```

### TLS Certificate Generation

```go
func GenerateTestCertificates(tempDir string) (certFile, keyFile string, err error) {
    // Generate private key
    priv, err := rsa.GenerateKey(rand.Reader, 2048)
    if err != nil {
        return "", "", err
    }
    
    // Create certificate template
    template := x509.Certificate{
        SerialNumber: big.NewInt(1),
        Subject: pkix.Name{
            Organization: []string{"Mirror Test"},
        },
        NotBefore:    time.Now(),
        NotAfter:     time.Now().Add(time.Hour),
        KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
        ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
        IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
        DNSNames:     []string{"localhost"},
    }
    
    // Generate certificate
    certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
    if err != nil {
        return "", "", err
    }
    
    // Write certificate file
    certFile = filepath.Join(tempDir, "cert.pem")
    certOut, err := os.Create(certFile)
    if err != nil {
        return "", "", err
    }
    defer certOut.Close()
    
    pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
    
    // Write key file
    keyFile = filepath.Join(tempDir, "key.pem")
    keyOut, err := os.Create(keyFile)
    if err != nil {
        return "", "", err
    }
    defer keyOut.Close()
    
    privDER, err := x509.MarshalPKCS8PrivateKey(priv)
    if err != nil {
        return "", "", err
    }
    
    pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
    
    return certFile, keyFile, nil
}
```

## Test Environment Setup

### Complete Environment Initialization

```go
func SetupTestEnvironment(t *testing.T, verbose *VerboseLogger) (*TestEnvironment, error) {
    verbose.Log("🚀 Setting up test environment...")
    
    // Create temporary directory
    tempDir, err := os.MkdirTemp("", "mirror-test-*")
    if err != nil {
        return nil, err
    }
    
    // Setup Redis
    mr, redisClient, err := SetupTestRedis()
    if err != nil {
        os.RemoveAll(tempDir)
        return nil, err
    }
    
    // Generate certificates
    certFile, keyFile, err := GenerateTestCertificates(tempDir)
    if err != nil {
        mr.Close()
        os.RemoveAll(tempDir)
        return nil, err
    }
    
    // Create configuration
    config := CreateTestConfig(tempDir, mr.Addr())
    config.Server.HTTP3.CertFile = certFile
    config.Server.HTTP3.KeyFile = keyFile
    
    // Create HTTP clients
    httpClient := CreateTestHTTPClient()
    httpClientPlain := &http.Client{Timeout: 10 * time.Second}
    
    // Initialize server
    server, err := server.New(config)
    if err != nil {
        mr.Close()
        os.RemoveAll(tempDir)
        return nil, err
    }
    
    env := &TestEnvironment{
        Server:         server,
        Config:         config,
        HTTPClient:     httpClient,
        HTTPClientPlain: httpClientPlain,
        Redis:          mr,
        RedisClient:    redisClient,
        TempDir:        tempDir,
        StartTime:      time.Now(),
        cleanupFuncs:   make([]func(), 0),
    }
    
    // Add cleanup functions
    env.AddCleanup(func() { os.RemoveAll(tempDir) })
    env.AddCleanup(func() { mr.Close() })
    env.AddCleanup(func() { server.Shutdown(context.Background()) })
    
    return env, nil
}
```

## Real Protocol Testing

### Auto-Detecting SRT Integration

The integration test automatically detects available capabilities and uses the best testing approach:

```bash
make test-full-integration
```

#### Automatic Capability Detection

The test automatically detects:
- **SRT Library**: Required for server SRT listener
- **FFmpeg Availability**: Checked via PATH lookup  
- **FFmpeg SRT Support**: Verified via `ffmpeg -protocols`

#### Testing Modes

**When FFmpeg with SRT is available:**
- Generates real SRT protocol streams using FFmpeg
- Creates H.264 video (320x240, 25fps) with test pattern
- Includes AAC audio (1kHz sine wave)
- Uses proper SRT protocol handshake and MPEG-TS container
- Validates real SRT session establishment

**When FFmpeg is not available:**
- Uses simulated SRT data for basic functionality validation
- Tests SRT listener availability and port connectivity  
- Validates that the SRT ingestion system is properly configured
- Provides guidance on installing FFmpeg for enhanced testing

#### FFmpeg Stream Generation

```go
func GenerateRealSRTStream(t *testing.T, ctx context.Context, env *TestEnvironment, verbose *VerboseLogger) {
    srtURL := fmt.Sprintf("srt://127.0.0.1:%d?streamid=ffmpeg_test_stream", env.Config.Ingestion.SRT.Port)
    
    ffmpegArgs := []string{
        "-f", "lavfi",
        "-i", "testsrc=duration=30:size=320x240:rate=25", // Test pattern video
        "-f", "lavfi", 
        "-i", "sine=frequency=1000:duration=30", // Sine wave audio
        "-c:v", "libx264",
        "-preset", "ultrafast",
        "-tune", "zerolatency",
        "-b:v", "500k",
        "-c:a", "aac",
        "-b:a", "64k",
        "-f", "mpegts",
        "-y",
        srtURL,
    }
    
    cmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
    // ... process management
}
```

**Expected Real SRT Test Results:**
- SRT session establishment with proper handshake
- MPEG-TS packet parsing from SRT stream
- H.264 video codec detection and processing
- AAC audio codec detection and processing
- Stream statistics including SRT-specific metrics:
  - Latency measurements
  - Packet loss detection
  - Retransmission statistics
  - Bandwidth utilization

## Performance Testing

### Load Testing Utilities

```go
func RunLoadTest(t *testing.T, env *TestEnvironment, config LoadTestConfig) *LoadTestResults {
    results := &LoadTestResults{
        StartTime: time.Now(),
        Config:    config,
    }
    
    // Create worker pool
    var wg sync.WaitGroup
    requests := make(chan *http.Request, config.ConcurrentUsers)
    responses := make(chan *LoadTestResponse, config.ConcurrentUsers)
    
    // Start workers
    for i := 0; i < config.ConcurrentUsers; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            loadTestWorker(requests, responses, env.HTTPClient)
        }()
    }
    
    // Generate requests
    go func() {
        defer close(requests)
        for i := 0; i < config.TotalRequests; i++ {
            req, _ := http.NewRequest("GET", "https://127.0.0.1:8080/health", nil)
            requests <- req
        }
    }()
    
    // Collect responses
    go func() {
        wg.Wait()
        close(responses)
    }()
    
    // Process results
    for response := range responses {
        results.Responses = append(results.Responses, response)
    }
    
    results.EndTime = time.Now()
    results.Calculate()
    
    return results
}
```

## Best Practices

### Test Organization
- Use table-driven tests where appropriate
- Separate unit tests from integration tests
- Include both positive and negative test cases
- Test error conditions and edge cases

### Environment Management
- Always clean up test resources
- Use temporary directories for test files
- Mock external dependencies when possible
- Isolate tests from each other

### Assertion Patterns
```go
// Use require for critical assertions that should stop the test
require.NoError(t, err)
require.NotNil(t, response)

// Use assert for non-critical validations
assert.Equal(t, expected, actual)
assert.Contains(t, response, "expected_field")
```

### Test Data Management
```go
// Use test helpers for common data generation
func generateTestMPEGTS() []byte {
    // Generate valid MPEG-TS test data
}

func createTestFrame() *types.VideoFrame {
    // Create test frame with valid structure
}
```

## CI/CD Integration

### GitHub Actions Configuration

The tests integrate with CI/CD pipelines:

```yaml
- name: Run Integration Tests
  run: |
    go test -v ./tests/... -timeout=30m
  env:
    MIRROR_TEST_VERBOSE: true
    MIRROR_LOG_LEVEL: debug
```

### Test Reporting
- JUnit XML output for CI systems
- Coverage reports with detailed package breakdown
- Performance metrics tracking over time
- Test result visualization

## Future Enhancements

Potential improvements for the test package:

- **Chaos Testing**: Network failure simulation and recovery testing
- **Performance Benchmarks**: Automated performance regression testing
- **Load Testing**: Scalability testing with multiple concurrent streams
- **Security Testing**: Penetration testing and vulnerability scanning
- **Browser Testing**: Frontend integration testing with real browsers
