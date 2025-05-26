package tests

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/config"
)

// SetupTestRedis creates a test Redis instance using miniredis
func SetupTestRedis(t *testing.T) *redis.Client {
	t.Helper()

	// Create miniredis server
	mr, err := miniredis.Run()
	require.NoError(t, err)

	// Register cleanup
	t.Cleanup(func() {
		mr.Close()
	})

	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	// Verify connection
	ctx := context.Background()
	err = client.Ping(ctx).Err()
	require.NoError(t, err)

	return client
}

// VerboseLogger provides detailed test output
type VerboseLogger struct {
	t      *testing.T
	prefix string
	mu     sync.Mutex
}

func NewVerboseLogger(t *testing.T, prefix string) *VerboseLogger {
	return &VerboseLogger{t: t, prefix: prefix}
}

func (v *VerboseLogger) Log(format string, args ...interface{}) {
	v.mu.Lock()
	defer v.mu.Unlock()

	timestamp := time.Now().Format("15:04:05.000")
	message := fmt.Sprintf(format, args...)
	v.t.Logf("[%s] %s: %s", timestamp, v.prefix, message)
}

func (v *VerboseLogger) LogJSON(title string, data interface{}) {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		v.Log("❌ Failed to marshal JSON for %s: %v", title, err)
		return
	}
	v.Log("📋 %s:\n%s", title, string(jsonData))
}

// TestEnvironment holds all test infrastructure
type TestEnvironment struct {
	t               *testing.T
	Redis           *redis.Client
	MinRedis        *miniredis.Miniredis
	TempDir         string
	Config          *config.Config
	HTTPClient      *http.Client // HTTPS client
	HTTPClientPlain *http.Client // HTTP client
	verbose         *VerboseLogger
}

// CreateTestHTTPClient creates an HTTP client for testing
func CreateTestHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // For self-signed test certs
			},
		},
		Timeout: 10 * time.Second,
	}
}

// CleanupRedis clears all data in Redis
func CleanupRedis(t *testing.T, client *redis.Client) {
	t.Helper()

	ctx := context.Background()
	err := client.FlushAll(ctx).Err()
	require.NoError(t, err)
}

// SetupTestEnvironment creates a complete test environment
func SetupTestEnvironment(t *testing.T, verbose *VerboseLogger) *TestEnvironment {
	if verbose != nil {
		verbose.Log("🔧 Setting up test environment...")
	}

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "mirror-integration-test-*")
	require.NoError(t, err)

	if verbose != nil {
		verbose.Log("📁 Created temp directory: %s", tempDir)
	}

	// Set up test Redis
	mr, err := miniredis.Run()
	require.NoError(t, err)

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	// Verify Redis connection
	ctx := context.Background()
	err = redisClient.Ping(ctx).Err()
	require.NoError(t, err)

	if verbose != nil {
		verbose.Log("🗄️ Redis test instance: %s", mr.Addr())
	}

	// Create test configuration
	cfg := CreateTestConfig(t, tempDir, mr.Addr())

	// DISABLE all logging to prevent interference with dashboard
	cfg.Logging.Level = "fatal"      // Only show fatal level logs (effectively silent)
	cfg.Logging.Output = "/dev/null" // Send to /dev/null

	// Set up HTTP client for API calls (HTTPS)
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // For self-signed test certs
			},
		},
		Timeout: 10 * time.Second,
	}

	// Set up HTTP client for HTTP (non-TLS) calls
	httpClientPlain := &http.Client{
		Timeout: 10 * time.Second,
	}

	env := &TestEnvironment{
		t:               t,
		Redis:           redisClient,
		MinRedis:        mr,
		TempDir:         tempDir,
		Config:          cfg,
		HTTPClient:      httpClient,
		HTTPClientPlain: httpClientPlain,
		verbose:         verbose,
	}

	if verbose != nil {
		verbose.Log("✅ Test environment setup complete")
	}
	return env
}

// Cleanup cleans up the test environment
func (env *TestEnvironment) Cleanup() {
	if env.verbose != nil {
		env.verbose.Log("🧹 Cleaning up test environment...")
	}

	// Clear Redis state before closing
	if env.Redis != nil {
		CleanupRedis(env.t, env.Redis)
		env.Redis.Close()
	}
	if env.MinRedis != nil {
		env.MinRedis.Close()
	}
	if env.TempDir != "" {
		os.RemoveAll(env.TempDir)
		if env.verbose != nil {
			env.verbose.Log("🗑️ Removed temp directory: %s", env.TempDir)
		}
	}

	if env.verbose != nil {
		env.verbose.Log("✅ Test environment cleanup complete")
	}
}

// ClearRedisState clears all Redis state during test run
func (env *TestEnvironment) ClearRedisState() {
	if env.Redis != nil {
		CleanupRedis(env.t, env.Redis)
		if env.verbose != nil {
			env.verbose.Log("🗑️ Cleared Redis state")
		}
	}
}

// getAvailablePort finds an available port for testing
func getAvailablePort() (int, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// CreateTestConfig creates a test configuration
func CreateTestConfig(t *testing.T, tempDir, redisAddr string) *config.Config {
	// Generate test certificates if they don't exist
	certDir := filepath.Join(tempDir, "certs")
	require.NoError(t, os.MkdirAll(certDir, 0755))

	certFile := filepath.Join(certDir, "cert.pem")
	keyFile := filepath.Join(certDir, "key.pem")

	// Generate self-signed certificate for testing
	GenerateTestCertificate(t, certFile, keyFile)

	// Get available ports for SRT and RTP
	srtPort, err := getAvailablePort()
	require.NoError(t, err)
	
	rtpPort, err := getAvailablePort()
	require.NoError(t, err)
	
	rtcpPort, err := getAvailablePort()
	require.NoError(t, err)

	return &config.Config{
		Server: config.ServerConfig{
			HTTP3Port:             8443,
			HTTPPort:              8080,
			EnableHTTP:            true,
			EnableHTTP2:           true,
			TLSCertFile:           certFile,
			TLSKeyFile:            keyFile,
			MaxIncomingStreams:    1000,
			MaxIncomingUniStreams: 500,
			MaxIdleTimeout:        60 * time.Second,
			ReadTimeout:           60 * time.Second,
			WriteTimeout:          60 * time.Second,
			ShutdownTimeout:       5 * time.Second,
			DebugEndpoints:        true,
		},
		Redis: config.RedisConfig{
			Addresses: []string{redisAddr},
			Password:  "",
			DB:        0,
		},
		Logging: config.LoggingConfig{
			Level:      "info",
			Format:     "json",
			Output:     "stdout",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     28,
		},
		Metrics: config.MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
			Port:    9091,
		},
		Ingestion: config.IngestionConfig{
			SRT: config.SRTConfig{
				Enabled:        true,
				ListenAddr:     "127.0.0.1",
				Port:           srtPort,
				MaxConnections: 25,
				MaxBandwidth:   52428800,
			},
			RTP: config.RTPConfig{
				Enabled:     true,
				ListenAddr:  "127.0.0.1",
				Port:        rtpPort,
				RTCPPort:    rtcpPort,
				MaxSessions: 25,
			},
			Buffer: config.BufferConfig{
				PoolSize: 50,
				RingSize: 4194304,
			},
			Memory: config.MemoryConfig{
				MaxTotal:     8589934592,
				MaxPerStream: 419430400,
			},
			StreamHandling: config.StreamHandlingConfig{
				FrameAssemblyTimeout: 200 * time.Millisecond,
				GOPBufferSize:        3,
				ErrorRetryLimit:      3,
			},
			Backpressure: config.BackpressureConfig{
				Enabled:        true,
				HighWatermark:  0.75,
				LowWatermark:   0.25,
			},
			Registry: config.RegistryConfig{
				RedisAddr:           redisAddr,
				RedisPassword:       "",
				RedisDB:             0,
				TTL:                 30 * time.Minute,
				HeartbeatInterval:   5 * time.Second,
				HeartbeatTimeout:    15 * time.Second,
				CleanupInterval:     60 * time.Second,
				MaxStreamsPerSource: 10,
			},
		},
	}
}

// GenerateTestCertificate generates a self-signed certificate for testing
func GenerateTestCertificate(t *testing.T, certFile, keyFile string) {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"Test"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"Test"},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{"localhost"},
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	// Write certificate file
	certOut, err := os.Create(certFile)
	require.NoError(t, err)
	defer certOut.Close()

	err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	require.NoError(t, err)

	// Write private key file
	keyOut, err := os.Create(keyFile)
	require.NoError(t, err)
	defer keyOut.Close()

	privKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)

	err = pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privKeyDER})
	require.NoError(t, err)
}
