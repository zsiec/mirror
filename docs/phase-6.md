# Phase 6: Admin Dashboard and Real-Time Monitoring

## Overview
This phase implements a comprehensive admin dashboard with real-time monitoring capabilities. We'll build a React-based web interface with WebSocket updates, stream control functionality, system health visualization, and intelligent alerting. The dashboard will provide operators with complete visibility and control over the streaming infrastructure.

## Goals
- Build modern React dashboard with Material-UI components
- Implement WebSocket-based real-time updates
- Create stream control interface (start/stop/configure)
- Visualize system metrics with interactive charts
- Build comprehensive alerting system
- Add audit logging and compliance reporting
- Create mobile-responsive design

## New Components Structure
```
internal/
├── api/
│   ├── admin/
│   │   ├── handlers.go         # Admin API handlers
│   │   ├── auth.go             # Authentication middleware
│   │   ├── stream_control.go   # Stream management endpoints
│   │   └── system_info.go      # System status endpoints
│   ├── websocket/
│   │   ├── hub.go              # WebSocket connection hub
│   │   ├── client.go           # WebSocket client handler
│   │   ├── messages.go         # Message types and protocols
│   │   └── broadcaster.go      # Event broadcasting
│   └── metrics/
│       ├── collector.go        # Metrics aggregation
│       ├── exporter.go         # Prometheus exporter
│       └── alerts.go           # Alert rule evaluation
├── dashboard/
│   ├── server.go               # Static file server
│   └── auth.go                 # Dashboard authentication
web/
├── dashboard/
│   ├── src/
│   │   ├── components/
│   │   │   ├── Layout/         # App layout components
│   │   │   ├── Streams/        # Stream management UI
│   │   │   ├── Metrics/        # Metrics visualization
│   │   │   ├── Alerts/         # Alert management
│   │   │   └── System/         # System status
│   │   ├── hooks/              # Custom React hooks
│   │   ├── services/           # API service layer
│   │   ├── store/              # Redux state management
│   │   └── utils/              # Utility functions
│   ├── package.json
│   └── vite.config.ts          # Vite bundler config
```

## Implementation Details

### 1. Updated Configuration
```go
// internal/config/config.go (additions)
type Config struct {
    // ... existing fields ...
    API       APIConfig       `mapstructure:"api"`
    Dashboard DashboardConfig `mapstructure:"dashboard"`
    Alerting  AlertingConfig  `mapstructure:"alerting"`
}

type APIConfig struct {
    Admin     AdminAPIConfig     `mapstructure:"admin"`
    WebSocket WebSocketConfig    `mapstructure:"websocket"`
    RateLimit RateLimitConfig    `mapstructure:"rate_limit"`
}

type AdminAPIConfig struct {
    Enabled          bool              `mapstructure:"enabled"`
    ListenAddr       string            `mapstructure:"listen_addr"`
    Port             int               `mapstructure:"port"`
    TLSEnabled       bool              `mapstructure:"tls_enabled"`
    TLSCertFile      string            `mapstructure:"tls_cert_file"`
    TLSKeyFile       string            `mapstructure:"tls_key_file"`
    Auth             AuthConfig        `mapstructure:"auth"`
    CORS             CORSConfig        `mapstructure:"cors"`
}

type AuthConfig struct {
    Type             string            `mapstructure:"type"`          // jwt, oauth2, basic
    JWTSecret        string            `mapstructure:"jwt_secret"`
    TokenExpiration  time.Duration     `mapstructure:"token_expiration"`
    RefreshExpiration time.Duration    `mapstructure:"refresh_expiration"`
    OAuth2           OAuth2Config      `mapstructure:"oauth2"`
}

type OAuth2Config struct {
    Provider         string            `mapstructure:"provider"`
    ClientID         string            `mapstructure:"client_id"`
    ClientSecret     string            `mapstructure:"client_secret"`
    RedirectURL      string            `mapstructure:"redirect_url"`
    Scopes           []string          `mapstructure:"scopes"`
}

type WebSocketConfig struct {
    PingInterval     time.Duration     `mapstructure:"ping_interval"`
    PongTimeout      time.Duration     `mapstructure:"pong_timeout"`
    WriteTimeout     time.Duration     `mapstructure:"write_timeout"`
    MaxMessageSize   int64             `mapstructure:"max_message_size"`
    BufferSize       int               `mapstructure:"buffer_size"`
}

type DashboardConfig struct {
    Enabled          bool              `mapstructure:"enabled"`
    BuildPath        string            `mapstructure:"build_path"`
    DevMode          bool              `mapstructure:"dev_mode"`
    DevProxyURL      string            `mapstructure:"dev_proxy_url"`
}

type AlertingConfig struct {
    Enabled          bool              `mapstructure:"enabled"`
    EvaluationInterval time.Duration   `mapstructure:"evaluation_interval"`
    Rules            []AlertRule       `mapstructure:"rules"`
    Notifications    NotificationConfig `mapstructure:"notifications"`
}

type AlertRule struct {
    Name             string            `mapstructure:"name"`
    Condition        string            `mapstructure:"condition"`
    Duration         time.Duration     `mapstructure:"duration"`
    Severity         string            `mapstructure:"severity"`
    Annotations      map[string]string `mapstructure:"annotations"`
}

type NotificationConfig struct {
    Email            EmailConfig       `mapstructure:"email"`
    Slack            SlackConfig       `mapstructure:"slack"`
    PagerDuty        PagerDutyConfig   `mapstructure:"pagerduty"`
    Webhook          WebhookConfig     `mapstructure:"webhook"`
}
```

### 2. Admin API Implementation
```go
// internal/api/admin/handlers.go
package admin

import (
    "encoding/json"
    "net/http"
    "time"
    
    "github.com/gorilla/mux"
    "github.com/sirupsen/logrus"
)

type Handler struct {
    ingestionMgr  *ingestion.Manager
    transcodingMgr *transcoding.Manager
    packagingMgr  *packaging.Manager
    storageMgr    *storage.Manager
    wsHub         *websocket.Hub
    logger        *logrus.Logger
}

func NewHandler(deps *Dependencies) *Handler {
    return &Handler{
        ingestionMgr:   deps.IngestionMgr,
        transcodingMgr: deps.TranscodingMgr,
        packagingMgr:   deps.PackagingMgr,
        storageMgr:     deps.StorageMgr,
        wsHub:          deps.WSHub,
        logger:         deps.Logger,
    }
}

func (h *Handler) RegisterRoutes(router *mux.Router) {
    api := router.PathPrefix("/api/v1").Subrouter()
    
    // Apply auth middleware
    api.Use(h.authMiddleware)
    api.Use(h.auditMiddleware)
    
    // System endpoints
    api.HandleFunc("/system/info", h.getSystemInfo).Methods("GET")
    api.HandleFunc("/system/health", h.getSystemHealth).Methods("GET")
    api.HandleFunc("/system/config", h.getSystemConfig).Methods("GET")
    
    // Stream management
    api.HandleFunc("/streams", h.listStreams).Methods("GET")
    api.HandleFunc("/streams/{id}", h.getStream).Methods("GET")
    api.HandleFunc("/streams/{id}/start", h.startStream).Methods("POST")
    api.HandleFunc("/streams/{id}/stop", h.stopStream).Methods("POST")
    api.HandleFunc("/streams/{id}/restart", h.restartStream).Methods("POST")
    api.HandleFunc("/streams/{id}/config", h.updateStreamConfig).Methods("PUT")
    
    // Metrics endpoints
    api.HandleFunc("/metrics/streams", h.getStreamMetrics).Methods("GET")
    api.HandleFunc("/metrics/system", h.getSystemMetrics).Methods("GET")
    api.HandleFunc("/metrics/cdn", h.getCDNMetrics).Methods("GET")
    
    // DVR endpoints
    api.HandleFunc("/dvr/recordings", h.listRecordings).Methods("GET")
    api.HandleFunc("/dvr/recordings/{id}", h.getRecording).Methods("GET")
    api.HandleFunc("/dvr/recordings/{id}/delete", h.deleteRecording).Methods("DELETE")
    
    // Alert management
    api.HandleFunc("/alerts", h.listAlerts).Methods("GET")
    api.HandleFunc("/alerts/{id}/acknowledge", h.acknowledgeAlert).Methods("POST")
    api.HandleFunc("/alerts/{id}/resolve", h.resolveAlert).Methods("POST")
    
    // WebSocket endpoint
    api.HandleFunc("/ws", h.handleWebSocket)
}

func (h *Handler) getSystemInfo(w http.ResponseWriter, r *http.Request) {
    info := SystemInfo{
        Version:      version.Version,
        BuildTime:    version.BuildTime,
        GitCommit:    version.GitCommit,
        GoVersion:    runtime.Version(),
        OS:           runtime.GOOS,
        Arch:         runtime.GOARCH,
        Uptime:       time.Since(h.startTime).String(),
        
        Capabilities: Capabilities{
            MaxStreams:      25,
            MaxViewers:      5000,
            SupportedCodecs: []string{"hevc", "h264", "aac"},
            Features: []string{
                "srt-ingestion",
                "rtp-ingestion",
                "gpu-transcoding",
                "ll-hls",
                "http3",
                "dvr",
                "multi-region",
            },
        },
    }
    
    h.respondJSON(w, info)
}

func (h *Handler) listStreams(w http.ResponseWriter, r *http.Request) {
    // Get query parameters
    status := r.URL.Query().Get("status")
    sortBy := r.URL.Query().Get("sort")
    
    // Get streams from ingestion manager
    streams, err := h.ingestionMgr.ListStreams()
    if err != nil {
        h.respondError(w, err, http.StatusInternalServerError)
        return
    }
    
    // Enrich with additional data
    enrichedStreams := make([]*StreamInfo, 0, len(streams))
    for _, stream := range streams {
        info := &StreamInfo{
            ID:           stream.ID,
            Type:         stream.Type,
            Status:       stream.Status,
            SourceAddr:   stream.SourceAddr,
            CreatedAt:    stream.CreatedAt,
            LastHeartbeat: stream.LastHeartbeat,
            
            // Get transcoding status
            TranscodingStatus: h.getTranscodingStatus(stream.ID),
            
            // Get packaging status
            PackagingStatus: h.getPackagingStatus(stream.ID),
            
            // Get viewer count
            ViewerCount: h.getViewerCount(stream.ID),
            
            // Get metrics
            Metrics: h.getStreamMetrics(stream.ID),
        }
        
        enrichedStreams = append(enrichedStreams, info)
    }
    
    // Apply filters
    if status != "" {
        enrichedStreams = h.filterStreamsByStatus(enrichedStreams, status)
    }
    
    // Sort
    if sortBy != "" {
        h.sortStreams(enrichedStreams, sortBy)
    }
    
    h.respondJSON(w, map[string]interface{}{
        "streams": enrichedStreams,
        "count":   len(enrichedStreams),
        "timestamp": time.Now(),
    })
}

// internal/api/admin/stream_control.go
package admin

import (
    "fmt"
    "net/http"
)

func (h *Handler) startStream(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["id"]
    
    // Parse request body
    var config StreamStartConfig
    if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
        h.respondError(w, err, http.StatusBadRequest)
        return
    }
    
    // Validate configuration
    if err := config.Validate(); err != nil {
        h.respondError(w, err, http.StatusBadRequest)
        return
    }
    
    // Start ingestion
    if err := h.ingestionMgr.StartStream(streamID, &config); err != nil {
        h.respondError(w, err, http.StatusInternalServerError)
        return
    }
    
    // Get stream buffer
    buffer := h.ingestionMgr.GetBuffer(streamID)
    
    // Start transcoding
    transcodingOutput, err := h.transcodingMgr.StartTranscoding(streamID, buffer)
    if err != nil {
        h.ingestionMgr.StopStream(streamID)
        h.respondError(w, err, http.StatusInternalServerError)
        return
    }
    
    // Start packaging
    if err := h.packagingMgr.StartPackaging(streamID, transcodingOutput); err != nil {
        h.transcodingMgr.StopTranscoding(streamID)
        h.ingestionMgr.StopStream(streamID)
        h.respondError(w, err, http.StatusInternalServerError)
        return
    }
    
    // Start DVR if enabled
    if config.DVREnabled {
        if err := h.storageMgr.StartDVR(streamID); err != nil {
            h.logger.Warnf("Failed to start DVR for stream %s: %v", streamID, err)
        }
    }
    
    // Broadcast event
    h.wsHub.Broadcast(&websocket.Message{
        Type: "stream.started",
        Data: map[string]interface{}{
            "stream_id": streamID,
            "config":    config,
            "timestamp": time.Now(),
        },
    })
    
    // Log audit event
    h.auditLog(r, "stream.start", map[string]interface{}{
        "stream_id": streamID,
        "config":    config,
    })
    
    h.respondJSON(w, map[string]interface{}{
        "status": "started",
        "stream_id": streamID,
    })
}

func (h *Handler) stopStream(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["id"]
    
    // Stop in reverse order
    errs := make([]error, 0)
    
    // Stop DVR
    if err := h.storageMgr.StopDVR(streamID); err != nil {
        errs = append(errs, fmt.Errorf("dvr: %w", err))
    }
    
    // Stop packaging
    if err := h.packagingMgr.StopPackaging(streamID); err != nil {
        errs = append(errs, fmt.Errorf("packaging: %w", err))
    }
    
    // Stop transcoding
    if err := h.transcodingMgr.StopTranscoding(streamID); err != nil {
        errs = append(errs, fmt.Errorf("transcoding: %w", err))
    }
    
    // Stop ingestion
    if err := h.ingestionMgr.StopStream(streamID); err != nil {
        errs = append(errs, fmt.Errorf("ingestion: %w", err))
    }
    
    // Broadcast event
    h.wsHub.Broadcast(&websocket.Message{
        Type: "stream.stopped",
        Data: map[string]interface{}{
            "stream_id": streamID,
            "timestamp": time.Now(),
            "errors":    errs,
        },
    })
    
    // Log audit event
    h.auditLog(r, "stream.stop", map[string]interface{}{
        "stream_id": streamID,
        "errors":    len(errs),
    })
    
    if len(errs) > 0 {
        h.respondJSON(w, map[string]interface{}{
            "status": "stopped_with_errors",
            "stream_id": streamID,
            "errors": errs,
        })
    } else {
        h.respondJSON(w, map[string]interface{}{
            "status": "stopped",
            "stream_id": streamID,
        })
    }
}
```

### 3. WebSocket Implementation
```go
// internal/api/websocket/hub.go
package websocket

import (
    "encoding/json"
    "sync"
    "time"
    
    "github.com/gorilla/websocket"
    "github.com/sirupsen/logrus"
)

type Hub struct {
    clients    map[*Client]bool
    broadcast  chan *Message
    register   chan *Client
    unregister chan *Client
    
    mu         sync.RWMutex
    logger     *logrus.Logger
}

type Client struct {
    hub        *Hub
    conn       *websocket.Conn
    send       chan []byte
    
    id         string
    user       string
    subscriptions map[string]bool
}

type Message struct {
    Type       string                 `json:"type"`
    Data       interface{}            `json:"data"`
    Timestamp  time.Time              `json:"timestamp"`
    ID         string                 `json:"id,omitempty"`
}

func NewHub(logger *logrus.Logger) *Hub {
    return &Hub{
        clients:    make(map[*Client]bool),
        broadcast:  make(chan *Message, 256),
        register:   make(chan *Client),
        unregister: make(chan *Client),
        logger:     logger,
    }
}

func (h *Hub) Run() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case client := <-h.register:
            h.mu.Lock()
            h.clients[client] = true
            h.mu.Unlock()
            
            h.logger.Infof("WebSocket client connected: %s", client.id)
            
            // Send welcome message
            welcome := &Message{
                Type: "welcome",
                Data: map[string]interface{}{
                    "client_id": client.id,
                    "server_time": time.Now(),
                },
            }
            
            if data, err := json.Marshal(welcome); err == nil {
                client.send <- data
            }
            
        case client := <-h.unregister:
            h.mu.Lock()
            if _, ok := h.clients[client]; ok {
                delete(h.clients, client)
                close(client.send)
                h.mu.Unlock()
                
                h.logger.Infof("WebSocket client disconnected: %s", client.id)
            } else {
                h.mu.Unlock()
            }
            
        case message := <-h.broadcast:
            data, err := json.Marshal(message)
            if err != nil {
                h.logger.Errorf("Failed to marshal message: %v", err)
                continue
            }
            
            h.mu.RLock()
            for client := range h.clients {
                // Check if client is subscribed to this message type
                if h.shouldSendToClient(client, message) {
                    select {
                    case client.send <- data:
                    default:
                        // Client's send channel is full, close it
                        close(client.send)
                        delete(h.clients, client)
                    }
                }
            }
            h.mu.RUnlock()
            
        case <-ticker.C:
            // Send heartbeat to all clients
            h.Broadcast(&Message{
                Type: "heartbeat",
                Data: map[string]interface{}{
                    "server_time": time.Now(),
                },
            })
        }
    }
}

func (h *Hub) shouldSendToClient(client *Client, message *Message) bool {
    // Check subscription filters
    if len(client.subscriptions) == 0 {
        return true // No filters, send all
    }
    
    // Extract topic from message type (e.g., "stream.started" -> "stream")
    topic := strings.Split(message.Type, ".")[0]
    
    _, subscribed := client.subscriptions[topic]
    return subscribed
}

// internal/api/websocket/client.go
package websocket

import (
    "bytes"
    "encoding/json"
    "time"
    
    "github.com/gorilla/websocket"
)

const (
    writeWait      = 10 * time.Second
    pongWait       = 60 * time.Second
    pingPeriod     = (pongWait * 9) / 10
    maxMessageSize = 512 * 1024 // 512KB
)

func (c *Client) readPump() {
    defer func() {
        c.hub.unregister <- c
        c.conn.Close()
    }()
    
    c.conn.SetReadLimit(maxMessageSize)
    c.conn.SetReadDeadline(time.Now().Add(pongWait))
    c.conn.SetPongHandler(func(string) error {
        c.conn.SetReadDeadline(time.Now().Add(pongWait))
        return nil
    })
    
    for {
        _, message, err := c.conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
                c.hub.logger.Errorf("WebSocket error: %v", err)
            }
            break
        }
        
        // Parse client message
        var msg ClientMessage
        if err := json.Unmarshal(message, &msg); err != nil {
            c.sendError("Invalid message format")
            continue
        }
        
        // Handle client message
        c.handleMessage(&msg)
    }
}

func (c *Client) writePump() {
    ticker := time.NewTicker(pingPeriod)
    defer func() {
        ticker.Stop()
        c.conn.Close()
    }()
    
    for {
        select {
        case message, ok := <-c.send:
            c.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if !ok {
                c.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            
            w, err := c.conn.NextWriter(websocket.TextMessage)
            if err != nil {
                return
            }
            w.Write(message)
            
            // Add queued messages to the current websocket message
            n := len(c.send)
            for i := 0; i < n; i++ {
                w.Write([]byte{'\n'})
                w.Write(<-c.send)
            }
            
            if err := w.Close(); err != nil {
                return
            }
            
        case <-ticker.C:
            c.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
                return
            }
        }
    }
}

type ClientMessage struct {
    Type    string                 `json:"type"`
    Data    json.RawMessage        `json:"data"`
    ID      string                 `json:"id"`
}

func (c *Client) handleMessage(msg *ClientMessage) {
    switch msg.Type {
    case "subscribe":
        var sub SubscribeRequest
        if err := json.Unmarshal(msg.Data, &sub); err != nil {
            c.sendError("Invalid subscribe request")
            return
        }
        
        c.mu.Lock()
        for _, topic := range sub.Topics {
            c.subscriptions[topic] = true
        }
        c.mu.Unlock()
        
        c.sendResponse(msg.ID, map[string]interface{}{
            "subscribed": sub.Topics,
        })
        
    case "unsubscribe":
        var unsub UnsubscribeRequest
        if err := json.Unmarshal(msg.Data, &unsub); err != nil {
            c.sendError("Invalid unsubscribe request")
            return
        }
        
        c.mu.Lock()
        for _, topic := range unsub.Topics {
            delete(c.subscriptions, topic)
        }
        c.mu.Unlock()
        
        c.sendResponse(msg.ID, map[string]interface{}{
            "unsubscribed": unsub.Topics,
        })
        
    case "ping":
        c.sendResponse(msg.ID, map[string]interface{}{
            "pong": true,
            "timestamp": time.Now(),
        })
        
    default:
        c.sendError(fmt.Sprintf("Unknown message type: %s", msg.Type))
    }
}
```

### 4. React Dashboard Components
```typescript
// web/dashboard/src/components/Streams/StreamList.tsx
import React, { useEffect, useState } from 'react';
import {
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Paper,
  Chip,
  IconButton,
  Tooltip,
  Box,
  Typography,
} from '@mui/material';
import {
  PlayArrow,
  Stop,
  Refresh,
  Settings,
  Visibility,
} from '@mui/icons-material';
import { useWebSocket } from '../../hooks/useWebSocket';
import { useApi } from '../../hooks/useApi';
import { Stream } from '../../types';
import StreamMetrics from './StreamMetrics';

export const StreamList: React.FC = () => {
  const [streams, setStreams] = useState<Stream[]>([]);
  const [selectedStream, setSelectedStream] = useState<string | null>(null);
  const { subscribe, unsubscribe } = useWebSocket();
  const api = useApi();

  useEffect(() => {
    // Load initial streams
    loadStreams();

    // Subscribe to stream updates
    const unsubscribeFn = subscribe('stream', (message) => {
      handleStreamUpdate(message);
    });

    return () => {
      unsubscribeFn();
    };
  }, []);

  const loadStreams = async () => {
    try {
      const response = await api.get('/streams');
      setStreams(response.data.streams);
    } catch (error) {
      console.error('Failed to load streams:', error);
    }
  };

  const handleStreamUpdate = (message: any) => {
    switch (message.type) {
      case 'stream.started':
      case 'stream.stopped':
      case 'stream.updated':
        loadStreams(); // Reload the list
        break;
    }
  };

  const handleStreamAction = async (streamId: string, action: string) => {
    try {
      await api.post(`/streams/${streamId}/${action}`);
    } catch (error) {
      console.error(`Failed to ${action} stream:`, error);
    }
  };

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'active':
        return 'success';
      case 'error':
        return 'error';
      case 'paused':
        return 'warning';
      default:
        return 'default';
    }
  };

  return (
    <Box>
      <Typography variant="h4" gutterBottom>
        Active Streams
      </Typography>
      
      <TableContainer component={Paper}>
        <Table>
          <TableHead>
            <TableRow>
              <TableCell>Stream ID</TableCell>
              <TableCell>Type</TableCell>
              <TableCell>Status</TableCell>
              <TableCell>Source</TableCell>
              <TableCell>Bitrate</TableCell>
              <TableCell>Viewers</TableCell>
              <TableCell>Uptime</TableCell>
              <TableCell>Actions</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {streams.map((stream) => (
              <React.Fragment key={stream.id}>
                <TableRow
                  hover
                  selected={selectedStream === stream.id}
                  onClick={() => setSelectedStream(
                    selectedStream === stream.id ? null : stream.id
                  )}
                >
                  <TableCell>{stream.id}</TableCell>
                  <TableCell>
                    <Chip label={stream.type} size="small" />
                  </TableCell>
                  <TableCell>
                    <Chip
                      label={stream.status}
                      color={getStatusColor(stream.status)}
                      size="small"
                    />
                  </TableCell>
                  <TableCell>{stream.sourceAddr}</TableCell>
                  <TableCell>{formatBitrate(stream.metrics?.bitrate)}</TableCell>
                  <TableCell>{stream.viewerCount}</TableCell>
                  <TableCell>{formatUptime(stream.createdAt)}</TableCell>
                  <TableCell>
                    <Tooltip title="View Details">
                      <IconButton size="small">
                        <Visibility />
                      </IconButton>
                    </Tooltip>
                    {stream.status === 'active' ? (
                      <Tooltip title="Stop Stream">
                        <IconButton
                          size="small"
                          onClick={(e) => {
                            e.stopPropagation();
                            handleStreamAction(stream.id, 'stop');
                          }}
                        >
                          <Stop />
                        </IconButton>
                      </Tooltip>
                    ) : (
                      <Tooltip title="Start Stream">
                        <IconButton
                          size="small"
                          onClick={(e) => {
                            e.stopPropagation();
                            handleStreamAction(stream.id, 'start');
                          }}
                        >
                          <PlayArrow />
                        </IconButton>
                      </Tooltip>
                    )}
                    <Tooltip title="Restart Stream">
                      <IconButton
                        size="small"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleStreamAction(stream.id, 'restart');
                        }}
                      >
                        <Refresh />
                      </IconButton>
                    </Tooltip>
                    <Tooltip title="Configure">
                      <IconButton size="small">
                        <Settings />
                      </IconButton>
                    </Tooltip>
                  </TableCell>
                </TableRow>
                {selectedStream === stream.id && (
                  <TableRow>
                    <TableCell colSpan={8}>
                      <StreamMetrics streamId={stream.id} />
                    </TableCell>
                  </TableRow>
                )}
              </React.Fragment>
            ))}
          </TableBody>
        </Table>
      </TableContainer>
    </Box>
  );
};

// web/dashboard/src/components/Metrics/SystemMetrics.tsx
import React, { useEffect, useState } from 'react';
import {
  Grid,
  Paper,
  Box,
  Typography,
  LinearProgress,
} from '@mui/material';
import {
  LineChart,
  Line,
  AreaChart,
  Area,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from 'recharts';
import { useWebSocket } from '../../hooks/useWebSocket';
import { useApi } from '../../hooks/useApi';

interface SystemMetrics {
  cpu: number;
  memory: {
    used: number;
    total: number;
    percentage: number;
  };
  gpu: GPUMetrics[];
  network: {
    inbound: number;
    outbound: number;
  };
  storage: {
    used: number;
    total: number;
    percentage: number;
  };
}

interface GPUMetrics {
  index: number;
  name: string;
  utilization: number;
  memory: {
    used: number;
    total: number;
  };
  temperature: number;
  encoderSessions: number;
}

export const SystemMetrics: React.FC = () => {
  const [metrics, setMetrics] = useState<SystemMetrics | null>(null);
  const [history, setHistory] = useState<any[]>([]);
  const { subscribe } = useWebSocket();
  const api = useApi();

  useEffect(() => {
    loadMetrics();

    const unsubscribe = subscribe('metrics', (message) => {
      if (message.type === 'metrics.system') {
        updateMetrics(message.data);
      }
    });

    return () => {
      unsubscribe();
    };
  }, []);

  const loadMetrics = async () => {
    try {
      const response = await api.get('/metrics/system');
      setMetrics(response.data);
      updateHistory(response.data);
    } catch (error) {
      console.error('Failed to load metrics:', error);
    }
  };

  const updateMetrics = (newMetrics: SystemMetrics) => {
    setMetrics(newMetrics);
    updateHistory(newMetrics);
  };

  const updateHistory = (newMetrics: SystemMetrics) => {
    setHistory((prev) => {
      const updated = [...prev, {
        timestamp: new Date().toLocaleTimeString(),
        cpu: newMetrics.cpu,
        memory: newMetrics.memory.percentage,
        networkIn: newMetrics.network.inbound,
        networkOut: newMetrics.network.outbound,
      }];
      
      // Keep last 60 data points
      return updated.slice(-60);
    });
  };

  if (!metrics) {
    return <LinearProgress />;
  }

  return (
    <Box>
      <Typography variant="h4" gutterBottom>
        System Metrics
      </Typography>

      <Grid container spacing={3}>
        {/* CPU & Memory */}
        <Grid item xs={12} md={6}>
          <Paper sx={{ p: 2 }}>
            <Typography variant="h6" gutterBottom>
              CPU & Memory Usage
            </Typography>
            <ResponsiveContainer width="100%" height={300}>
              <LineChart data={history}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="timestamp" />
                <YAxis domain={[0, 100]} />
                <Tooltip />
                <Legend />
                <Line
                  type="monotone"
                  dataKey="cpu"
                  stroke="#8884d8"
                  name="CPU %"
                />
                <Line
                  type="monotone"
                  dataKey="memory"
                  stroke="#82ca9d"
                  name="Memory %"
                />
              </LineChart>
            </ResponsiveContainer>
          </Paper>
        </Grid>

        {/* Network */}
        <Grid item xs={12} md={6}>
          <Paper sx={{ p: 2 }}>
            <Typography variant="h6" gutterBottom>
              Network Throughput
            </Typography>
            <ResponsiveContainer width="100%" height={300}>
              <AreaChart data={history}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="timestamp" />
                <YAxis />
                <Tooltip />
                <Legend />
                <Area
                  type="monotone"
                  dataKey="networkIn"
                  stackId="1"
                  stroke="#8884d8"
                  fill="#8884d8"
                  name="Inbound (Mbps)"
                />
                <Area
                  type="monotone"
                  dataKey="networkOut"
                  stackId="1"
                  stroke="#82ca9d"
                  fill="#82ca9d"
                  name="Outbound (Mbps)"
                />
              </AreaChart>
            </ResponsiveContainer>
          </Paper>
        </Grid>

        {/* GPU Metrics */}
        {metrics.gpu.map((gpu) => (
          <Grid item xs={12} md={6} key={gpu.index}>
            <Paper sx={{ p: 2 }}>
              <Typography variant="h6" gutterBottom>
                GPU {gpu.index}: {gpu.name}
              </Typography>
              <Grid container spacing={2}>
                <Grid item xs={6}>
                  <MetricCard
                    label="Utilization"
                    value={`${gpu.utilization}%`}
                    progress={gpu.utilization}
                  />
                </Grid>
                <Grid item xs={6}>
                  <MetricCard
                    label="Temperature"
                    value={`${gpu.temperature}°C`}
                    progress={gpu.temperature}
                    max={90}
                    warning={85}
                  />
                </Grid>
                <Grid item xs={6}>
                  <MetricCard
                    label="Memory"
                    value={`${formatBytes(gpu.memory.used)} / ${formatBytes(gpu.memory.total)}`}
                    progress={(gpu.memory.used / gpu.memory.total) * 100}
                  />
                </Grid>
                <Grid item xs={6}>
                  <MetricCard
                    label="Encoder Sessions"
                    value={gpu.encoderSessions.toString()}
                    progress={(gpu.encoderSessions / 15) * 100}
                  />
                </Grid>
              </Grid>
            </Paper>
          </Grid>
        ))}

        {/* Storage */}
        <Grid item xs={12}>
          <Paper sx={{ p: 2 }}>
            <Typography variant="h6" gutterBottom>
              Storage Usage
            </Typography>
            <Box sx={{ mt: 2 }}>
              <Typography variant="body2" color="text.secondary">
                {formatBytes(metrics.storage.used)} / {formatBytes(metrics.storage.total)}
              </Typography>
              <LinearProgress
                variant="determinate"
                value={metrics.storage.percentage}
                sx={{ mt: 1, height: 10 }}
              />
            </Box>
          </Paper>
        </Grid>
      </Grid>
    </Box>
  );
};

interface MetricCardProps {
  label: string;
  value: string;
  progress: number;
  max?: number;
  warning?: number;
}

const MetricCard: React.FC<MetricCardProps> = ({
  label,
  value,
  progress,
  max = 100,
  warning = 80,
}) => {
  const percentage = (progress / max) * 100;
  const color = percentage > warning ? 'error' : 'primary';

  return (
    <Box>
      <Typography variant="body2" color="text.secondary">
        {label}
      </Typography>
      <Typography variant="h6">{value}</Typography>
      <LinearProgress
        variant="determinate"
        value={percentage}
        color={color}
        sx={{ mt: 1 }}
      />
    </Box>
  );
};
```

### 5. Alert Management
```go
// internal/api/metrics/alerts.go
package metrics

import (
    "context"
    "fmt"
    "sync"
    "time"
    
    "github.com/prometheus/client_golang/prometheus"
    "github.com/sirupsen/logrus"
)

type AlertManager struct {
    config       *AlertingConfig
    evaluator    *RuleEvaluator
    notifier     *Notifier
    alerts       sync.Map // alertID -> *Alert
    logger       *logrus.Logger
    
    ctx          context.Context
    cancel       context.CancelFunc
}

type Alert struct {
    ID           string
    Rule         *AlertRule
    Status       AlertStatus
    Value        float64
    Labels       map[string]string
    Annotations  map[string]string
    StartsAt     time.Time
    EndsAt       *time.Time
    LastEval     time.Time
}

type AlertStatus string

const (
    AlertPending  AlertStatus = "pending"
    AlertFiring   AlertStatus = "firing"
    AlertResolved AlertStatus = "resolved"
)

func NewAlertManager(config *AlertingConfig, logger *logrus.Logger) *AlertManager {
    ctx, cancel := context.WithCancel(context.Background())
    
    return &AlertManager{
        config:    config,
        evaluator: NewRuleEvaluator(config.Rules),
        notifier:  NewNotifier(config.Notifications, logger),
        logger:    logger,
        ctx:       ctx,
        cancel:    cancel,
    }
}

func (am *AlertManager) Start() {
    ticker := time.NewTicker(am.config.EvaluationInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-am.ctx.Done():
            return
        case <-ticker.C:
            am.evaluate()
        }
    }
}

func (am *AlertManager) evaluate() {
    for _, rule := range am.config.Rules {
        go am.evaluateRule(rule)
    }
}

func (am *AlertManager) evaluateRule(rule AlertRule) {
    // Evaluate rule condition
    value, labels, err := am.evaluator.Evaluate(rule)
    if err != nil {
        am.logger.Errorf("Failed to evaluate rule %s: %v", rule.Name, err)
        return
    }
    
    alertID := am.generateAlertID(rule, labels)
    
    // Check if condition is met
    if value > 0 {
        am.handleActiveAlert(alertID, rule, value, labels)
    } else {
        am.handleInactiveAlert(alertID)
    }
}

func (am *AlertManager) handleActiveAlert(alertID string, rule AlertRule, value float64, labels map[string]string) {
    existing, exists := am.alerts.Load(alertID)
    
    if !exists {
        // New alert
        alert := &Alert{
            ID:          alertID,
            Rule:        &rule,
            Status:      AlertPending,
            Value:       value,
            Labels:      labels,
            Annotations: am.expandAnnotations(rule.Annotations, labels),
            StartsAt:    time.Now(),
            LastEval:    time.Now(),
        }
        
        am.alerts.Store(alertID, alert)
        am.logger.Infof("Alert pending: %s", alertID)
        
    } else {
        // Existing alert
        alert := existing.(*Alert)
        alert.Value = value
        alert.LastEval = time.Now()
        
        // Check if pending duration exceeded
        if alert.Status == AlertPending && time.Since(alert.StartsAt) >= rule.Duration {
            alert.Status = AlertFiring
            am.logger.Warnf("Alert firing: %s", alertID)
            
            // Send notifications
            am.notifier.Send(alert)
        }
    }
}

func (am *AlertManager) handleInactiveAlert(alertID string) {
    existing, exists := am.alerts.Load(alertID)
    if !exists {
        return
    }
    
    alert := existing.(*Alert)
    if alert.Status != AlertResolved {
        alert.Status = AlertResolved
        now := time.Now()
        alert.EndsAt = &now
        
        am.logger.Infof("Alert resolved: %s", alertID)
        
        // Send resolution notification
        am.notifier.Send(alert)
        
        // Remove after grace period
        time.AfterFunc(5*time.Minute, func() {
            am.alerts.Delete(alertID)
        })
    }
}

// Example alert rules
var defaultAlertRules = []AlertRule{
    {
        Name:     "HighCPUUsage",
        Condition: "avg(cpu_usage_percent) > 80",
        Duration: 5 * time.Minute,
        Severity: "warning",
        Annotations: map[string]string{
            "summary":     "High CPU usage detected",
            "description": "CPU usage is above 80% for more than 5 minutes",
        },
    },
    {
        Name:     "StreamDown",
        Condition: "stream_status{status=\"error\"} > 0",
        Duration: 1 * time.Minute,
        Severity: "critical",
        Annotations: map[string]string{
            "summary":     "Stream {{.stream_id}} is down",
            "description": "Stream {{.stream_id}} has been in error state for more than 1 minute",
        },
    },
    {
        Name:     "HighGPUTemperature",
        Condition: "gpu_temperature_celsius > 85",
        Duration: 2 * time.Minute,
        Severity: "critical",
        Annotations: map[string]string{
            "summary":     "GPU {{.gpu_index}} temperature critical",
            "description": "GPU {{.gpu_index}} temperature is above 85°C",
        },
    },
    {
        Name:     "LowDiskSpace",
        Condition: "(storage_free_bytes / storage_total_bytes) < 0.1",
        Duration: 10 * time.Minute,
        Severity: "warning",
        Annotations: map[string]string{
            "summary":     "Low disk space",
            "description": "Less than 10% disk space remaining",
        },
    },
}
```

### 6. Dashboard Configuration
```yaml
# configs/default.yaml (additions)
api:
  admin:
    enabled: true
    listen_addr: "0.0.0.0"
    port: 8080
    tls_enabled: true
    tls_cert_file: "./certs/admin.crt"
    tls_key_file: "./certs/admin.key"
    auth:
      type: "jwt"
      jwt_secret: "${JWT_SECRET}"
      token_expiration: 1h
      refresh_expiration: 24h
    cors:
      enabled: true
      origins:
        - "https://dashboard.example.com"
        - "http://localhost:3000"
      
  websocket:
    ping_interval: 30s
    pong_timeout: 60s
    write_timeout: 10s
    max_message_size: 524288  # 512KB
    buffer_size: 256
    
dashboard:
  enabled: true
  build_path: "./web/dashboard/dist"
  dev_mode: false
  dev_proxy_url: "http://localhost:5173"
  
alerting:
  enabled: true
  evaluation_interval: 30s
  rules:
    - name: "HighCPUUsage"
      condition: "avg(cpu_usage_percent) > 80"
      duration: 5m
      severity: "warning"
      annotations:
        summary: "High CPU usage detected"
        description: "CPU usage is above 80% for more than 5 minutes"
        
    - name: "StreamDown"
      condition: 'stream_status{status="error"} > 0'
      duration: 1m
      severity: "critical"
      annotations:
        summary: "Stream {{.stream_id}} is down"
        description: "Stream {{.stream_id}} has been in error state"
        
  notifications:
    email:
      enabled: true
      smtp_host: "smtp.gmail.com"
      smtp_port: 587
      username: "${SMTP_USERNAME}"
      password: "${SMTP_PASSWORD}"
      from: "alerts@example.com"
      to:
        - "ops@example.com"
        
    slack:
      enabled: true
      webhook_url: "${SLACK_WEBHOOK_URL}"
      channel: "#alerts"
      username: "Mirror Alerts"
      
    pagerduty:
      enabled: false
      integration_key: "${PAGERDUTY_KEY}"
      
    webhook:
      enabled: true
      url: "https://webhook.example.com/alerts"
      method: "POST"
      headers:
        Authorization: "Bearer ${WEBHOOK_TOKEN}"
```

## Testing Requirements

### Unit Tests
- API endpoint authentication and authorization
- WebSocket message handling and broadcasting
- Alert rule evaluation logic
- Metric aggregation accuracy
- React component rendering

### Integration Tests
- Full API workflow with authentication
- WebSocket connection lifecycle
- Alert firing and notification delivery
- Dashboard real-time updates
- Multi-user concurrent access

### E2E Tests
- Complete user flows (login → view streams → control stream)
- Real-time metric updates
- Alert acknowledgment workflow
- Mobile responsive behavior
- Cross-browser compatibility

## Monitoring Metrics

### Prometheus Metrics
```go
// API metrics
api_requests_total{endpoint, method, status}
api_request_duration_seconds{endpoint, method}
api_auth_failures_total{reason}
api_concurrent_requests{endpoint}

// WebSocket metrics
websocket_connections_active
websocket_messages_sent_total{type}
websocket_messages_received_total{type}
websocket_errors_total{error}

// Dashboard metrics
dashboard_page_views_total{page}
dashboard_active_users
dashboard_load_time_seconds{page}

// Alert metrics
alerts_active{severity}
alerts_evaluated_total
alerts_fired_total{rule}
notifications_sent_total{channel, status}
```

## Deliverables
1. Complete admin API with authentication
2. WebSocket server for real-time updates
3. React dashboard with Material-UI
4. Stream control interface
5. Real-time metrics visualization
6. Alert management system
7. Audit logging system
8. Mobile-responsive design
9. Comprehensive documentation

## Success Criteria
- Dashboard loads in <2 seconds
- Real-time updates with <100ms latency
- Support 100+ concurrent admin users
- All critical metrics visible at a glance
- Stream control actions complete in <1 second
- Alert notifications delivered within 30 seconds
- 99.9% uptime for admin interface
- All security requirements met

## Next Phase Preview
Phase 7 will focus on testing, optimization, and production deployment, including comprehensive load testing, performance tuning, security hardening, deployment automation, and operational documentation.
