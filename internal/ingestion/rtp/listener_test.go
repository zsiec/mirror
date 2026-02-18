package rtp

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/logger"
)

func TestListener_NewListener(t *testing.T) {
	cfg := &config.RTPConfig{
		ListenAddr: "127.0.0.1",
		Port:       5004,
		RTCPPort:   5005,
		BufferSize: 65536,
	}

	codecsCfg := &config.CodecsConfig{
		Supported: []string{"h264", "hevc"},
		Preferred: "hevc",
	}

	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	registry := newMockRegistry()

	listener := NewListener(cfg, codecsCfg, registry, logger)

	assert.NotNil(t, listener)
	assert.Equal(t, cfg, listener.config)
	assert.Equal(t, codecsCfg, listener.codecsConfig)
	assert.NotNil(t, listener.sessions)
	assert.NotNil(t, listener.ctx)
	assert.NotNil(t, listener.cancel)
}

func TestListener_GetActiveSessions(t *testing.T) {
	listener := &Listener{
		sessions: make(map[string]*Session),
	}

	// Initially no sessions
	assert.Equal(t, 0, listener.GetActiveSessions())

	// Add some sessions
	listener.sessions["session1"] = &Session{}
	listener.sessions["session2"] = &Session{}
	listener.sessions["session3"] = &Session{}

	assert.Equal(t, 3, listener.GetActiveSessions())

	// Remove one
	delete(listener.sessions, "session2")
	assert.Equal(t, 2, listener.GetActiveSessions())
}

func TestListener_GetSessionStats(t *testing.T) {
	listener := &Listener{
		sessions: make(map[string]*Session),
	}

	// Add sessions with stats
	listener.sessions["stream1"] = &Session{
		streamID: "stream1",
		stats: &SessionStats{
			PacketsReceived: 100,
			BytesReceived:   10000,
		},
	}

	listener.sessions["stream2"] = &Session{
		streamID: "stream2",
		stats: &SessionStats{
			PacketsReceived: 200,
			BytesReceived:   20000,
		},
	}

	stats := listener.GetSessionStats()

	assert.Len(t, stats, 2)
	assert.Contains(t, stats, "stream1")
	assert.Contains(t, stats, "stream2")
	assert.Equal(t, uint64(100), stats["stream1"].PacketsReceived)
	assert.Equal(t, uint64(200), stats["stream2"].PacketsReceived)
}
