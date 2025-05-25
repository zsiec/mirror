# Phase 7: Testing, Optimization, and Production Deployment

## Overview
This final phase focuses on comprehensive testing, performance optimization, security hardening, and production deployment. We'll conduct load testing with 5,000 concurrent viewers, optimize for sub-second latency, implement security best practices, and create deployment automation with monitoring and rollback capabilities.

## Goals
- Conduct comprehensive load and stress testing
- Optimize performance for production scale
- Implement security hardening and compliance
- Create automated deployment pipelines
- Build operational runbooks and documentation
- Set up production monitoring and alerting
- Implement disaster recovery procedures

## New Components Structure
```
testing/
├── load/
│   ├── scenarios/
│   │   ├── viewer_surge.js     # K6 viewer surge test
│   │   ├── stream_lifecycle.js # Full stream lifecycle
│   │   └── failover.js         # Multi-region failover
│   ├── data/
│   │   └── test_streams/       # Sample HEVC streams
│   └── reports/                # Test result reports
├── integration/
│   ├── e2e/                    # End-to-end tests
│   ├── api/                    # API integration tests
│   └── performance/            # Performance benchmarks
├── security/
│   ├── penetration/            # Pen test scenarios
│   └── compliance/             # Compliance checks
deployment/
├── terraform/
│   ├── modules/
│   │   ├── compute/            # ECS/GPU instances
│   │   ├── networking/         # VPC/ALB/CloudFront
│   │   ├── storage/            # S3/EFS configuration
│   │   └── monitoring/         # CloudWatch/Grafana
│   ├── environments/
│   │   ├── staging/
│   │   └── production/
│   └── scripts/
├── kubernetes/
│   ├── base/                   # Base manifests
│   ├── overlays/               # Environment overlays
│   └── operators/              # GPU/monitoring operators
├── ansible/
│   ├── playbooks/              # Deployment playbooks
│   └── roles/                  # Ansible roles
docs/
├── operations/
│   ├── runbooks/               # Operational procedures
│   ├── troubleshooting/        # Debug guides
│   └── architecture/           # System diagrams
```

## Implementation Details

### 1. Load Testing Configuration
```javascript
// testing/load/scenarios/viewer_surge.js
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';
import ws from 'k6/ws';

// Custom metrics
const errorRate = new Rate('errors');
const bufferingRate = new Rate('buffering');

// Test configuration
export const options = {
  scenarios: {
    // Gradual ramp-up to 5000 viewers
    viewer_surge: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '2m', target: 1000 },  // Warm-up
        { duration: '3m', target: 2500 },  // Ramp to 50%
        { duration: '5m', target: 5000 },  // Full load
        { duration: '10m', target: 5000 }, // Sustain
        { duration: '2m', target: 0 },     // Ramp down
      ],
      gracefulRampDown: '30s',
    },
    
    // Spike test
    viewer_spike: {
      executor: 'constant-arrival-rate',
      rate: 500,
      timeUnit: '1s',
      duration: '30s',
      preAllocatedVUs: 1000,
      startTime: '15m', // Start after warm-up
    },
    
    // Multi-stream viewing
    multi_stream: {
      executor: 'per-vu-iterations',
      vus: 500,
      iterations: 1,
      startTime: '10m',
    },
  },
  
  thresholds: {
    http_req_duration: ['p(95)<1000', 'p(99)<2000'], // 95% under 1s
    errors: ['rate<0.01'],                            // <1% error rate
    buffering: ['rate<0.05'],                         // <5% buffering
    ws_connecting: ['p(95)<500'],                     // WebSocket connection
  },
};

// Test data
const BASE_URL = __ENV.BASE_URL || 'https://streaming.example.com';
const STREAMS = [
  'stream001', 'stream002', 'stream003', 'stream004', 'stream005',
  'stream006', 'stream007', 'stream008', 'stream009', 'stream010',
  'stream011', 'stream012', 'stream013', 'stream014', 'stream015',
  'stream016', 'stream017', 'stream018', 'stream019', 'stream020',
  'stream021', 'stream022', 'stream023', 'stream024', 'stream025',
];

// Viewer behavior simulation
export default function () {
  // Select random stream
  const streamId = STREAMS[Math.floor(Math.random() * STREAMS.length)];
  const sessionId = `viewer_${__VU}_${Date.now()}`;
  
  // 1. Get master playlist
  let res = http.get(`${BASE_URL}/stream/${streamId}/playlist.m3u8`, {
    headers: {
      'User-Agent': 'HLS-Player/1.0',
      'X-Session-ID': sessionId,
    },
  });
  
  check(res, {
    'master playlist success': (r) => r.status === 200,
    'master playlist has content': (r) => r.body.includes('#EXTM3U'),
  });
  
  if (res.status !== 200) {
    errorRate.add(1);
    return;
  }
  
  // 2. Establish WebSocket for real-time updates
  const wsUrl = `wss://${BASE_URL.replace('https://', '')}/api/v1/ws`;
  const params = { headers: { 'X-Session-ID': sessionId } };
  
  ws.connect(wsUrl, params, function (socket) {
    socket.on('open', () => {
      // Subscribe to stream updates
      socket.send(JSON.stringify({
        type: 'subscribe',
        data: { topics: [`stream.${streamId}`] },
      }));
    });
    
    socket.on('message', (data) => {
      const msg = JSON.parse(data);
      if (msg.type === 'stream.quality_change') {
        console.log(`Quality change for ${streamId}: ${msg.data.quality}`);
      }
    });
    
    // Simulate viewing session
    socket.setTimeout(() => {
      socket.close();
    }, 30000 + Math.random() * 60000); // 30-90 seconds
  });
  
  // 3. Fetch segments continuously
  let segmentNum = 0;
  let lastPlaylistUpdate = Date.now();
  let bufferingEvents = 0;
  
  while (segmentNum < 60) { // Watch for ~30 seconds
    // Fetch playlist with LL-HLS parameters
    const playlistRes = http.get(
      `${BASE_URL}/stream/${streamId}/playlist.m3u8?_HLS_msn=${segmentNum}&_HLS_part=0`,
      {
        headers: {
          'X-Session-ID': sessionId,
          'X-Playback-Session-Id': `${sessionId}_playback`,
        },
        timeout: '5s',
      }
    );
    
    if (playlistRes.status !== 200) {
      errorRate.add(1);
      bufferingEvents++;
      sleep(1);
      continue;
    }
    
    // Parse playlist for segments
    const segments = parseSegments(playlistRes.body);
    
    // Fetch next segment
    if (segments.length > segmentNum) {
      const segment = segments[segmentNum];
      const segmentRes = http.get(
        `${BASE_URL}/stream/${streamId}/${segment.uri}`,
        {
          headers: {
            'X-Session-ID': sessionId,
            'Range': 'bytes=0-', // Support byte-range requests
          },
          responseType: 'binary',
          timeout: '2s',
        }
      );
      
      check(segmentRes, {
        'segment success': (r) => r.status === 200 || r.status === 206,
        'segment size ok': (r) => r.body.length > 10000,
      });
      
      if (segmentRes.status === 200 || segmentRes.status === 206) {
        segmentNum++;
        
        // Simulate playback timing
        sleep(segment.duration || 0.5);
      } else {
        bufferingEvents++;
        sleep(0.1);
      }
    } else {
      // Wait for playlist update
      sleep(0.1);
    }
    
    // Check for buffering
    if (Date.now() - lastPlaylistUpdate > 2000) {
      bufferingRate.add(1);
    }
  }
  
  // Report session metrics
  if (bufferingEvents > 3) {
    bufferingRate.add(1);
  }
}

// Helper function to parse segments from playlist
function parseSegments(playlistBody) {
  const lines = playlistBody.split('\n');
  const segments = [];
  let duration = 0;
  
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].startsWith('#EXTINF:')) {
      duration = parseFloat(lines[i].split(':')[1].split(',')[0]);
    } else if (lines[i] && !lines[i].startsWith('#')) {
      segments.push({
        uri: lines[i].trim(),
        duration: duration,
      });
    }
  }
  
  return segments;
}

// Multi-stream viewing scenario
export function multiStreamViewing() {
  const viewerId = `multi_viewer_${__VU}`;
  const numStreams = 6; // View 6 streams concurrently
  const selectedStreams = STREAMS.slice(0, numStreams);
  
  // Fetch all playlists
  const responses = http.batch(
    selectedStreams.map(streamId => ({
      method: 'GET',
      url: `${BASE_URL}/stream/${streamId}/playlist.m3u8`,
      params: {
        headers: { 'X-Viewer-ID': viewerId },
      },
    }))
  );
  
  // Check all successful
  responses.forEach((res, idx) => {
    check(res, {
      [`stream ${idx} loaded`]: (r) => r.status === 200,
    });
  });
  
  // Simulate viewing multiple streams
  sleep(30);
}
```

### 2. Performance Optimization
```go
// internal/optimization/performance.go
package optimization

import (
    "runtime"
    "runtime/debug"
    "time"
    
    "github.com/sirupsen/logrus"
)

type PerformanceOptimizer struct {
    config *OptimizationConfig
    logger *logrus.Logger
}

type OptimizationConfig struct {
    // Memory settings
    MemoryLimit        int64         `mapstructure:"memory_limit"`        // GOMEMLIMIT
    GCPercent          int           `mapstructure:"gc_percent"`          // GOGC
    MaxProcs           int           `mapstructure:"max_procs"`           // GOMAXPROCS
    
    // Buffer pool settings
    BufferPoolSize     int           `mapstructure:"buffer_pool_size"`
    BufferSize         int           `mapstructure:"buffer_size"`
    
    // Network optimizations
    TCPNoDelay         bool          `mapstructure:"tcp_nodelay"`
    TCPQuickAck        bool          `mapstructure:"tcp_quickack"`
    SO_REUSEPORT       bool          `mapstructure:"so_reuseport"`
    
    // CPU affinity
    CPUAffinity        []int         `mapstructure:"cpu_affinity"`
    
    // Profile collection
    EnableProfiling    bool          `mapstructure:"enable_profiling"`
    ProfilePath        string        `mapstructure:"profile_path"`
}

func (po *PerformanceOptimizer) Apply() error {
    // Set runtime parameters
    if po.config.MemoryLimit > 0 {
        debug.SetMemoryLimit(po.config.MemoryLimit)
        po.logger.Infof("Set memory limit to %d bytes", po.config.MemoryLimit)
    }
    
    if po.config.GCPercent > 0 {
        debug.SetGCPercent(po.config.GCPercent)
        po.logger.Infof("Set GC percent to %d", po.config.GCPercent)
    }
    
    if po.config.MaxProcs > 0 {
        runtime.GOMAXPROCS(po.config.MaxProcs)
        po.logger.Infof("Set GOMAXPROCS to %d", po.config.MaxProcs)
    }
    
    // Apply CPU affinity if specified
    if len(po.config.CPUAffinity) > 0 {
        if err := po.setCPUAffinity(po.config.CPUAffinity); err != nil {
            return fmt.Errorf("failed to set CPU affinity: %w", err)
        }
    }
    
    // Start profiling if enabled
    if po.config.EnableProfiling {
        po.startProfiling()
    }
    
    return nil
}

// Network optimization for Linux
func (po *PerformanceOptimizer) OptimizeNetwork(conn net.Conn) error {
    tcpConn, ok := conn.(*net.TCPConn)
    if !ok {
        return nil
    }
    
    rawConn, err := tcpConn.SyscallConn()
    if err != nil {
        return err
    }
    
    return rawConn.Control(func(fd uintptr) {
        // TCP_NODELAY - disable Nagle's algorithm
        if po.config.TCPNoDelay {
            syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
        }
        
        // TCP_QUICKACK - send ACKs immediately
        if po.config.TCPQuickAck && runtime.GOOS == "linux" {
            syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, 12, 1) // TCP_QUICKACK = 12
        }
        
        // SO_REUSEPORT - allow multiple sockets on same port
        if po.config.SO_REUSEPORT && runtime.GOOS == "linux" {
            syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, 15, 1) // SO_REUSEPORT = 15
        }
        
        // Set socket buffer sizes
        syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4*1024*1024) // 4MB
        syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, 4*1024*1024) // 4MB
    })
}

// Buffer pool optimization
type OptimizedBufferPool struct {
    pools map[int]*sync.Pool
    sizes []int
}

func NewOptimizedBufferPool() *OptimizedBufferPool {
    sizes := []int{
        1 << 10,  // 1KB
        4 << 10,  // 4KB
        16 << 10, // 16KB
        64 << 10, // 64KB
        256 << 10, // 256KB
        1 << 20,  // 1MB
        4 << 20,  // 4MB
    }
    
    bp := &OptimizedBufferPool{
        pools: make(map[int]*sync.Pool),
        sizes: sizes,
    }
    
    for _, size := range sizes {
        size := size // Capture loop variable
        bp.pools[size] = &sync.Pool{
            New: func() interface{} {
                return make([]byte, size)
            },
        }
    }
    
    return bp
}

func (bp *OptimizedBufferPool) Get(size int) []byte {
    // Find the smallest pool that fits
    for _, poolSize := range bp.sizes {
        if poolSize >= size {
            buf := bp.pools[poolSize].Get().([]byte)
            return buf[:size]
        }
    }
    
    // Fallback for large sizes
    return make([]byte, size)
}

func (bp *OptimizedBufferPool) Put(buf []byte) {
    size := cap(buf)
    
    // Find matching pool
    for _, poolSize := range bp.sizes {
        if poolSize == size {
            bp.pools[poolSize].Put(buf)
            return
        }
    }
    
    // No matching pool, let GC handle it
}
```

### 3. Security Hardening
```go
// internal/security/hardening.go
package security

import (
    "crypto/tls"
    "fmt"
    "net/http"
    "time"
    
    "golang.org/x/crypto/acme/autocert"
    "golang.org/x/time/rate"
)

type SecurityConfig struct {
    TLS            TLSConfig            `mapstructure:"tls"`
    RateLimit      RateLimitConfig      `mapstructure:"rate_limit"`
    Headers        HeadersConfig        `mapstructure:"headers"`
    Authentication AuthConfig           `mapstructure:"authentication"`
    Encryption     EncryptionConfig     `mapstructure:"encryption"`
}

type TLSConfig struct {
    MinVersion     string               `mapstructure:"min_version"`
    CipherSuites   []string             `mapstructure:"cipher_suites"`
    AutoCert       bool                 `mapstructure:"auto_cert"`
    CertDomains    []string             `mapstructure:"cert_domains"`
    HSTS           HSTSConfig           `mapstructure:"hsts"`
}

type HSTSConfig struct {
    MaxAge            int              `mapstructure:"max_age"`
    IncludeSubDomains bool             `mapstructure:"include_subdomains"`
    Preload           bool             `mapstructure:"preload"`
}

func NewSecureServer(config *SecurityConfig) (*http.Server, error) {
    // Configure TLS
    tlsConfig := &tls.Config{
        MinVersion: parseTLSVersion(config.TLS.MinVersion),
        CipherSuites: parseCipherSuites(config.TLS.CipherSuites),
        PreferServerCipherSuites: true,
        CurvePreferences: []tls.CurveID{
            tls.X25519,
            tls.CurveP256,
        },
    }
    
    // Auto-cert with Let's Encrypt
    if config.TLS.AutoCert {
        certManager := autocert.Manager{
            Prompt:     autocert.AcceptTOS,
            HostPolicy: autocert.HostWhitelist(config.TLS.CertDomains...),
            Cache:      autocert.DirCache("/etc/mirror/certs"),
        }
        
        tlsConfig.GetCertificate = certManager.GetCertificate
        tlsConfig.NextProtos = append(tlsConfig.NextProtos, acme.ALPNProto)
    }
    
    return &http.Server{
        TLSConfig:         tlsConfig,
        ReadTimeout:       30 * time.Second,
        WriteTimeout:      30 * time.Second,
        IdleTimeout:       120 * time.Second,
        ReadHeaderTimeout: 10 * time.Second,
        MaxHeaderBytes:    1 << 20, // 1MB
    }, nil
}

// Security middleware
func SecurityMiddleware(config *SecurityConfig) func(http.Handler) http.Handler {
    // Rate limiters per IP
    limiters := make(map[string]*rate.Limiter)
    var mu sync.Mutex
    
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Apply security headers
            applySecurityHeaders(w, config.Headers)
            
            // Rate limiting
            ip := getClientIP(r)
            mu.Lock()
            limiter, exists := limiters[ip]
            if !exists {
                limiter = rate.NewLimiter(
                    rate.Limit(config.RateLimit.RequestsPerSecond),
                    config.RateLimit.Burst,
                )
                limiters[ip] = limiter
            }
            mu.Unlock()
            
            if !limiter.Allow() {
                http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
                return
            }
            
            // CSRF protection for state-changing methods
            if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
                if !validateCSRFToken(r) {
                    http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
                    return
                }
            }
            
            next.ServeHTTP(w, r)
        })
    }
}

func applySecurityHeaders(w http.ResponseWriter, config HeadersConfig) {
    // HSTS
    if config.HSTS.MaxAge > 0 {
        hstsValue := fmt.Sprintf("max-age=%d", config.HSTS.MaxAge)
        if config.HSTS.IncludeSubDomains {
            hstsValue += "; includeSubDomains"
        }
        if config.HSTS.Preload {
            hstsValue += "; preload"
        }
        w.Header().Set("Strict-Transport-Security", hstsValue)
    }
    
    // Content Security Policy
    w.Header().Set("Content-Security-Policy", config.CSP)
    
    // Other security headers
    w.Header().Set("X-Content-Type-Options", "nosniff")
    w.Header().Set("X-Frame-Options", "DENY")
    w.Header().Set("X-XSS-Protection", "1; mode=block")
    w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
    w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
}

// Input validation
type Validator struct {
    rules map[string]ValidationRule
}

type ValidationRule struct {
    Type        string
    Required    bool
    MinLength   int
    MaxLength   int
    Pattern     string
    Sanitize    func(string) string
}

func (v *Validator) ValidateStreamID(id string) error {
    if len(id) == 0 {
        return fmt.Errorf("stream ID required")
    }
    
    if len(id) > 64 {
        return fmt.Errorf("stream ID too long")
    }
    
    // Allow only alphanumeric and dash
    if !regexp.MustCompile(`^[a-zA-Z0-9-]+$`).MatchString(id) {
        return fmt.Errorf("invalid stream ID format")
    }
    
    return nil
}

// Secrets management
type SecretsManager struct {
    kmsClient *kms.Client
    cache     map[string]*Secret
    mu        sync.RWMutex
}

type Secret struct {
    Value     string
    ExpiresAt time.Time
}

func (sm *SecretsManager) GetSecret(ctx context.Context, name string) (string, error) {
    // Check cache
    sm.mu.RLock()
    if secret, exists := sm.cache[name]; exists && time.Now().Before(secret.ExpiresAt) {
        sm.mu.RUnlock()
        return secret.Value, nil
    }
    sm.mu.RUnlock()
    
    // Fetch from KMS
    result, err := sm.kmsClient.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
        SecretId: aws.String(name),
    })
    if err != nil {
        return "", fmt.Errorf("failed to get secret: %w", err)
    }
    
    // Cache the secret
    sm.mu.Lock()
    sm.cache[name] = &Secret{
        Value:     *result.SecretString,
        ExpiresAt: time.Now().Add(5 * time.Minute),
    }
    sm.mu.Unlock()
    
    return *result.SecretString, nil
}
```

### 4. Production Deployment (Terraform)
```hcl
# deployment/terraform/modules/compute/ecs.tf
resource "aws_ecs_cluster" "mirror" {
  name = "${var.environment}-mirror-cluster"
  
  setting {
    name  = "containerInsights"
    value = "enabled"
  }
  
  configuration {
    execute_command_configuration {
      kms_key_id = aws_kms_key.ecs.id
      logging    = "OVERRIDE"
      
      log_configuration {
        cloud_watch_encryption_enabled = true
        cloud_watch_log_group_name     = aws_cloudwatch_log_group.ecs.name
      }
    }
  }
}

# GPU-enabled task definition
resource "aws_ecs_task_definition" "mirror_gpu" {
  family                   = "${var.environment}-mirror-gpu"
  network_mode             = "awsvpc"
  requires_compatibilities = ["EC2"]
  cpu                      = "8192"
  memory                   = "32768"
  
  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }
  
  container_definitions = jsonencode([
    {
      name  = "mirror"
      image = "${var.ecr_repository_url}:${var.image_tag}"
      
      cpu    = 8192
      memory = 32768
      
      # GPU requirements
      linuxParameters = {
        devices = [
          {
            hostPath      = "/dev/nvidia0"
            containerPath = "/dev/nvidia0"
            permissions   = ["read", "write", "mknod"]
          }
        ]
      }
      
      resourceRequirements = [
        {
          type  = "GPU"
          value = "1"
        }
      ]
      
      environment = [
        {
          name  = "NVIDIA_VISIBLE_DEVICES"
          value = "all"
        },
        {
          name  = "NVIDIA_DRIVER_CAPABILITIES"
          value = "compute,utility,video"
        }
      ]
      
      secrets = [
        {
          name      = "JWT_SECRET"
          valueFrom = aws_secretsmanager_secret.jwt_secret.arn
        },
        {
          name      = "S3_ACCESS_KEY"
          valueFrom = "${aws_secretsmanager_secret.s3_credentials.arn}:access_key::"
        },
        {
          name      = "S3_SECRET_KEY"
          valueFrom = "${aws_secretsmanager_secret.s3_credentials.arn}:secret_key::"
        }
      ]
      
      portMappings = [
        {
          containerPort = 443
          protocol      = "udp"  # HTTP/3
        },
        {
          containerPort = 6000
          protocol      = "udp"  # SRT
        },
        {
          containerPort = 5004
          protocol      = "udp"  # RTP
        }
      ]
      
      healthCheck = {
        command     = ["CMD-SHELL", "wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }
      
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.mirror.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "mirror"
        }
      }
    }
  ])
}

# Auto-scaling for GPU instances
resource "aws_autoscaling_group" "gpu_instances" {
  name                = "${var.environment}-mirror-gpu-asg"
  vpc_zone_identifier = var.private_subnet_ids
  min_size            = var.min_gpu_instances
  max_size            = var.max_gpu_instances
  desired_capacity    = var.desired_gpu_instances
  
  launch_template {
    id      = aws_launch_template.gpu_instance.id
    version = "$Latest"
  }
  
  tag {
    key                 = "Name"
    value               = "${var.environment}-mirror-gpu"
    propagate_at_launch = true
  }
  
  tag {
    key                 = "AmazonECSManaged"
    value               = ""
    propagate_at_launch = true
  }
}

resource "aws_launch_template" "gpu_instance" {
  name_prefix   = "${var.environment}-mirror-gpu-"
  image_id      = data.aws_ami.ecs_gpu_optimized.id
  instance_type = var.gpu_instance_type # g5.2xlarge
  
  iam_instance_profile {
    arn = aws_iam_instance_profile.ecs_instance.arn
  }
  
  vpc_security_group_ids = [aws_security_group.ecs_instances.id]
  
  block_device_mappings {
    device_name = "/dev/xvda"
    
    ebs {
      volume_size           = 100
      volume_type           = "gp3"
      iops                  = 3000
      throughput            = 125
      delete_on_termination = true
      encrypted             = true
      kms_key_id            = aws_kms_key.ebs.id
    }
  }
  
  user_data = base64encode(templatefile("${path.module}/templates/user_data.sh", {
    cluster_name = aws_ecs_cluster.mirror.name
    region       = var.aws_region
  }))
  
  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
  }
  
  monitoring {
    enabled = true
  }
}

# Application Load Balancer for HTTPS/WebSocket
resource "aws_lb" "mirror" {
  name               = "${var.environment}-mirror-alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.public_subnet_ids
  
  enable_deletion_protection = var.environment == "production"
  enable_http2               = true
  enable_waf_fail_open       = true
  
  access_logs {
    bucket  = aws_s3_bucket.alb_logs.bucket
    prefix  = "mirror-alb"
    enabled = true
  }
}

# Network Load Balancer for UDP (SRT/RTP)
resource "aws_lb" "mirror_udp" {
  name               = "${var.environment}-mirror-nlb"
  internal           = false
  load_balancer_type = "network"
  subnets            = var.public_subnet_ids
  
  enable_deletion_protection = var.environment == "production"
  enable_cross_zone_load_balancing = true
}

# CloudFront distribution
resource "aws_cloudfront_distribution" "mirror" {
  enabled             = true
  is_ipv6_enabled     = true
  comment             = "${var.environment} Mirror Streaming CDN"
  default_root_object = ""
  price_class         = var.cloudfront_price_class
  
  origin {
    domain_name = aws_lb.mirror.dns_name
    origin_id   = "mirror-alb"
    
    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }
  
  default_cache_behavior {
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "mirror-alb"
    
    forwarded_values {
      query_string = true
      headers      = ["Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"]
      
      cookies {
        forward = "none"
      }
    }
    
    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 0
    max_ttl                = 31536000
    compress               = true
  }
  
  # Cache behavior for segments
  ordered_cache_behavior {
    path_pattern     = "*.m4s"
    allowed_methods  = ["GET", "HEAD"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "mirror-alb"
    
    forwarded_values {
      query_string = false
      headers      = []
      
      cookies {
        forward = "none"
      }
    }
    
    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 86400
    max_ttl                = 31536000
    compress               = false
  }
  
  restrictions {
    geo_restriction {
      restriction_type = var.geo_restriction_type
      locations        = var.geo_restriction_locations
    }
  }
  
  viewer_certificate {
    cloudfront_default_certificate = var.custom_domain == null
    acm_certificate_arn            = var.acm_certificate_arn
    ssl_support_method             = "sni-only"
    minimum_protocol_version       = "TLSv1.2_2021"
  }
  
  logging_config {
    include_cookies = false
    bucket          = aws_s3_bucket.cloudfront_logs.bucket_domain_name
    prefix          = "cf-logs/"
  }
  
  web_acl_id = aws_wafv2_web_acl.mirror.arn
}
```

### 5. Monitoring and Alerting
```yaml
# deployment/kubernetes/base/monitoring/prometheus-rules.yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: mirror-alerts
  namespace: mirror
spec:
  groups:
    - name: stream.alerts
      interval: 30s
      rules:
        - alert: StreamDown
          expr: |
            stream_status{status="error"} > 0
          for: 1m
          labels:
            severity: critical
            service: mirror
          annotations:
            summary: "Stream {{ $labels.stream_id }} is down"
            description: "Stream {{ $labels.stream_id }} has been in error state for more than 1 minute"
            runbook_url: "https://wiki.example.com/runbooks/stream-down"
            
        - alert: HighStreamLatency
          expr: |
            histogram_quantile(0.95, rate(stream_segment_latency_bucket[5m])) > 1
          for: 5m
          labels:
            severity: warning
            service: mirror
          annotations:
            summary: "High latency for stream {{ $labels.stream_id }}"
            description: "95th percentile latency is above 1 second"
            
    - name: gpu.alerts
      interval: 30s
      rules:
        - alert: GPUHighTemperature
          expr: |
            gpu_temperature_celsius > 85
          for: 2m
          labels:
            severity: critical
            service: mirror
          annotations:
            summary: "GPU {{ $labels.gpu_index }} temperature critical"
            description: "GPU temperature is {{ $value }}°C"
            
        - alert: GPUMemoryExhausted
          expr: |
            (gpu_memory_used_bytes / gpu_memory_total_bytes) > 0.95
          for: 5m
          labels:
            severity: warning
            service: mirror
          annotations:
            summary: "GPU {{ $labels.gpu_index }} memory almost full"
            description: "GPU memory usage is {{ $value | humanizePercentage }}"
            
    - name: system.alerts
      interval: 30s
      rules:
        - alert: HighCPUUsage
          expr: |
            (100 - (avg by (instance) (irate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)) > 80
          for: 10m
          labels:
            severity: warning
            service: mirror
          annotations:
            summary: "High CPU usage on {{ $labels.instance }}"
            description: "CPU usage is {{ $value }}%"
            
        - alert: DiskSpaceLow
          expr: |
            (node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"}) < 0.10
          for: 5m
          labels:
            severity: warning
            service: mirror
          annotations:
            summary: "Low disk space on {{ $labels.instance }}"
            description: "Only {{ $value | humanizePercentage }} disk space remaining"
            
        - alert: NetworkSaturation
          expr: |
            rate(node_network_receive_bytes_total[5m]) > 1e9
          for: 5m
          labels:
            severity: warning
            service: mirror
          annotations:
            summary: "High network ingress on {{ $labels.instance }}"
            description: "Network receiving {{ $value | humanize }}B/s"

# deployment/kubernetes/base/monitoring/grafana-dashboard.json
{
  "dashboard": {
    "title": "Mirror Streaming Platform",
    "panels": [
      {
        "title": "Active Streams",
        "targets": [
          {
            "expr": "count(stream_status{status=\"active\"})"
          }
        ],
        "type": "stat"
      },
      {
        "title": "Total Viewers",
        "targets": [
          {
            "expr": "sum(stream_viewer_count)"
          }
        ],
        "type": "stat"
      },
      {
        "title": "Stream Bitrate",
        "targets": [
          {
            "expr": "stream_bitrate_bps"
          }
        ],
        "type": "graph"
      },
      {
        "title": "End-to-End Latency",
        "targets": [
          {
            "expr": "histogram_quantile(0.95, rate(stream_e2e_latency_bucket[5m]))"
          }
        ],
        "type": "graph"
      },
      {
        "title": "GPU Utilization",
        "targets": [
          {
            "expr": "gpu_utilization_percent"
          }
        ],
        "type": "graph"
      },
      {
        "title": "CDN Cache Hit Rate",
        "targets": [
          {
            "expr": "rate(cdn_cache_hits_total[5m]) / rate(cdn_requests_total[5m])"
          }
        ],
        "type": "graph"
      }
    ]
  }
}
```

### 6. Operational Runbooks
```markdown
# docs/operations/runbooks/stream-down.md

# Stream Down Runbook

## Alert
**Name**: StreamDown  
**Severity**: Critical  
**Team**: Streaming Operations

## Description
This alert fires when a stream remains in error state for more than 1 minute.

## Impact
- Viewers cannot watch the affected stream
- Recording/DVR may be interrupted
- Revenue impact if paid content

## Diagnosis Steps

1. **Check stream status**
   ```bash
   curl -s https://api.mirror.example.com/api/v1/streams/${STREAM_ID} | jq .
   ```

2. **Check ingestion logs**
   ```bash
   kubectl logs -n mirror -l app=mirror,component=ingestion --tail=100 | grep ${STREAM_ID}
   ```

3. **Verify source connectivity**
   ```bash
   # For SRT
   srt-live-transmit srt://source.example.com:6000 file://con | head -c 1000
   
   # For RTP
   ffprobe -v error -show_entries stream=codec_type -of default=noprint_wrappers=1 rtp://source.example.com:5004
   ```

4. **Check GPU status**
   ```bash
   kubectl exec -n mirror deployment/mirror-gpu -- nvidia-smi
   ```

5. **Review metrics**
   - Grafana dashboard: https://grafana.mirror.example.com/d/mirror-streams
   - Look for correlated issues (CPU, memory, network)

## Resolution Steps

### Quick Fixes

1. **Restart stream**
   ```bash
   mirror-cli stream restart ${STREAM_ID}
   ```

2. **Restart specific component**
   ```bash
   # Ingestion only
   kubectl rollout restart -n mirror deployment/mirror-ingestion
   
   # Transcoding only
   kubectl rollout restart -n mirror deployment/mirror-transcoding
   ```

### Root Cause Analysis

1. **Source Issues**
   - Contact content provider
   - Check network path: `mtr -r -c 100 source.example.com`
   - Verify firewall rules

2. **GPU Issues**
   - Check GPU errors: `dmesg | grep -i nvidia`
   - Reset GPU: `nvidia-smi -r -i ${GPU_ID}`
   - Drain node if persistent: `kubectl drain node-gpu-${N}`

3. **Resource Exhaustion**
   - Scale up: `kubectl scale -n mirror deployment/mirror-gpu --replicas=+1`
   - Check resource limits
   - Review recent deployments

## Escalation
1. Try quick fixes (5 minutes)
2. Page on-call engineer if unresolved (10 minutes)
3. Engage vendor support if infrastructure issue (30 minutes)

## Prevention
- Monitor source reliability metrics
- Set up predictive alerts for resource usage
- Regular failover testing
- Capacity planning reviews

# docs/operations/runbooks/high-latency.md

# High Latency Runbook

## Alert
**Name**: HighStreamLatency  
**Severity**: Warning  
**Team**: Streaming Operations

## Description
This alert fires when the 95th percentile latency exceeds 1 second for 5 minutes.

## Impact
- Poor viewer experience
- Increased buffering
- Competitive disadvantage for live events

## Diagnosis Steps

1. **Identify affected streams**
   ```bash
   curl -s https://api.mirror.example.com/api/v1/metrics/streams | \
     jq '.streams[] | select(.latency_p95 > 1000)'
   ```

2. **Check component latencies**
   ```promql
   # Ingestion latency
   histogram_quantile(0.95, rate(ingestion_latency_bucket[5m]))
   
   # Transcoding latency
   histogram_quantile(0.95, rate(transcoding_latency_bucket[5m]))
   
   # Packaging latency
   histogram_quantile(0.95, rate(packaging_latency_bucket[5m]))
   ```

3. **Review CDN performance**
   ```bash
   # Check origin response times
   aws cloudwatch get-metric-statistics \
     --namespace AWS/CloudFront \
     --metric-name OriginLatency \
     --dimensions Name=DistributionId,Value=${DISTRIBUTION_ID} \
     --start-time $(date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%S) \
     --end-time $(date -u +%Y-%m-%dT%H:%M:%S) \
     --period 300 \
     --statistics Average
   ```

## Resolution Steps

1. **Optimize packaging**
   ```bash
   # Reduce segment duration
   kubectl set env -n mirror deployment/mirror-packaging \
     SEGMENT_DURATION=300ms
   
   # Increase chunk preloading
   kubectl set env -n mirror deployment/mirror-packaging \
     PRELOAD_CHUNKS=3
   ```

2. **Scale transcoding**
   ```bash
   # Add more GPU workers
   kubectl scale -n mirror deployment/mirror-gpu --replicas=+2
   ```

3. **CDN optimization**
   - Enable more aggressive caching
   - Add edge locations
   - Review origin shield configuration

## Prevention
- Regular performance testing
- Gradual rollouts with canary deployments
- Automated performance regression detection
```

### 7. Production Configuration
```yaml
# configs/production.yaml
# Production-optimized configuration for Mirror streaming platform

server:
  http3_port: 443
  tls_cert_file: "/etc/mirror/certs/cert.pem"
  tls_key_file: "/etc/mirror/certs/key.pem"
  max_incoming_streams: 10000
  max_incoming_uni_streams: 5000
  max_idle_timeout: 60s
  
redis:
  addresses:
    - "redis-cluster-0.redis.svc.cluster.local:6379"
    - "redis-cluster-1.redis.svc.cluster.local:6379"
    - "redis-cluster-2.redis.svc.cluster.local:6379"
  password: "${REDIS_PASSWORD}"
  pool_size: 200
  min_idle_conns: 50
  
ingestion:
  srt:
    max_bandwidth: 75000000      # 75 Mbps headroom
    input_bandwidth: 60000000    # 60 Mbps expected
    latency: 100ms               # Reduced for production
    fc_window: 32768             # Increased for high bitrate
    max_connections: 50          # Support growth
    
  buffer:
    ring_size: 8388608           # 8MB per stream
    pool_size: 50                # Pre-allocate for 50 streams
    
transcoding:
  gpu:
    devices: [0, 1, 2, 3]        # 4 GPUs in production
    max_encoders_per_gpu: 20     # A10G can handle more
    memory_reserve: 2048         # 2GB reserve
    
  pipeline:
    max_concurrent: 50           # Support 50 streams
    worker_count: 8              # More workers
    
  output:
    video_preset: "p5"           # Highest quality preset
    keyframe_interval: 30        # 1s at 30fps for better seeking
    
packaging:
  cmaf:
    segment_duration: 400ms      # Slightly lower for production
    chunk_duration: 80ms         # Finer granularity
    
  cache:
    max_size: 53687091200        # 50GB cache
    mmap_enabled: true
    
  delivery:
    http3_enabled: true
    push_enabled: true
    max_push_streams: 200
    
storage:
  s3:
    upload_concurrency: 20       # More parallel uploads
    part_size: 10485760          # 10MB parts
    
cdn:
  cloudfront:
    price_class: "PriceClass_All" # Use all edge locations
    
  failover:
    health_check_interval: 15s   # More frequent checks
    unhealthy_threshold: 2       # Faster failover
    
dvr:
  max_duration: 6h               # 6 hour DVR window
  storage_class: "INTELLIGENT_TIERING"
  
alerting:
  evaluation_interval: 15s       # Faster alert detection
  
# Performance optimizations
optimization:
  memory_limit: 128849018880     # 120GB
  gc_percent: 50                 # Less aggressive GC
  max_procs: 32                  # Use all cores
  tcp_nodelay: true
  tcp_quickack: true
  so_reuseport: true
  enable_profiling: true
  profile_path: "/var/log/mirror/profiles"
  
# Security hardening
security:
  tls:
    min_version: "TLS1.3"
    cipher_suites:
      - "TLS_AES_128_GCM_SHA256"
      - "TLS_AES_256_GCM_SHA384"
      - "TLS_CHACHA20_POLY1305_SHA256"
    auto_cert: true
    cert_domains:
      - "streaming.example.com"
      - "api.streaming.example.com"
      
  rate_limit:
    requests_per_second: 100
    burst: 200
    
  headers:
    hsts:
      max_age: 31536000
      include_subdomains: true
      preload: true
    csp: "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self'; connect-src 'self' wss: https:; media-src 'self' blob:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self';"
```

## Testing Requirements

### Load Testing
- 5,000 concurrent viewers sustained for 1 hour
- 25 concurrent 50Mbps HEVC streams
- Viewer surge from 0 to 5,000 in 5 minutes
- Multi-region failover under load
- 6 concurrent streams per viewer

### Security Testing
- Penetration testing by third party
- OWASP Top 10 vulnerability scanning
- DDoS simulation (100Gbps attack)
- Authentication bypass attempts
- Input fuzzing on all endpoints

### Performance Testing
- End-to-end latency <800ms at p95
- Zero dropped frames under normal load
- CDN cache hit ratio >95%
- API response time <100ms at p99
- Dashboard load time <2 seconds

## Monitoring and Alerting

### Key Metrics
- Stream health and status
- End-to-end latency percentiles
- GPU utilization and temperature
- Network throughput and errors
- Storage usage and growth rate
- API request rates and errors
- Viewer connection statistics

### Alert Policies
- Critical: Stream down, GPU failure, storage full
- Warning: High latency, resource usage >80%
- Info: New deployment, configuration change

## Deliverables
1. Complete load testing suite with K6
2. Performance optimization implementation
3. Security hardening configuration
4. Terraform infrastructure as code
5. Kubernetes manifests with GitOps
6. Operational runbooks
7. Monitoring dashboards
8. Automated deployment pipeline
9. Disaster recovery procedures

## Success Criteria
- Support 25 streams + 5,000 viewers confirmed
- End-to-end latency <1 second at p95
- 99.9% uptime over 30 days
- Zero security vulnerabilities (critical/high)
- Automated deployment with <5 min rollback
- Complete monitoring coverage
- All runbooks tested and validated
- Team trained on operations

## Production Readiness Checklist
- [ ] Load testing passed all scenarios
- [ ] Security audit completed
- [ ] Performance targets met
- [ ] Infrastructure provisioned
- [ ] Monitoring configured
- [ ] Alerts tested
- [ ] Runbooks validated
- [ ] Team training completed
- [ ] Deployment automation working
- [ ] Backup/restore tested
- [ ] Disaster recovery plan executed
- [ ] Documentation complete
- [ ] Support contracts in place
- [ ] On-call rotation established
- [ ] Go-live plan approved
