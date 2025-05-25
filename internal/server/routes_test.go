package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/pkg/version"
)

func TestHandleVersion(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	req, err := http.NewRequest("GET", "/version", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(server.handleVersion)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var versionInfo version.Info
	err = json.Unmarshal(rr.Body.Bytes(), &versionInfo)
	require.NoError(t, err)
	assert.NotEmpty(t, versionInfo.Version)
	assert.NotEmpty(t, versionInfo.GoVersion)
}

func TestHandleStreamsPlaceholder(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	req, err := http.NewRequest("GET", "/api/v1/streams", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(server.handleStreamsPlaceholder)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var response map[string]string
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "Streams endpoint requires ingestion to be enabled", response["message"])
	assert.Equal(t, "2", response["phase"])
}

func TestWriteJSON(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	rr := httptest.NewRecorder()
	testData := map[string]string{
		"key": "value",
	}

	err := server.writeJSON(rr, http.StatusCreated, testData)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var result map[string]string
	err = json.Unmarshal(rr.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, testData, result)
}
