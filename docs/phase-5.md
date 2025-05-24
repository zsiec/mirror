# Phase 5: Storage, CDN Integration, and DVR

## Overview
This phase completes the storage and content delivery infrastructure by implementing S3 lifecycle policies, CloudFront CDN configuration, DVR functionality with intelligent retention, and multi-region failover capabilities. We'll build a robust system that handles both live streaming and time-shifted viewing.

## Goals
- Implement intelligent S3 storage tiers with lifecycle transitions
- Configure CloudFront CDN with optimal caching strategies
- Build DVR recording system with configurable retention
- Create multi-region failover with health checks
- Implement viewer analytics and access logging
- Add content protection with signed URLs
- Build storage cost optimization system

## New Components Structure
```
internal/
├── cdn/
│   ├── cloudfront/
│   │   ├── distribution.go     # CloudFront distribution management
│   │   ├── behaviors.go        # Cache behavior configuration
│   │   ├── invalidation.go     # Cache invalidation logic
│   │   └── signing.go          # URL signing for protected content
│   ├── failover/
│   │   ├── health.go           # Origin health checks
│   │   ├── routing.go          # Failover routing logic
│   │   └── sync.go             # Cross-region sync
│   └── analytics/
│       ├── collector.go        # Analytics data collection
│       └── processor.go        # Log processing
├── storage/
│   ├── lifecycle/
│   │   ├── policy.go           # S3 lifecycle policies
│   │   ├── transitions.go      # Storage class transitions
│   │   └── optimizer.go        # Cost optimization
│   ├── dvr/
│   │   ├── recorder.go         # DVR recording implementation
│   │   ├── indexer.go          # DVR content indexing
│   │   ├── retention.go        # Retention policy enforcement
│   │   └── playback.go         # DVR playback API
│   └── replication/
│       ├── cross_region.go     # Cross-region replication
│       └── consistency.go      # Consistency management
```

## Implementation Details

### 1. Updated Configuration
```go
// internal/config/config.go (additions)
type Config struct {
    // ... existing fields ...
    CDN         CDNConfig         `mapstructure:"cdn"`
    DVR         DVRConfig         `mapstructure:"dvr"`
    Replication ReplicationConfig `mapstructure:"replication"`
}

type CDNConfig struct {
    CloudFront   CloudFrontConfig   `mapstructure:"cloudfront"`
    Failover     FailoverConfig     `mapstructure:"failover"`
    Analytics    AnalyticsConfig    `mapstructure:"analytics"`
    Protection   ProtectionConfig   `mapstructure:"protection"`
}

type CloudFrontConfig struct {
    DistributionID      string            `mapstructure:"distribution_id"`
    DomainName          string            `mapstructure:"domain_name"`
    OriginID            string            `mapstructure:"origin_id"`
    PriceClass          string            `mapstructure:"price_class"`
    CacheBehaviors      []CacheBehavior   `mapstructure:"cache_behaviors"`
    CustomErrorPages    []ErrorPage       `mapstructure:"custom_error_pages"`
    LoggingEnabled      bool              `mapstructure:"logging_enabled"`
    LogBucket           string            `mapstructure:"log_bucket"`
    LogPrefix           string            `mapstructure:"log_prefix"`
}

type CacheBehavior struct {
    PathPattern         string            `mapstructure:"path_pattern"`
    TargetOriginID      string            `mapstructure:"target_origin_id"`
    ViewerProtocolPolicy string           `mapstructure:"viewer_protocol_policy"`
    CachePolicyID       string            `mapstructure:"cache_policy_id"`
    TTL                 TTLConfig         `mapstructure:"ttl"`
    Compress            bool              `mapstructure:"compress"`
    SmoothStreaming     bool              `mapstructure:"smooth_streaming"`
}

type TTLConfig struct {
    Default             int64             `mapstructure:"default"`
    Min                 int64             `mapstructure:"min"`
    Max                 int64             `mapstructure:"max"`
}

type FailoverConfig struct {
    Enabled             bool              `mapstructure:"enabled"`
    HealthCheckInterval time.Duration     `mapstructure:"health_check_interval"`
    HealthCheckTimeout  time.Duration     `mapstructure:"health_check_timeout"`
    UnhealthyThreshold  int               `mapstructure:"unhealthy_threshold"`
    HealthyThreshold    int               `mapstructure:"healthy_threshold"`
    Regions             []RegionConfig    `mapstructure:"regions"`
}

type RegionConfig struct {
    Region              string            `mapstructure:"region"`
    Priority            int               `mapstructure:"priority"`
    Endpoint            string            `mapstructure:"endpoint"`
    Bucket              string            `mapstructure:"bucket"`
}

type ProtectionConfig struct {
    SignedURLs          bool              `mapstructure:"signed_urls"`
    KeyPairID           string            `mapstructure:"key_pair_id"`
    PrivateKeyPath      string            `mapstructure:"private_key_path"`
    URLExpiration       time.Duration     `mapstructure:"url_expiration"`
    IPWhitelist         []string          `mapstructure:"ip_whitelist"`
    GeoRestriction      GeoRestrictionConfig `mapstructure:"geo_restriction"`
}

type GeoRestrictionConfig struct {
    RestrictionType     string            `mapstructure:"restriction_type"` // whitelist or blacklist
    Locations           []string          `mapstructure:"locations"`        // Country codes
}

type DVRConfig struct {
    Enabled             bool              `mapstructure:"enabled"`
    MaxDuration         time.Duration     `mapstructure:"max_duration"`      // Max DVR window
    SegmentRetention    time.Duration     `mapstructure:"segment_retention"` // How long to keep segments
    IndexUpdateInterval time.Duration     `mapstructure:"index_update_interval"`
    StorageClass        string            `mapstructure:"storage_class"`
    CompressionEnabled  bool              `mapstructure:"compression_enabled"`
    Encryption          EncryptionConfig  `mapstructure:"encryption"`
}

type EncryptionConfig struct {
    Enabled             bool              `mapstructure:"enabled"`
    Algorithm           string            `mapstructure:"algorithm"`         // AES-256
    KeyRotationInterval time.Duration     `mapstructure:"key_rotation_interval"`
    KMSKeyID            string            `mapstructure:"kms_key_id"`
}

type ReplicationConfig struct {
    Enabled             bool              `mapstructure:"enabled"`
    Regions             []string          `mapstructure:"regions"`
    ReplicationRole     string            `mapstructure:"replication_role"`
    Rules               []ReplicationRule `mapstructure:"rules"`
}

type ReplicationRule struct {
    ID                  string            `mapstructure:"id"`
    Priority            int               `mapstructure:"priority"`
    Prefix              string            `mapstructure:"prefix"`
    DestinationBucket   string            `mapstructure:"destination_bucket"`
    StorageClass        string            `mapstructure:"storage_class"`
}
```

### 2. S3 Lifecycle Management
```go
// internal/storage/lifecycle/policy.go
package lifecycle

import (
    "context"
    "fmt"
    "time"
    
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/aws/aws-sdk-go-v2/service/s3/types"
    "github.com/sirupsen/logrus"
)

type LifecycleManager struct {
    s3Client    *s3.Client
    config      *StorageConfig
    logger      *logrus.Logger
}

func NewLifecycleManager(s3Client *s3.Client, config *StorageConfig, logger *logrus.Logger) *LifecycleManager {
    return &LifecycleManager{
        s3Client: s3Client,
        config:   config,
        logger:   logger,
    }
}

func (l *LifecycleManager) SetupLifecycleRules(bucket string) error {
    rules := []types.LifecycleRule{
        // Live segments - transition to IA after 1 hour
        {
            ID:     aws.String("live-segments-transition"),
            Status: types.ExpirationStatusEnabled,
            Filter: &types.LifecycleRuleFilter{
                Prefix: aws.String("live/"),
            },
            Transitions: []types.Transition{
                {
                    Days:         aws.Int32(0),
                    StorageClass: types.TransitionStorageClassStandardIa,
                },
            },
            NoncurrentVersionTransitions: []types.NoncurrentVersionTransition{
                {
                    NoncurrentDays: aws.Int32(1),
                    StorageClass:   types.TransitionStorageClassGlacierIr,
                },
            },
            AbortIncompleteMultipartUpload: &types.AbortIncompleteMultipartUpload{
                DaysAfterInitiation: aws.Int32(1),
            },
        },
        
        // DVR segments - intelligent tiering
        {
            ID:     aws.String("dvr-intelligent-tiering"),
            Status: types.ExpirationStatusEnabled,
            Filter: &types.LifecycleRuleFilter{
                Prefix: aws.String("dvr/"),
            },
            Transitions: []types.Transition{
                {
                    Days:         aws.Int32(1),
                    StorageClass: types.TransitionStorageClassIntelligentTiering,
                },
            },
            Expiration: &types.LifecycleExpiration{
                Days: aws.Int32(30), // Delete after 30 days
            },
        },
        
        // Logs - compress and archive
        {
            ID:     aws.String("logs-archive"),
            Status: types.ExpirationStatusEnabled,
            Filter: &types.LifecycleRuleFilter{
                Prefix: aws.String("logs/"),
            },
            Transitions: []types.Transition{
                {
                    Days:         aws.Int32(7),
                    StorageClass: types.TransitionStorageClassGlacierIr,
                },
                {
                    Days:         aws.Int32(30),
                    StorageClass: types.TransitionStorageClassDeepArchive,
                },
            },
            Expiration: &types.LifecycleExpiration{
                Days: aws.Int32(365), // Keep logs for 1 year
            },
        },
    }
    
    input := &s3.PutBucketLifecycleConfigurationInput{
        Bucket: aws.String(bucket),
        LifecycleConfiguration: &types.BucketLifecycleConfiguration{
            Rules: rules,
        },
    }
    
    _, err := l.s3Client.PutBucketLifecycleConfiguration(context.TODO(), input)
    if err != nil {
        return fmt.Errorf("failed to set lifecycle rules: %w", err)
    }
    
    l.logger.Infof("Set lifecycle rules for bucket %s", bucket)
    
    return nil
}

// internal/storage/lifecycle/optimizer.go
package lifecycle

import (
    "context"
    "fmt"
    "sync"
    "time"
    
    "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

type StorageOptimizer struct {
    s3Client         *s3.Client
    cloudwatchClient *cloudwatch.Client
    logger           *logrus.Logger
    
    metrics          sync.Map // bucket -> *BucketMetrics
}

type BucketMetrics struct {
    TotalSize        int64
    ObjectCount      int64
    StorageClassDist map[string]int64
    AccessPatterns   map[string]*AccessPattern
    LastUpdated      time.Time
}

type AccessPattern struct {
    ObjectKey        string
    AccessCount      int64
    LastAccessed     time.Time
    AverageSize      int64
}

func (o *StorageOptimizer) AnalyzeAndOptimize(ctx context.Context, bucket string) error {
    // Get bucket metrics
    metrics, err := o.collectBucketMetrics(ctx, bucket)
    if err != nil {
        return fmt.Errorf("failed to collect metrics: %w", err)
    }
    
    // Analyze access patterns
    recommendations := o.analyzeAccessPatterns(metrics)
    
    // Apply optimizations
    for _, rec := range recommendations {
        switch rec.Type {
        case "transition":
            o.transitionObjects(ctx, bucket, rec)
        case "delete":
            o.deleteObjects(ctx, bucket, rec)
        case "compress":
            o.scheduleCompression(ctx, bucket, rec)
        }
    }
    
    // Update metrics
    o.metrics.Store(bucket, metrics)
    
    return nil
}

func (o *StorageOptimizer) collectBucketMetrics(ctx context.Context, bucket string) (*BucketMetrics, error) {
    metrics := &BucketMetrics{
        StorageClassDist: make(map[string]int64),
        AccessPatterns:   make(map[string]*AccessPattern),
        LastUpdated:      time.Now(),
    }
    
    // List objects with storage class info
    paginator := s3.NewListObjectsV2Paginator(o.s3Client, &s3.ListObjectsV2Input{
        Bucket: aws.String(bucket),
    })
    
    for paginator.HasMorePages() {
        page, err := paginator.NextPage(ctx)
        if err != nil {
            return nil, err
        }
        
        for _, obj := range page.Contents {
            metrics.TotalSize += obj.Size
            metrics.ObjectCount++
            
            // Track storage class distribution
            storageClass := string(obj.StorageClass)
            if storageClass == "" {
                storageClass = "STANDARD"
            }
            metrics.StorageClassDist[storageClass]++
            
            // Analyze access patterns from CloudWatch
            if pattern := o.getAccessPattern(ctx, bucket, *obj.Key); pattern != nil {
                metrics.AccessPatterns[*obj.Key] = pattern
            }
        }
    }
    
    return metrics, nil
}

type Recommendation struct {
    Type        string   // transition, delete, compress
    Objects     []string
    TargetClass string
    Reason      string
}

func (o *StorageOptimizer) analyzeAccessPatterns(metrics *BucketMetrics) []Recommendation {
    var recommendations []Recommendation
    
    // Analyze each object's access pattern
    for key, pattern := range metrics.AccessPatterns {
        daysSinceAccess := time.Since(pattern.LastAccessed).Hours() / 24
        
        // Recommend transition for infrequently accessed objects
        if daysSinceAccess > 30 && pattern.AccessCount < 5 {
            recommendations = append(recommendations, Recommendation{
                Type:        "transition",
                Objects:     []string{key},
                TargetClass: "GLACIER_IR",
                Reason:      fmt.Sprintf("Not accessed for %.0f days", daysSinceAccess),
            })
        }
        
        // Recommend deletion for very old objects
        if daysSinceAccess > 365 {
            recommendations = append(recommendations, Recommendation{
                Type:    "delete",
                Objects: []string{key},
                Reason:  "Object older than retention policy",
            })
        }
    }
    
    return recommendations
}
```

### 3. CloudFront CDN Integration
```go
// internal/cdn/cloudfront/distribution.go
package cloudfront

import (
    "context"
    "crypto/rsa"
    "crypto/x509"
    "encoding/pem"
    "fmt"
    "io/ioutil"
    "time"
    
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/cloudfront"
    "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
    "github.com/sirupsen/logrus"
)

type DistributionManager struct {
    cfClient     *cloudfront.Client
    config       *CloudFrontConfig
    privateKey   *rsa.PrivateKey
    logger       *logrus.Logger
}

func NewDistributionManager(cfClient *cloudfront.Client, config *CloudFrontConfig, logger *logrus.Logger) (*DistributionManager, error) {
    dm := &DistributionManager{
        cfClient: cfClient,
        config:   config,
        logger:   logger,
    }
    
    // Load private key for signed URLs if enabled
    if config.Protection.SignedURLs {
        key, err := dm.loadPrivateKey(config.Protection.PrivateKeyPath)
        if err != nil {
            return nil, fmt.Errorf("failed to load private key: %w", err)
        }
        dm.privateKey = key
    }
    
    return dm, nil
}

func (d *DistributionManager) CreateDistribution(originDomain string) (*types.Distribution, error) {
    // Create cache behaviors
    defaultCacheBehavior := d.createDefaultCacheBehavior()
    cacheBehaviors := d.createCacheBehaviors()
    
    // Create origin
    origin := types.Origin{
        Id:         aws.String(d.config.OriginID),
        DomainName: aws.String(originDomain),
        CustomOriginConfig: &types.CustomOriginConfig{
            HTTPPort:             aws.Int32(80),
            HTTPSPort:            aws.Int32(443),
            OriginProtocolPolicy: types.OriginProtocolPolicyHttpsOnly,
            OriginSslProtocols: &types.OriginSslProtocols{
                Items:    []types.SslProtocol{types.SslProtocolTLSv12},
                Quantity: aws.Int32(1),
            },
        },
    }
    
    // Create distribution config
    distributionConfig := &types.DistributionConfig{
        CallerReference: aws.String(fmt.Sprintf("mirror-%d", time.Now().Unix())),
        Comment:         aws.String("Mirror streaming CDN distribution"),
        Enabled:         aws.Bool(true),
        PriceClass:      types.PriceClass(d.config.PriceClass),
        
        Origins: &types.Origins{
            Items:    []types.Origin{origin},
            Quantity: aws.Int32(1),
        },
        
        DefaultCacheBehavior: defaultCacheBehavior,
        CacheBehaviors:       cacheBehaviors,
        
        ViewerCertificate: &types.ViewerCertificate{
            CloudFrontDefaultCertificate: aws.Bool(true),
            MinimumProtocolVersion:       types.MinimumProtocolVersionTLSv12,
        },
        
        HttpVersion: types.HttpVersionHttp2and3,
        IsIPV6Enabled: aws.Bool(true),
    }
    
    // Add logging if enabled
    if d.config.LoggingEnabled {
        distributionConfig.Logging = &types.LoggingConfig{
            Enabled:        aws.Bool(true),
            Bucket:         aws.String(d.config.LogBucket),
            Prefix:         aws.String(d.config.LogPrefix),
            IncludeCookies: aws.Bool(false),
        }
    }
    
    // Add geo restriction if configured
    if d.config.Protection.GeoRestriction.RestrictionType != "" {
        distributionConfig.Restrictions = &types.Restrictions{
            GeoRestriction: &types.GeoRestriction{
                RestrictionType: types.GeoRestrictionType(d.config.Protection.GeoRestriction.RestrictionType),
                Quantity:        aws.Int32(int32(len(d.config.Protection.GeoRestriction.Locations))),
                Items:           d.config.Protection.GeoRestriction.Locations,
            },
        }
    }
    
    // Create distribution
    result, err := d.cfClient.CreateDistribution(context.TODO(), &cloudfront.CreateDistributionInput{
        DistributionConfig: distributionConfig,
    })
    
    if err != nil {
        return nil, fmt.Errorf("failed to create distribution: %w", err)
    }
    
    d.logger.Infof("Created CloudFront distribution: %s", *result.Distribution.Id)
    
    return result.Distribution, nil
}

func (d *DistributionManager) createDefaultCacheBehavior() *types.DefaultCacheBehavior {
    return &types.DefaultCacheBehavior{
        TargetOriginId: aws.String(d.config.OriginID),
        ViewerProtocolPolicy: types.ViewerProtocolPolicyRedirectToHttps,
        
        AllowedMethods: &types.AllowedMethods{
            Items: []types.Method{
                types.MethodGet,
                types.MethodHead,
                types.MethodOptions,
            },
            Quantity: aws.Int32(3),
            CachedMethods: &types.CachedMethods{
                Items: []types.Method{
                    types.MethodGet,
                    types.MethodHead,
                },
                Quantity: aws.Int32(2),
            },
        },
        
        Compress: aws.Bool(true),
        
        // Use managed cache policy for LL-HLS
        CachePolicyId: aws.String("658327ea-f89d-4fab-a63d-7e88639e58f6"), // Managed-Elemental-MediaPackage
        
        // Enable smooth streaming for HLS
        SmoothStreaming: aws.Bool(true),
        
        // Trusted signers for signed URLs
        TrustedSigners: &types.TrustedSigners{
            Enabled:  aws.Bool(d.config.Protection.SignedURLs),
            Quantity: aws.Int32(1),
            Items:    []string{"self"},
        },
    }
}

func (d *DistributionManager) createCacheBehaviors() *types.CacheBehaviors {
    behaviors := []types.CacheBehavior{
        // Playlist files - no cache
        {
            PathPattern:          aws.String("*.m3u8"),
            TargetOriginId:       aws.String(d.config.OriginID),
            ViewerProtocolPolicy: types.ViewerProtocolPolicyRedirectToHttps,
            
            CachePolicyId: aws.String("4135ea2d-6df8-44a3-9df3-4b5a84be39ad"), // Managed-CachingDisabled
            
            AllowedMethods: &types.AllowedMethods{
                Items:    []types.Method{types.MethodGet, types.MethodHead},
                Quantity: aws.Int32(2),
            },
            
            Compress: aws.Bool(true),
        },
        
        // Segment files - long cache
        {
            PathPattern:          aws.String("*.m4s"),
            TargetOriginId:       aws.String(d.config.OriginID),
            ViewerProtocolPolicy: types.ViewerProtocolPolicyRedirectToHttps,
            
            CachePolicyId: aws.String("658327ea-f89d-4fab-a63d-7e88639e58f6"), // Managed-Elemental-MediaPackage
            
            AllowedMethods: &types.AllowedMethods{
                Items:    []types.Method{types.MethodGet, types.MethodHead},
                Quantity: aws.Int32(2),
            },
            
            Compress: aws.Bool(false), // Don't compress video
        },
        
        // Init segments - long cache
        {
            PathPattern:          aws.String("*.mp4"),
            TargetOriginId:       aws.String(d.config.OriginID),
            ViewerProtocolPolicy: types.ViewerProtocolPolicyRedirectToHttps,
            
            CachePolicyId: aws.String("658327ea-f89d-4fab-a63d-7e88639e58f6"),
            
            AllowedMethods: &types.AllowedMethods{
                Items:    []types.Method{types.MethodGet, types.MethodHead},
                Quantity: aws.Int32(2),
            },
            
            Compress: aws.Bool(false),
        },
    }
    
    return &types.CacheBehaviors{
        Items:    behaviors,
        Quantity: aws.Int32(int32(len(behaviors))),
    }
}

// internal/cdn/cloudfront/signing.go
package cloudfront

import (
    "crypto"
    "crypto/rand"
    "crypto/rsa"
    "crypto/sha1"
    "encoding/base64"
    "fmt"
    "net/url"
    "strings"
    "time"
)

func (d *DistributionManager) CreateSignedURL(resourceURL string, expiration time.Time) (string, error) {
    if d.privateKey == nil {
        return "", fmt.Errorf("private key not loaded")
    }
    
    // Create policy
    policy := &Policy{
        Statement: []Statement{
            {
                Resource: resourceURL,
                Condition: Condition{
                    DateLessThan: DateLessThan{
                        EpochTime: expiration.Unix(),
                    },
                },
            },
        },
    }
    
    policyJSON, err := json.Marshal(policy)
    if err != nil {
        return "", fmt.Errorf("failed to marshal policy: %w", err)
    }
    
    // Base64 encode policy
    encodedPolicy := base64.StdEncoding.EncodeToString(policyJSON)
    encodedPolicy = strings.ReplaceAll(encodedPolicy, "+", "-")
    encodedPolicy = strings.ReplaceAll(encodedPolicy, "=", "_")
    encodedPolicy = strings.ReplaceAll(encodedPolicy, "/", "~")
    
    // Sign policy
    h := sha1.New()
    h.Write(policyJSON)
    digest := h.Sum(nil)
    
    signature, err := rsa.SignPKCS1v15(rand.Reader, d.privateKey, crypto.SHA1, digest)
    if err != nil {
        return "", fmt.Errorf("failed to sign policy: %w", err)
    }
    
    // Base64 encode signature
    encodedSignature := base64.StdEncoding.EncodeToString(signature)
    encodedSignature = strings.ReplaceAll(encodedSignature, "+", "-")
    encodedSignature = strings.ReplaceAll(encodedSignature, "=", "_")
    encodedSignature = strings.ReplaceAll(encodedSignature, "/", "~")
    
    // Build signed URL
    u, err := url.Parse(resourceURL)
    if err != nil {
        return "", fmt.Errorf("failed to parse URL: %w", err)
    }
    
    q := u.Query()
    q.Set("Policy", encodedPolicy)
    q.Set("Signature", encodedSignature)
    q.Set("Key-Pair-Id", d.config.Protection.KeyPairID)
    u.RawQuery = q.Encode()
    
    return u.String(), nil
}
```

### 4. DVR Implementation
```go
// internal/storage/dvr/recorder.go
package dvr

import (
    "context"
    "fmt"
    "path/filepath"
    "sync"
    "time"
    
    "github.com/sirupsen/logrus"
)

type Recorder struct {
    config        *DVRConfig
    s3Client      *s3.Client
    indexer       *Indexer
    retentionMgr  *RetentionManager
    
    recordings    sync.Map // streamID -> *Recording
    logger        *logrus.Logger
    
    ctx           context.Context
    cancel        context.CancelFunc
}

type Recording struct {
    StreamID      string
    StartTime     time.Time
    EndTime       time.Time
    Segments      []*SegmentRecord
    Status        RecordingStatus
    
    mu            sync.RWMutex
}

type SegmentRecord struct {
    Number        uint32
    Timestamp     time.Time
    Duration      time.Duration
    S3Key         string
    Size          int64
    Encrypted     bool
}

type RecordingStatus string

const (
    RecordingActive   RecordingStatus = "active"
    RecordingComplete RecordingStatus = "complete"
    RecordingFailed   RecordingStatus = "failed"
)

func NewRecorder(config *DVRConfig, s3Client *s3.Client, logger *logrus.Logger) *Recorder {
    ctx, cancel := context.WithCancel(context.Background())
    
    return &Recorder{
        config:       config,
        s3Client:     s3Client,
        indexer:      NewIndexer(s3Client, logger),
        retentionMgr: NewRetentionManager(config, s3Client, logger),
        logger:       logger,
        ctx:          ctx,
        cancel:       cancel,
    }
}

func (r *Recorder) StartRecording(streamID string) error {
    if _, exists := r.recordings.Load(streamID); exists {
        return fmt.Errorf("recording already active for stream %s", streamID)
    }
    
    recording := &Recording{
        StreamID:  streamID,
        StartTime: time.Now(),
        Status:    RecordingActive,
        Segments:  make([]*SegmentRecord, 0),
    }
    
    r.recordings.Store(streamID, recording)
    
    r.logger.Infof("Started DVR recording for stream %s", streamID)
    
    return nil
}

func (r *Recorder) RecordSegment(streamID string, segment *cmaf.Segment) error {
    value, exists := r.recordings.Load(streamID)
    if !exists {
        return fmt.Errorf("no active recording for stream %s", streamID)
    }
    
    recording := value.(*Recording)
    
    // Check DVR window
    if time.Since(recording.StartTime) > r.config.MaxDuration {
        // Trim old segments
        r.trimRecording(recording)
    }
    
    // Generate S3 key for DVR storage
    s3Key := r.generateS3Key(streamID, segment)
    
    // Prepare segment data
    segmentData := segment.Data
    
    // Encrypt if enabled
    if r.config.Encryption.Enabled {
        encrypted, err := r.encryptSegment(segmentData)
        if err != nil {
            return fmt.Errorf("failed to encrypt segment: %w", err)
        }
        segmentData = encrypted
    }
    
    // Compress if enabled
    if r.config.CompressionEnabled {
        compressed, err := r.compressSegment(segmentData)
        if err != nil {
            return fmt.Errorf("failed to compress segment: %w", err)
        }
        segmentData = compressed
    }
    
    // Upload to S3 with appropriate storage class
    putInput := &s3.PutObjectInput{
        Bucket:       aws.String(r.config.Bucket),
        Key:          aws.String(s3Key),
        Body:         bytes.NewReader(segmentData),
        StorageClass: types.StorageClass(r.config.StorageClass),
        Metadata: map[string]string{
            "stream-id":    streamID,
            "segment-num":  fmt.Sprintf("%d", segment.Number),
            "duration":     fmt.Sprintf("%.3f", segment.Duration.Seconds()),
            "timestamp":    segment.Timestamp.Format(time.RFC3339),
            "encrypted":    fmt.Sprintf("%t", r.config.Encryption.Enabled),
            "compressed":   fmt.Sprintf("%t", r.config.CompressionEnabled),
        },
    }
    
    // Add encryption headers if using KMS
    if r.config.Encryption.Enabled && r.config.Encryption.KMSKeyID != "" {
        putInput.ServerSideEncryption = types.ServerSideEncryptionAwsKms
        putInput.SSEKMSKeyId = aws.String(r.config.Encryption.KMSKeyID)
    }
    
    _, err := r.s3Client.PutObject(r.ctx, putInput)
    if err != nil {
        return fmt.Errorf("failed to upload DVR segment: %w", err)
    }
    
    // Update recording
    recording.mu.Lock()
    recording.Segments = append(recording.Segments, &SegmentRecord{
        Number:    segment.Number,
        Timestamp: segment.Timestamp,
        Duration:  segment.Duration,
        S3Key:     s3Key,
        Size:      int64(len(segmentData)),
        Encrypted: r.config.Encryption.Enabled,
    })
    recording.mu.Unlock()
    
    // Update index
    r.indexer.UpdateIndex(recording)
    
    return nil
}

func (r *Recorder) generateS3Key(streamID string, segment *cmaf.Segment) string {
    t := segment.Timestamp
    return fmt.Sprintf("dvr/%s/%04d/%02d/%02d/%02d/segment_%d.m4s",
        streamID,
        t.Year(),
        t.Month(),
        t.Day(),
        t.Hour(),
        segment.Number,
    )
}

func (r *Recorder) trimRecording(recording *Recording) {
    recording.mu.Lock()
    defer recording.mu.Unlock()
    
    cutoffTime := time.Now().Add(-r.config.MaxDuration)
    
    // Find segments to keep
    keepIndex := 0
    for i, seg := range recording.Segments {
        if seg.Timestamp.After(cutoffTime) {
            keepIndex = i
            break
        }
    }
    
    // Remove old segments from recording
    if keepIndex > 0 {
        recording.Segments = recording.Segments[keepIndex:]
    }
}

// internal/storage/dvr/indexer.go
package dvr

import (
    "encoding/json"
    "fmt"
    "sync"
    "time"
)

type Indexer struct {
    s3Client    *s3.Client
    indices     sync.Map // streamID -> *Index
    logger      *logrus.Logger
}

type Index struct {
    StreamID       string                 `json:"stream_id"`
    StartTime      time.Time              `json:"start_time"`
    EndTime        time.Time              `json:"end_time"`
    Duration       time.Duration          `json:"duration"`
    SegmentCount   int                    `json:"segment_count"`
    TimeRanges     []TimeRange            `json:"time_ranges"`
    Manifest       string                 `json:"manifest"`
    LastUpdated    time.Time              `json:"last_updated"`
}

type TimeRange struct {
    Start          time.Time              `json:"start"`
    End            time.Time              `json:"end"`
    FirstSegment   uint32                 `json:"first_segment"`
    LastSegment    uint32                 `json:"last_segment"`
    S3Prefix       string                 `json:"s3_prefix"`
}

func (i *Indexer) UpdateIndex(recording *Recording) error {
    recording.mu.RLock()
    defer recording.mu.RUnlock()
    
    if len(recording.Segments) == 0 {
        return nil
    }
    
    index := &Index{
        StreamID:     recording.StreamID,
        StartTime:    recording.StartTime,
        EndTime:      recording.Segments[len(recording.Segments)-1].Timestamp,
        Duration:     recording.Segments[len(recording.Segments)-1].Timestamp.Sub(recording.StartTime),
        SegmentCount: len(recording.Segments),
        LastUpdated:  time.Now(),
    }
    
    // Build time ranges for efficient seeking
    var currentRange *TimeRange
    for _, seg := range recording.Segments {
        t := seg.Timestamp
        hourKey := fmt.Sprintf("%04d/%02d/%02d/%02d", t.Year(), t.Month(), t.Day(), t.Hour())
        
        if currentRange == nil || currentRange.S3Prefix != hourKey {
            if currentRange != nil {
                index.TimeRanges = append(index.TimeRanges, *currentRange)
            }
            currentRange = &TimeRange{
                Start:        seg.Timestamp,
                FirstSegment: seg.Number,
                S3Prefix:     hourKey,
            }
        }
        
        currentRange.End = seg.Timestamp.Add(seg.Duration)
        currentRange.LastSegment = seg.Number
    }
    
    if currentRange != nil {
        index.TimeRanges = append(index.TimeRanges, *currentRange)
    }
    
    // Generate manifest
    index.Manifest = i.generateManifest(recording)
    
    // Store in memory
    i.indices.Store(recording.StreamID, index)
    
    // Upload index to S3
    indexData, err := json.MarshalIndent(index, "", "  ")
    if err != nil {
        return fmt.Errorf("failed to marshal index: %w", err)
    }
    
    indexKey := fmt.Sprintf("dvr/%s/index.json", recording.StreamID)
    _, err = i.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
        Bucket:       aws.String(i.config.Bucket),
        Key:          aws.String(indexKey),
        Body:         bytes.NewReader(indexData),
        ContentType:  aws.String("application/json"),
    })
    
    if err != nil {
        return fmt.Errorf("failed to upload index: %w", err)
    }
    
    return nil
}

func (i *Indexer) generateManifest(recording *Recording) string {
    // Generate HLS manifest for DVR playback
    var manifest strings.Builder
    
    manifest.WriteString("#EXTM3U\n")
    manifest.WriteString("#EXT-X-VERSION:7\n")
    manifest.WriteString("#EXT-X-TARGETDURATION:1\n")
    manifest.WriteString("#EXT-X-PLAYLIST-TYPE:EVENT\n")
    manifest.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", recording.Segments[0].Number))
    
    for _, seg := range recording.Segments {
        manifest.WriteString(fmt.Sprintf("#EXTINF:%.3f,\n", seg.Duration.Seconds()))
        manifest.WriteString(fmt.Sprintf("%s\n", seg.S3Key))
    }
    
    manifest.WriteString("#EXT-X-ENDLIST\n")
    
    return manifest.String()
}
```

### 5. Multi-Region Failover
```go
// internal/cdn/failover/health.go
package failover

import (
    "context"
    "fmt"
    "net/http"
    "sync"
    "time"
    
    "github.com/sirupsen/logrus"
)

type HealthChecker struct {
    config      *FailoverConfig
    regions     map[string]*RegionHealth
    httpClient  *http.Client
    logger      *logrus.Logger
    
    mu          sync.RWMutex
    ctx         context.Context
    cancel      context.CancelFunc
}

type RegionHealth struct {
    Region          string
    Endpoint        string
    Healthy         bool
    LastCheck       time.Time
    ConsecutiveFails int
    ResponseTime    time.Duration
}

func NewHealthChecker(config *FailoverConfig, logger *logrus.Logger) *HealthChecker {
    ctx, cancel := context.WithCancel(context.Background())
    
    hc := &HealthChecker{
        config:  config,
        regions: make(map[string]*RegionHealth),
        httpClient: &http.Client{
            Timeout: config.HealthCheckTimeout,
        },
        logger:  logger,
        ctx:     ctx,
        cancel:  cancel,
    }
    
    // Initialize regions
    for _, region := range config.Regions {
        hc.regions[region.Region] = &RegionHealth{
            Region:   region.Region,
            Endpoint: region.Endpoint,
            Healthy:  true,
        }
    }
    
    return hc
}

func (h *HealthChecker) Start() {
    ticker := time.NewTicker(h.config.HealthCheckInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-h.ctx.Done():
            return
        case <-ticker.C:
            h.checkAllRegions()
        }
    }
}

func (h *HealthChecker) checkAllRegions() {
    var wg sync.WaitGroup
    
    for _, region := range h.regions {
        wg.Add(1)
        go func(r *RegionHealth) {
            defer wg.Done()
            h.checkRegion(r)
        }(region)
    }
    
    wg.Wait()
}

func (h *HealthChecker) checkRegion(region *RegionHealth) {
    start := time.Now()
    
    // Perform health check
    healthURL := fmt.Sprintf("%s/health", region.Endpoint)
    resp, err := h.httpClient.Get(healthURL)
    
    region.LastCheck = time.Now()
    region.ResponseTime = time.Since(start)
    
    if err != nil || resp.StatusCode != http.StatusOK {
        h.handleUnhealthy(region)
    } else {
        h.handleHealthy(region)
    }
    
    if resp != nil {
        resp.Body.Close()
    }
}

func (h *HealthChecker) handleUnhealthy(region *RegionHealth) {
    h.mu.Lock()
    defer h.mu.Unlock()
    
    region.ConsecutiveFails++
    
    if region.ConsecutiveFails >= h.config.UnhealthyThreshold && region.Healthy {
        region.Healthy = false
        h.logger.Warnf("Region %s marked unhealthy after %d failed checks",
            region.Region, region.ConsecutiveFails)
        
        // Trigger failover if this was the primary region
        if h.isPrimaryRegion(region.Region) {
            h.triggerFailover()
        }
    }
}

func (h *HealthChecker) handleHealthy(region *RegionHealth) {
    h.mu.Lock()
    defer h.mu.Unlock()
    
    if !region.Healthy && region.ConsecutiveFails == 0 {
        h.logger.Infof("Region %s recovered", region.Region)
    }
    
    region.Healthy = true
    region.ConsecutiveFails = 0
}

func (h *HealthChecker) GetHealthyRegions() []string {
    h.mu.RLock()
    defer h.mu.RUnlock()
    
    var healthy []string
    for _, region := range h.regions {
        if region.Healthy {
            healthy = append(healthy, region.Region)
        }
    }
    
    return healthy
}

// internal/cdn/failover/routing.go
package failover

import (
    "fmt"
    "sort"
    "sync"
)

type FailoverRouter struct {
    config        *FailoverConfig
    healthChecker *HealthChecker
    currentPrimary string
    logger        *logrus.Logger
    
    mu            sync.RWMutex
    listeners     []FailoverListener
}

type FailoverListener interface {
    OnFailover(oldRegion, newRegion string)
}

func (r *FailoverRouter) GetPrimaryRegion() string {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    return r.currentPrimary
}

func (r *FailoverRouter) triggerFailover() {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    oldPrimary := r.currentPrimary
    healthyRegions := r.healthChecker.GetHealthyRegions()
    
    if len(healthyRegions) == 0 {
        r.logger.Error("No healthy regions available!")
        return
    }
    
    // Sort by priority
    sort.Slice(healthyRegions, func(i, j int) bool {
        return r.getRegionPriority(healthyRegions[i]) < r.getRegionPriority(healthyRegions[j])
    })
    
    newPrimary := healthyRegions[0]
    if newPrimary != oldPrimary {
        r.currentPrimary = newPrimary
        r.logger.Warnf("Failing over from %s to %s", oldPrimary, newPrimary)
        
        // Notify listeners
        for _, listener := range r.listeners {
            go listener.OnFailover(oldPrimary, newPrimary)
        }
    }
}

func (r *FailoverRouter) getRegionPriority(region string) int {
    for _, cfg := range r.config.Regions {
        if cfg.Region == region {
            return cfg.Priority
        }
    }
    return 999 // Default low priority
}
```

### 6. Updated Configuration
```yaml
# configs/default.yaml (additions)
cdn:
  cloudfront:
    distribution_id: "${CLOUDFRONT_DISTRIBUTION_ID}"
    domain_name: "streaming.example.com"
    origin_id: "mirror-origin"
    price_class: "PriceClass_100"    # Use only US/Canada/Europe edges
    logging_enabled: true
    log_bucket: "mirror-cf-logs"
    log_prefix: "streaming/"
    
  failover:
    enabled: true
    health_check_interval: 30s
    health_check_timeout: 5s
    unhealthy_threshold: 3
    healthy_threshold: 2
    regions:
      - region: "us-east-1"
        priority: 1
        endpoint: "https://origin-us-east-1.example.com"
        bucket: "mirror-streams-us-east-1"
      - region: "us-west-2"
        priority: 2
        endpoint: "https://origin-us-west-2.example.com"
        bucket: "mirror-streams-us-west-2"
      - region: "eu-west-1"
        priority: 3
        endpoint: "https://origin-eu-west-1.example.com"
        bucket: "mirror-streams-eu-west-1"
        
  protection:
    signed_urls: true
    key_pair_id: "${CLOUDFRONT_KEY_PAIR_ID}"
    private_key_path: "/etc/mirror/cloudfront-private-key.pem"
    url_expiration: 6h
    ip_whitelist: []
    geo_restriction:
      restriction_type: "whitelist"
      locations: ["US", "CA", "GB", "DE", "FR"]
      
  analytics:
    enabled: true
    log_processor: "athena"         # Use AWS Athena for log analysis
    metrics_interval: 5m
    retention_days: 90
    
dvr:
  enabled: true
  max_duration: 4h                  # 4 hour DVR window
  segment_retention: 24h            # Keep segments for 24 hours
  index_update_interval: 1m
  storage_class: "STANDARD_IA"      # Use Infrequent Access for DVR
  compression_enabled: true
  encryption:
    enabled: true
    algorithm: "AES-256"
    key_rotation_interval: 24h
    kms_key_id: "${KMS_KEY_ID}"
    
replication:
  enabled: true
  regions: ["us-west-2", "eu-west-1"]
  replication_role: "${REPLICATION_ROLE_ARN}"
  rules:
    - id: "replicate-live-segments"
      priority: 1
      prefix: "live/"
      destination_bucket: "mirror-streams-replica"
      storage_class: "STANDARD_IA"
    - id: "replicate-dvr-content"
      priority: 2
      prefix: "dvr/"
      destination_bucket: "mirror-dvr-replica"
      storage_class: "GLACIER_IR"
```

## Testing Requirements

### Unit Tests
- S3 lifecycle policy creation
- CloudFront distribution configuration
- DVR segment recording and indexing
- Health check logic
- Failover decision making

### Integration Tests
- End-to-end storage lifecycle transitions
- CloudFront signed URL generation
- DVR playback with seeking
- Multi-region failover simulation
- Cross-region replication verification

### Performance Tests
- S3 upload throughput with concurrency
- CloudFront cache hit ratios
- DVR index query performance
- Failover latency measurement
- Storage cost optimization validation

## Monitoring Metrics

### Prometheus Metrics
```go
// Storage metrics
storage_objects_total{bucket, storage_class}
storage_size_bytes{bucket, storage_class}
storage_transitions_total{from_class, to_class}
storage_cost_dollars{bucket, storage_class}

// CDN metrics
cdn_requests_total{distribution, cache_status}
cdn_bandwidth_bytes{distribution, edge_location}
cdn_origin_requests_total{distribution}
cdn_error_rate{distribution, status_code}

// DVR metrics
dvr_recordings_active{stream_id}
dvr_segments_recorded_total{stream_id}
dvr_storage_bytes{stream_id}
dvr_playback_requests_total{stream_id}

// Failover metrics
failover_health_checks_total{region, status}
failover_response_time_seconds{region}
failover_events_total{from_region, to_region}
failover_active_region{region}
```

## Deliverables
1. Complete S3 lifecycle management system
2. CloudFront distribution with optimal caching
3. DVR recording and playback system
4. Multi-region failover implementation
5. Storage cost optimization engine
6. Signed URL generation for content protection
7. Analytics and monitoring integration
8. Comprehensive test suite

## Success Criteria
- S3 storage costs reduced by 40%+ through lifecycle policies
- CloudFront cache hit ratio >90%
- DVR playback starts within 2 seconds
- Failover completes within 30 seconds
- Zero data loss during failover
- Storage grows <2TB per day for 25 streams
- All security requirements met
- All tests passing

## Next Phase Preview
Phase 6 will implement the admin dashboard and monitoring system, including real-time stream statistics, system health visualization, WebSocket-based updates, and comprehensive alerting.