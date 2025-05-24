# Mirror Streaming Platform - Part 5: Admin API and Monitoring

## Table of Contents
1. [Admin API Overview](#admin-api-overview)
2. [Stream Management Endpoints](#stream-management-endpoints)
3. [System Control Endpoints](#system-control-endpoints)
4. [Monitoring and Metrics](#monitoring-and-metrics)
5. [Admin Dashboard](#admin-dashboard)
6. [WebSocket Real-time Updates](#websocket-real-time-updates)

## Admin API Overview

### internal/api/admin.go
```go
package api

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/gorilla/mux"
    "github.com/gorilla/websocket"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/ingestion"
    "github.com/zsiec/mirror/internal/packaging"
    "github.com/zsiec/mirror/internal/storage"
    "github.com/zsiec/mirror/internal/transcoding"
    "github.com/zsiec/mirror/pkg/models"
    "github.com/zsiec/mirror/pkg/utils"
)

// AdminAPI provides administrative control endpoints
type AdminAPI struct {
    ingestion   *ingestion.Service
    transcoding *transcoding.Service
    packaging   *packaging.Service
    storage     *storage.Service

    // WebSocket upgrader for real-time updates
    upgrader websocket.Upgrader

    // Active WebSocket connections
    wsClients map[*WSClient]bool
    wsMutex   sync.RWMutex

    // Metrics
    apiRequestsTotal *prometheus.CounterVec
    apiDuration      *prometheus.HistogramVec
}

// WSClient represents a WebSocket client connection
type WSClient struct {
    conn   *websocket.Conn
    send   chan []byte
    api    *AdminAPI
}

// NewAdminAPI creates a new admin API instance
func NewAdminAPI(ingestion *ingestion.Service, transcoding *transcoding.Service, 
    packaging *packaging.Service, storage *storage.Service) *AdminAPI {
    
    api := &AdminAPI{
        ingestion:   ingestion,
        transcoding: transcoding,
        packaging:   packaging,
        storage:     storage,
        wsClients:   make(map[*WSClient]bool),
        upgrader: websocket.Upgrader{
            CheckOrigin: func(r *http.Request) bool {
                return true // Allow all origins for admin dashboard
            },
        },
    }

    // Initialize metrics
    api.apiRequestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "mirror_api_requests_total",
            Help: "Total number of API requests",
        },
        []string{"method", "endpoint", "status"},
    )

    api.apiDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "mirror_api_duration_seconds",
            Help:    "API request duration in seconds",
            Buckets: prometheus.DefBuckets,
        },
        []string{"method", "endpoint"},
    )

    prometheus.MustRegister(api.apiRequestsTotal, api.apiDuration)

    return api
}

// RegisterRoutes registers all admin API routes
func (api *AdminAPI) RegisterRoutes(mux *http.ServeMux) {
    r := mux.NewRouter()
    
    // Apply middleware
    r.Use(api.metricsMiddleware)
    r.Use(api.loggingMiddleware)
    r.Use(api.corsMiddleware)

    // Stream management
    r.HandleFunc("/api/v1/streams", api.ListStreams).Methods("GET")
    r.HandleFunc("/api/v1/streams/{id}", api.GetStream).Methods("GET")
    r.HandleFunc("/api/v1/streams/{id}", api.DeleteStream).Methods("DELETE")
    r.HandleFunc("/api/v1/streams/{id}/restart", api.RestartStream).Methods("POST")

    // System control
    r.HandleFunc("/api/v1/system/info", api.GetSystemInfo).Methods("GET")
    r.HandleFunc("/api/v1/system/config", api.GetConfig).Methods("GET")
    r.HandleFunc("/api/v1/system/config", api.UpdateConfig).Methods("PUT")
    
    // Service health
    r.HandleFunc("/api/v1/health", api.GetHealth).Methods("GET")
    r.HandleFunc("/api/v1/health/{service}", api.GetServiceHealth).Methods("GET")

    // Statistics
    r.HandleFunc("/api/v1/stats", api.GetStats).Methods("GET")
    r.HandleFunc("/api/v1/stats/streams/{id}", api.GetStreamStats).Methods("GET")

    // DVR management
    r.HandleFunc("/api/v1/dvr/{id}", api.ListDVRRecordings).Methods("GET")
    r.HandleFunc("/api/v1/dvr/{id}/{timestamp}", api.GetDVRRecording).Methods("GET")

    // WebSocket endpoint for real-time updates
    r.HandleFunc("/api/v1/ws", api.HandleWebSocket)

    // Admin dashboard (static files)
    r.PathPrefix("/admin/").Handler(http.StripPrefix("/admin/", 
        http.FileServer(http.Dir("./web/admin/"))))
    
    // Default to admin dashboard
    r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
    })

    mux.Handle("/", r)
}

// Middleware functions
func (api *AdminAPI) metricsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        
        wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
        next.ServeHTTP(wrapped, r)
        
        duration := time.Since(start).Seconds()
        
        // Record metrics
        api.apiRequestsTotal.WithLabelValues(
            r.Method,
            r.URL.Path,
            http.StatusText(wrapped.statusCode),
        ).Inc()
        
        api.apiDuration.WithLabelValues(
            r.Method,
            r.URL.Path,
        ).Observe(duration)
    })
}

func (api *AdminAPI) loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        
        wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
        next.ServeHTTP(wrapped, r)
        
        log.Info().
            Str("method", r.Method).
            Str("path", r.URL.Path).
            Int("status", wrapped.statusCode).
            Dur("duration", time.Since(start)).
            Msg("API request")
    })
}

func (api *AdminAPI) corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
        
        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }
        
        next.ServeHTTP(w, r)
    })
}

type responseWriter struct {
    http.ResponseWriter
    statusCode int
}

func (w *responseWriter) WriteHeader(code int) {
    w.statusCode = code
    w.ResponseWriter.WriteHeader(code)
}

// Helper functions
func (api *AdminAPI) writeJSON(w http.ResponseWriter, status int, data interface{}) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    
    if err := json.NewEncoder(w).Encode(data); err != nil {
        log.Error().Err(err).Msg("Failed to encode JSON response")
    }
}

func (api *AdminAPI) writeError(w http.ResponseWriter, status int, message string) {
    api.writeJSON(w, status, map[string]interface{}{
        "error": message,
        "code":  status,
        "time":  time.Now().Unix(),
    })
}
```

## Stream Management Endpoints

### internal/api/streams.go
```go
package api

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/gorilla/mux"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/pkg/models"
)

// ListStreams returns all active streams
func (api *AdminAPI) ListStreams(w http.ResponseWriter, r *http.Request) {
    streams := api.ingestion.ListStreams()
    
    response := ListStreamsResponse{
        Streams: streams,
        Total:   len(streams),
        Time:    time.Now().Unix(),
    }
    
    api.writeJSON(w, http.StatusOK, response)
}

// GetStream returns details for a specific stream
func (api *AdminAPI) GetStream(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["id"]
    
    stream, err := api.ingestion.GetStream(streamID)
    if err != nil {
        api.writeError(w, http.StatusNotFound, "Stream not found")
        return
    }
    
    // Get additional details
    stats := stream.GetStats()
    transcodingStatus := api.transcoding.GetPipelineStatus(streamID)
    packagingStatus := api.packaging.GetPackagerStatus(streamID)
    
    response := StreamDetailsResponse{
        ID:       streamID,
        Protocol: stream.Protocol,
        Status:   stream.GetStatus(),
        Metadata: stream.Metadata,
        Stats:    stats,
        Transcoding: transcodingStatus,
        Packaging:   packagingStatus,
        CreatedAt:   stream.CreatedAt,
        UpdatedAt:   time.Now(),
    }
    
    api.writeJSON(w, http.StatusOK, response)
}

// DeleteStream stops and removes a stream
func (api *AdminAPI) DeleteStream(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["id"]
    
    // Stop stream in all services
    if err := api.ingestion.UnregisterStream(streamID); err != nil {
        api.writeError(w, http.StatusNotFound, "Stream not found")
        return
    }
    
    // Broadcast update via WebSocket
    api.broadcastUpdate(WSMessage{
        Type: "stream_deleted",
        Data: map[string]interface{}{
            "stream_id": streamID,
            "timestamp": time.Now().Unix(),
        },
    })
    
    api.writeJSON(w, http.StatusOK, map[string]interface{}{
        "message": "Stream deleted successfully",
        "stream_id": streamID,
    })
}

// RestartStream restarts a failed stream
func (api *AdminAPI) RestartStream(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["id"]
    
    stream, err := api.ingestion.GetStream(streamID)
    if err != nil {
        api.writeError(w, http.StatusNotFound, "Stream not found")
        return
    }
    
    // Set reconnecting status
    stream.SetStatus(models.StreamStatusReconnecting)
    
    // Trigger reconnection logic
    go func() {
        // Simulate reconnection attempt
        time.Sleep(2 * time.Second)
        
        // Update status based on result
        if err := api.attemptReconnection(streamID); err != nil {
            stream.SetStatus(models.StreamStatusError)
            log.Error().Err(err).Str("stream_id", streamID).Msg("Failed to restart stream")
        } else {
            stream.SetStatus(models.StreamStatusActive)
            log.Info().Str("stream_id", streamID).Msg("Stream restarted successfully")
        }
        
        // Broadcast status update
        api.broadcastUpdate(WSMessage{
            Type: "stream_updated",
            Data: map[string]interface{}{
                "stream_id": streamID,
                "status": stream.GetStatus(),
                "timestamp": time.Now().Unix(),
            },
        })
    }()
    
    api.writeJSON(w, http.StatusAccepted, map[string]interface{}{
        "message": "Stream restart initiated",
        "stream_id": streamID,
    })
}

func (api *AdminAPI) attemptReconnection(streamID string) error {
    // Implementation would depend on protocol
    // This is a placeholder
    return nil
}

// Response types
type ListStreamsResponse struct {
    Streams []models.StreamInfo `json:"streams"`
    Total   int                 `json:"total"`
    Time    int64               `json:"time"`
}

type StreamDetailsResponse struct {
    ID          string                  `json:"id"`
    Protocol    string                  `json:"protocol"`
    Status      models.StreamStatus     `json:"status"`
    Metadata    models.StreamMetadata   `json:"metadata"`
    Stats       models.StreamStats      `json:"stats"`
    Transcoding models.PipelineStatus   `json:"transcoding"`
    Packaging   models.PackagerStatus   `json:"packaging"`
    CreatedAt   time.Time              `json:"created_at"`
    UpdatedAt   time.Time              `json:"updated_at"`
}
```

## System Control Endpoints

### internal/api/system.go
```go
package api

import (
    "encoding/json"
    "net/http"
    "runtime"
    "time"

    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/config"
)

// GetSystemInfo returns system information
func (api *AdminAPI) GetSystemInfo(w http.ResponseWriter, r *http.Request) {
    var m runtime.MemStats
    runtime.ReadMemStats(&m)
    
    info := SystemInfo{
        Version:   Version,
        BuildTime: BuildTime,
        GitCommit: GitCommit,
        GoVersion: runtime.Version(),
        OS:        runtime.GOOS,
        Arch:      runtime.GOARCH,
        CPUs:      runtime.NumCPU(),
        Goroutines: runtime.NumGoroutine(),
        Memory: MemoryInfo{
            Alloc:      m.Alloc,
            TotalAlloc: m.TotalAlloc,
            Sys:        m.Sys,
            NumGC:      m.NumGC,
        },
        Uptime: time.Since(startTime).Seconds(),
    }
    
    api.writeJSON(w, http.StatusOK, info)
}

// GetConfig returns current configuration
func (api *AdminAPI) GetConfig(w http.ResponseWriter, r *http.Request) {
    // Return sanitized config (remove sensitive values)
    cfg := api.getSanitizedConfig()
    api.writeJSON(w, http.StatusOK, cfg)
}

// UpdateConfig updates runtime configuration
func (api *AdminAPI) UpdateConfig(w http.ResponseWriter, r *http.Request) {
    var updates map[string]interface{}
    
    if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
        api.writeError(w, http.StatusBadRequest, "Invalid request body")
        return
    }
    
    // Apply configuration updates
    applied := make(map[string]interface{})
    
    for key, value := range updates {
        if err := api.applyConfigUpdate(key, value); err != nil {
            log.Error().Err(err).Str("key", key).Msg("Failed to apply config update")
            continue
        }
        applied[key] = value
    }
    
    // Broadcast configuration change
    api.broadcastUpdate(WSMessage{
        Type: "config_updated",
        Data: applied,
    })
    
    api.writeJSON(w, http.StatusOK, map[string]interface{}{
        "message": "Configuration updated",
        "applied": applied,
    })
}

func (api *AdminAPI) getSanitizedConfig() map[string]interface{} {
    // Return config with sensitive values removed
    return map[string]interface{}{
        "server": map[string]interface{}{
            "http3_port": 443,
            "admin_port": 8080,
        },
        "ingestion": map[string]interface{}{
            "max_streams": 25,
            "srt": map[string]interface{}{
                "enabled": true,
                "port":    6000,
            },
            "rtp": map[string]interface{}{
                "enabled": true,
                "port":    5004,
            },
        },
        "transcoding": map[string]interface{}{
            "use_gpu":       true,
            "output_width":  640,
            "output_height": 360,
            "output_bitrate": 400000,
        },
    }
}

func (api *AdminAPI) applyConfigUpdate(key string, value interface{}) error {
    // Implementation would update specific config values
    // This is a placeholder
    log.Info().Str("key", key).Interface("value", value).Msg("Applying config update")
    return nil
}

// Response types
type SystemInfo struct {
    Version    string     `json:"version"`
    BuildTime  string     `json:"build_time"`
    GitCommit  string     `json:"git_commit"`
    GoVersion  string     `json:"go_version"`
    OS         string     `json:"os"`
    Arch       string     `json:"arch"`
    CPUs       int        `json:"cpus"`
    Goroutines int        `json:"goroutines"`
    Memory     MemoryInfo `json:"memory"`
    Uptime     float64    `json:"uptime_seconds"`
}

type MemoryInfo struct {
    Alloc      uint64 `json:"alloc"`
    TotalAlloc uint64 `json:"total_alloc"`
    Sys        uint64 `json:"sys"`
    NumGC      uint32 `json:"num_gc"`
}
```

## Monitoring and Metrics

### internal/api/metrics.go
```go
package api

import (
    "net/http"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/rs/zerolog/log"
)

// GetHealth returns overall system health
func (api *AdminAPI) GetHealth(w http.ResponseWriter, r *http.Request) {
    health := HealthResponse{
        Status:    "healthy",
        Timestamp: time.Now().Unix(),
        Services:  make(map[string]ServiceHealth),
    }
    
    // Check each service
    services := map[string]interface {
        Health() models.ServiceHealth
    }{
        "ingestion":   api.ingestion,
        "transcoding": api.transcoding,
        "packaging":   api.packaging,
        "storage":     api.storage,
    }
    
    allHealthy := true
    
    for name, service := range services {
        svcHealth := service.Health()
        health.Services[name] = ServiceHealth{
            Healthy: svcHealth.Healthy,
            Status:  svcHealth.Status,
            Details: svcHealth.Details,
        }
        
        if !svcHealth.Healthy {
            allHealthy = false
        }
    }
    
    if !allHealthy {
        health.Status = "degraded"
    }
    
    status := http.StatusOK
    if health.Status != "healthy" {
        status = http.StatusServiceUnavailable
    }
    
    api.writeJSON(w, status, health)
}

// GetServiceHealth returns health for a specific service
func (api *AdminAPI) GetServiceHealth(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    service := vars["service"]
    
    var health models.ServiceHealth
    
    switch service {
    case "ingestion":
        health = api.ingestion.Health()
    case "transcoding":
        health = api.transcoding.Health()
    case "packaging":
        health = api.packaging.Health()
    case "storage":
        health = api.storage.Health()
    default:
        api.writeError(w, http.StatusNotFound, "Service not found")
        return
    }
    
    status := http.StatusOK
    if !health.Healthy {
        status = http.StatusServiceUnavailable
    }
    
    api.writeJSON(w, status, health)
}

// GetStats returns system statistics
func (api *AdminAPI) GetStats(w http.ResponseWriter, r *http.Request) {
    stats := SystemStats{
        Timestamp: time.Now().Unix(),
        Streams:   api.getStreamStats(),
        System:    api.getSystemStats(),
        Network:   api.getNetworkStats(),
    }
    
    api.writeJSON(w, http.StatusOK, stats)
}

// GetStreamStats returns statistics for a specific stream
func (api *AdminAPI) GetStreamStats(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["id"]
    
    stream, err := api.ingestion.GetStream(streamID)
    if err != nil {
        api.writeError(w, http.StatusNotFound, "Stream not found")
        return
    }
    
    stats := stream.GetStats()
    
    // Get additional metrics from Prometheus
    metrics := api.getStreamMetrics(streamID)
    
    response := StreamStatsResponse{
        StreamID:  streamID,
        Stats:     stats,
        Metrics:   metrics,
        Timestamp: time.Now().Unix(),
    }
    
    api.writeJSON(w, http.StatusOK, response)
}

func (api *AdminAPI) getStreamStats() StreamStatsAggregate {
    activeStreams := 0
    totalBitrate := float64(0)
    totalViewers := 0
    
    streams := api.ingestion.ListStreams()
    
    for _, stream := range streams {
        if stream.Status == models.StreamStatusActive {
            activeStreams++
            totalBitrate += stream.Stats.Bitrate
        }
    }
    
    return StreamStatsAggregate{
        Active:       activeStreams,
        Total:        len(streams),
        TotalBitrate: totalBitrate,
        TotalViewers: totalViewers,
    }
}

func (api *AdminAPI) getSystemStats() SystemResourceStats {
    // Gather system metrics
    return SystemResourceStats{
        CPUUsage:    api.getCPUUsage(),
        MemoryUsage: api.getMemoryUsage(),
        GPUUsage:    api.getGPUUsage(),
        DiskUsage:   api.getDiskUsage(),
    }
}

func (api *AdminAPI) getNetworkStats() NetworkStats {
    // Gather network metrics
    return NetworkStats{
        IngressBps:  api.getIngressBandwidth(),
        EgressBps:   api.getEgressBandwidth(),
        Connections: api.getActiveConnections(),
    }
}

func (api *AdminAPI) getStreamMetrics(streamID string) map[string]float64 {
    // Query Prometheus for stream-specific metrics
    // This is a simplified example
    return map[string]float64{
        "bytes_received":   12345678,
        "packets_received": 98765,
        "errors":          12,
        "segments_generated": 1234,
    }
}

// Response types
type HealthResponse struct {
    Status    string                   `json:"status"`
    Timestamp int64                    `json:"timestamp"`
    Services  map[string]ServiceHealth `json:"services"`
}

type ServiceHealth struct {
    Healthy bool                   `json:"healthy"`
    Status  string                 `json:"status"`
    Details map[string]interface{} `json:"details,omitempty"`
}

type SystemStats struct {
    Timestamp int64                 `json:"timestamp"`
    Streams   StreamStatsAggregate  `json:"streams"`
    System    SystemResourceStats   `json:"system"`
    Network   NetworkStats          `json:"network"`
}

type StreamStatsAggregate struct {
    Active       int     `json:"active"`
    Total        int     `json:"total"`
    TotalBitrate float64 `json:"total_bitrate_bps"`
    TotalViewers int     `json:"total_viewers"`
}

type SystemResourceStats struct {
    CPUUsage    float64 `json:"cpu_usage_percent"`
    MemoryUsage float64 `json:"memory_usage_percent"`
    GPUUsage    float64 `json:"gpu_usage_percent"`
    DiskUsage   float64 `json:"disk_usage_percent"`
}

type NetworkStats struct {
    IngressBps  float64 `json:"ingress_bps"`
    EgressBps   float64 `json:"egress_bps"`
    Connections int     `json:"active_connections"`
}

type StreamStatsResponse struct {
    StreamID  string               `json:"stream_id"`
    Stats     models.StreamStats   `json:"stats"`
    Metrics   map[string]float64   `json:"metrics"`
    Timestamp int64                `json:"timestamp"`
}
```

## Admin Dashboard

### web/admin/index.html
```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Mirror Streaming Admin</title>
    <link href="https://cdn.jsdelivr.net/npm/tailwindcss@2.2.19/dist/tailwind.min.css" rel="stylesheet">
    <link href="https://cdn.jsdelivr.net/npm/font-awesome@6.0.0/css/all.min.css" rel="stylesheet">
    <script src="https://cdn.jsdelivr.net/npm/chart.js@3.9.1/dist/chart.min.js"></script>
    <script src="https://unpkg.com/alpinejs@3.x.x/dist/cdn.min.js" defer></script>
</head>
<body class="bg-gray-100">
    <div x-data="adminDashboard()" x-init="init()">
        <!-- Header -->
        <header class="bg-white shadow">
            <div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
                <div class="flex justify-between items-center py-6">
                    <h1 class="text-3xl font-bold text-gray-900">
                        <i class="fas fa-broadcast-tower text-blue-600"></i>
                        Mirror Streaming Admin
                    </h1>
                    <div class="flex items-center space-x-4">
                        <span class="text-sm text-gray-500">
                            <i class="fas fa-circle" :class="connected ? 'text-green-500' : 'text-red-500'"></i>
                            <span x-text="connected ? 'Connected' : 'Disconnected'"></span>
                        </span>
                        <button @click="refresh()" class="btn btn-primary">
                            <i class="fas fa-sync-alt"></i> Refresh
                        </button>
                    </div>
                </div>
            </div>
        </header>

        <!-- Main Content -->
        <main class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
            <!-- System Overview -->
            <div class="grid grid-cols-1 md:grid-cols-4 gap-6 mb-8">
                <div class="bg-white p-6 rounded-lg shadow">
                    <div class="flex items-center justify-between">
                        <div>
                            <p class="text-sm font-medium text-gray-600">Active Streams</p>
                            <p class="text-3xl font-bold text-gray-900" x-text="stats.streams.active"></p>
                        </div>
                        <i class="fas fa-video text-blue-500 text-3xl"></i>
                    </div>
                </div>
                
                <div class="bg-white p-6 rounded-lg shadow">
                    <div class="flex items-center justify-between">
                        <div>
                            <p class="text-sm font-medium text-gray-600">Total Bitrate</p>
                            <p class="text-3xl font-bold text-gray-900" x-text="formatBitrate(stats.streams.total_bitrate_bps)"></p>
                        </div>
                        <i class="fas fa-tachometer-alt text-green-500 text-3xl"></i>
                    </div>
                </div>
                
                <div class="bg-white p-6 rounded-lg shadow">
                    <div class="flex items-center justify-between">
                        <div>
                            <p class="text-sm font-medium text-gray-600">CPU Usage</p>
                            <p class="text-3xl font-bold text-gray-900" x-text="stats.system.cpu_usage_percent.toFixed(1) + '%'"></p>
                        </div>
                        <i class="fas fa-microchip text-purple-500 text-3xl"></i>
                    </div>
                </div>
                
                <div class="bg-white p-6 rounded-lg shadow">
                    <div class="flex items-center justify-between">
                        <div>
                            <p class="text-sm font-medium text-gray-600">GPU Usage</p>
                            <p class="text-3xl font-bold text-gray-900" x-text="stats.system.gpu_usage_percent.toFixed(1) + '%'"></p>
                        </div>
                        <i class="fas fa-memory text-orange-500 text-3xl"></i>
                    </div>
                </div>
            </div>

            <!-- Streams Table -->
            <div class="bg-white shadow rounded-lg mb-8">
                <div class="px-6 py-4 border-b border-gray-200">
                    <h2 class="text-xl font-semibold text-gray-900">Active Streams</h2>
                </div>
                <div class="overflow-x-auto">
                    <table class="min-w-full divide-y divide-gray-200">
                        <thead class="bg-gray-50">
                            <tr>
                                <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                    Stream ID
                                </th>
                                <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                    Protocol
                                </th>
                                <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                    Status
                                </th>
                                <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                    Bitrate
                                </th>
                                <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                    Duration
                                </th>
                                <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                    Actions
                                </th>
                            </tr>
                        </thead>
                        <tbody class="bg-white divide-y divide-gray-200">
                            <template x-for="stream in streams" :key="stream.id">
                                <tr>
                                    <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-gray-900" x-text="stream.id"></td>
                                    <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-500" x-text="stream.protocol"></td>
                                    <td class="px-6 py-4 whitespace-nowrap">
                                        <span class="px-2 inline-flex text-xs leading-5 font-semibold rounded-full"
                                              :class="getStatusClass(stream.status)"
                                              x-text="stream.status">
                                        </span>
                                    </td>
                                    <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-500" x-text="formatBitrate(stream.stats.bitrate)"></td>
                                    <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-500" x-text="formatDuration(stream.stats.last_update)"></td>
                                    <td class="px-6 py-4 whitespace-nowrap text-sm font-medium">
                                        <button @click="viewStream(stream.id)" class="text-blue-600 hover:text-blue-900 mr-3">
                                            <i class="fas fa-eye"></i> View
                                        </button>
                                        <button @click="restartStream(stream.id)" class="text-yellow-600 hover:text-yellow-900 mr-3">
                                            <i class="fas fa-redo"></i> Restart
                                        </button>
                                        <button @click="deleteStream(stream.id)" class="text-red-600 hover:text-red-900">
                                            <i class="fas fa-trash"></i> Delete
                                        </button>
                                    </td>
                                </tr>
                            </template>
                        </tbody>
                    </table>
                </div>
            </div>

            <!-- Charts -->
            <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
                <div class="bg-white p-6 rounded-lg shadow">
                    <h3 class="text-lg font-semibold text-gray-900 mb-4">Bandwidth Usage</h3>
                    <canvas id="bandwidthChart"></canvas>
                </div>
                
                <div class="bg-white p-6 rounded-lg shadow">
                    <h3 class="text-lg font-semibold text-gray-900 mb-4">System Resources</h3>
                    <canvas id="resourceChart"></canvas>
                </div>
            </div>
        </main>
    </div>

    <script src="/admin/js/dashboard.js"></script>
</body>
</html>
```

### web/admin/js/dashboard.js
```javascript
function adminDashboard() {
    return {
        connected: false,
        ws: null,
        streams: [],
        stats: {
            streams: { active: 0, total: 0, total_bitrate_bps: 0 },
            system: { cpu_usage_percent: 0, gpu_usage_percent: 0 },
            network: { ingress_bps: 0, egress_bps: 0 }
        },
        
        init() {
            this.connectWebSocket();
            this.loadStreams();
            this.loadStats();
            this.initCharts();
            
            // Refresh data periodically
            setInterval(() => {
                this.loadStats();
            }, 5000);
        },
        
        connectWebSocket() {
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const wsUrl = `${protocol}//${window.location.host}/api/v1/ws`;
            
            this.ws = new WebSocket(wsUrl);
            
            this.ws.onopen = () => {
                console.log('WebSocket connected');
                this.connected = true;
            };
            
            this.ws.onmessage = (event) => {
                const message = JSON.parse(event.data);
                this.handleWebSocketMessage(message);
            };
            
            this.ws.onclose = () => {
                console.log('WebSocket disconnected');
                this.connected = false;
                // Reconnect after 5 seconds
                setTimeout(() => this.connectWebSocket(), 5000);
            };
        },
        
        handleWebSocketMessage(message) {
            switch (message.type) {
                case 'stream_updated':
                    this.updateStream(message.data);
                    break;
                case 'stream_deleted':
                    this.removeStream(message.data.stream_id);
                    break;
                case 'stats_update':
                    this.stats = message.data;
                    this.updateCharts();
                    break;
            }
        },
        
        async loadStreams() {
            try {
                const response = await fetch('/api/v1/streams');
                const data = await response.json();
                this.streams = data.streams;
            } catch (error) {
                console.error('Failed to load streams:', error);
            }
        },
        
        async loadStats() {
            try {
                const response = await fetch('/api/v1/stats');
                const data = await response.json();
                this.stats = data;
                this.updateCharts();
            } catch (error) {
                console.error('Failed to load stats:', error);
            }
        },
        
        async viewStream(streamId) {
            window.open(`/streams/${streamId}/playlist.m3u8`, '_blank');
        },
        
        async restartStream(streamId) {
            if (!confirm(`Restart stream ${streamId}?`)) return;
            
            try {
                await fetch(`/api/v1/streams/${streamId}/restart`, {
                    method: 'POST'
                });
                this.showNotification('Stream restart initiated', 'success');
            } catch (error) {
                this.showNotification('Failed to restart stream', 'error');
            }
        },
        
        async deleteStream(streamId) {
            if (!confirm(`Delete stream ${streamId}?`)) return;
            
            try {
                await fetch(`/api/v1/streams/${streamId}`, {
                    method: 'DELETE'
                });
                this.removeStream(streamId);
                this.showNotification('Stream deleted', 'success');
            } catch (error) {
                this.showNotification('Failed to delete stream', 'error');
            }
        },
        
        updateStream(data) {
            const index = this.streams.findIndex(s => s.id === data.stream_id);
            if (index >= 0) {
                this.streams[index].status = data.status;
            }
        },
        
        removeStream(streamId) {
            this.streams = this.streams.filter(s => s.id !== streamId);
        },
        
        formatBitrate(bps) {
            if (bps > 1000000) {
                return (bps / 1000000).toFixed(2) + ' Mbps';
            } else if (bps > 1000) {
                return (bps / 1000).toFixed(2) + ' Kbps';
            }
            return bps + ' bps';
        },
        
        formatDuration(timestamp) {
            const seconds = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000);
            const hours = Math.floor(seconds / 3600);
            const minutes = Math.floor((seconds % 3600) / 60);
            const secs = seconds % 60;
            
            if (hours > 0) {
                return `${hours}h ${minutes}m`;
            } else if (minutes > 0) {
                return `${minutes}m ${secs}s`;
            }
            return `${secs}s`;
        },
        
        getStatusClass(status) {
            switch (status) {
                case 'active':
                    return 'bg-green-100 text-green-800';
                case 'reconnecting':
                    return 'bg-yellow-100 text-yellow-800';
                case 'error':
                    return 'bg-red-100 text-red-800';
                default:
                    return 'bg-gray-100 text-gray-800';
            }
        },
        
        initCharts() {
            // Bandwidth chart
            const bandwidthCtx = document.getElementById('bandwidthChart').getContext('2d');
            this.bandwidthChart = new Chart(bandwidthCtx, {
                type: 'line',
                data: {
                    labels: [],
                    datasets: [{
                        label: 'Ingress',
                        data: [],
                        borderColor: 'rgb(59, 130, 246)',
                        tension: 0.1
                    }, {
                        label: 'Egress',
                        data: [],
                        borderColor: 'rgb(16, 185, 129)',
                        tension: 0.1
                    }]
                },
                options: {
                    responsive: true,
                    scales: {
                        y: {
                            beginAtZero: true
                        }
                    }
                }
            });
            
            // Resource chart
            const resourceCtx = document.getElementById('resourceChart').getContext('2d');
            this.resourceChart = new Chart(resourceCtx, {
                type: 'doughnut',
                data: {
                    labels: ['CPU', 'GPU', 'Memory'],
                    datasets: [{
                        data: [0, 0, 0],
                        backgroundColor: [
                            'rgb(59, 130, 246)',
                            'rgb(251, 146, 60)',
                            'rgb(139, 92, 246)'
                        ]
                    }]
                },
                options: {
                    responsive: true
                }
            });
        },
        
        updateCharts() {
            // Update bandwidth chart
            const now = new Date().toLocaleTimeString();
            this.bandwidthChart.data.labels.push(now);
            this.bandwidthChart.data.datasets[0].data.push(this.stats.network.ingress_bps / 1000000);
            this.bandwidthChart.data.datasets[1].data.push(this.stats.network.egress_bps / 1000000);
            
            // Keep only last 20 data points
            if (this.bandwidthChart.data.labels.length > 20) {
                this.bandwidthChart.data.labels.shift();
                this.bandwidthChart.data.datasets[0].data.shift();
                this.bandwidthChart.data.datasets[1].data.shift();
            }
            
            this.bandwidthChart.update();
            
            // Update resource chart
            this.resourceChart.data.datasets[0].data = [
                this.stats.system.cpu_usage_percent,
                this.stats.system.gpu_usage_percent,
                this.stats.system.memory_usage_percent || 0
            ];
            this.resourceChart.update();
        },
        
        showNotification(message, type) {
            // Simple notification (could be replaced with a proper toast library)
            console.log(`${type}: ${message}`);
        },
        
        refresh() {
            this.loadStreams();
            this.loadStats();
        }
    };
}
```

## WebSocket Real-time Updates

### internal/api/websocket.go
```go
package api

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/gorilla/websocket"
    "github.com/rs/zerolog/log"
)

// WSMessage represents a WebSocket message
type WSMessage struct {
    Type string                 `json:"type"`
    Data map[string]interface{} `json:"data"`
}

// HandleWebSocket handles WebSocket connections for real-time updates
func (api *AdminAPI) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
    conn, err := api.upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Error().Err(err).Msg("Failed to upgrade WebSocket connection")
        return
    }
    
    client := &WSClient{
        conn: conn,
        send: make(chan []byte, 256),
        api:  api,
    }
    
    api.registerClient(client)
    
    // Start goroutines for reading and writing
    go client.writePump()
    go client.readPump()
    
    // Send initial data
    client.sendInitialData()
}

func (api *AdminAPI) registerClient(client *WSClient) {
    api.wsMutex.Lock()
    defer api.wsMutex.Unlock()
    api.wsClients[client] = true
    
    log.Info().Str("remote", client.conn.RemoteAddr().String()).Msg("WebSocket client connected")
}

func (api *AdminAPI) unregisterClient(client *WSClient) {
    api.wsMutex.Lock()
    defer api.wsMutex.Unlock()
    
    if _, ok := api.wsClients[client]; ok {
        delete(api.wsClients, client)
        close(client.send)
        log.Info().Str("remote", client.conn.RemoteAddr().String()).Msg("WebSocket client disconnected")
    }
}

// broadcastUpdate sends an update to all connected clients
func (api *AdminAPI) broadcastUpdate(message WSMessage) {
    data, err := json.Marshal(message)
    if err != nil {
        log.Error().Err(err).Msg("Failed to marshal WebSocket message")
        return
    }
    
    api.wsMutex.RLock()
    defer api.wsMutex.RUnlock()
    
    for client := range api.wsClients {
        select {
        case client.send <- data:
        default:
            // Client's send channel is full, close it
            api.unregisterClient(client)
        }
    }
}

// WSClient methods
func (c *WSClient) readPump() {
    defer func() {
        c.api.unregisterClient(c)
        c.conn.Close()
    }()
    
    c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
    c.conn.SetPongHandler(func(string) error {
        c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
        return nil
    })
    
    for {
        _, message, err := c.conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
                log.Error().Err(err).Msg("WebSocket read error")
            }
            break
        }
        
        // Handle incoming messages (e.g., commands)
        var msg WSMessage
        if err := json.Unmarshal(message, &msg); err != nil {
            log.Error().Err(err).Msg("Failed to unmarshal WebSocket message")
            continue
        }
        
        c.handleMessage(msg)
    }
}

func (c *WSClient) writePump() {
    ticker := time.NewTicker(54 * time.Second)
    defer func() {
        ticker.Stop()
        c.conn.Close()
    }()
    
    for {
        select {
        case message, ok := <-c.send:
            c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
            if !ok {
                c.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            
            if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
                return
            }
            
        case <-ticker.C:
            c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
            if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
                return
            }
        }
    }
}

func (c *WSClient) handleMessage(msg WSMessage) {
    switch msg.Type {
    case "subscribe":
        // Handle subscription to specific events
        log.Debug().Interface("data", msg.Data).Msg("WebSocket subscription request")
    case "command":
        // Handle commands from client
        log.Debug().Interface("data", msg.Data).Msg("WebSocket command received")
    }
}

func (c *WSClient) sendInitialData() {
    // Send current system state
    streams := c.api.ingestion.ListStreams()
    
    message := WSMessage{
        Type: "initial_state",
        Data: map[string]interface{}{
            "streams": streams,
            "timestamp": time.Now().Unix(),
        },
    }
    
    data, _ := json.Marshal(message)
    c.send <- data
}

// StartPeriodicUpdates sends periodic updates to all clients
func (api *AdminAPI) StartPeriodicUpdates() {
    ticker := time.NewTicker(5 * time.Second)
    
    go func() {
        for range ticker.C {
            // Gather current stats
            stats := api.gatherSystemStats()
            
            // Broadcast to all clients
            api.broadcastUpdate(WSMessage{
                Type: "stats_update",
                Data: stats,
            })
        }
    }()
}

func (api *AdminAPI) gatherSystemStats() map[string]interface{} {
    return map[string]interface{}{
        "streams": api.getStreamStats(),
        "system":  api.getSystemStats(),
        "network": api.getNetworkStats(),
        "timestamp": time.Now().Unix(),
    }
}
```

## Next Steps

Continue to:
- [Part 6: Infrastructure and Deployment](plan-6.md) - AWS setup with Terraform
- [Part 7: Development Setup and CI/CD](plan-7.md) - Local development and GitHub Actions