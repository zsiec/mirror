package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// RedisRegistry implements Registry interface using Redis as backend
type RedisRegistry struct {
	client *redis.Client
	logger *logrus.Logger
	prefix string
	ttl    time.Duration
}

// NewRedisRegistry creates a new Redis-backed registry
func NewRedisRegistry(client *redis.Client, logger *logrus.Logger) *RedisRegistry {
	return &RedisRegistry{
		client: client,
		logger: logger,
		prefix: "mirror:streams:",
		ttl:    5 * time.Minute,
	}
}

// Register adds a new stream to the registry
func (r *RedisRegistry) Register(ctx context.Context, stream *Stream) error {
	stream.CreatedAt = time.Now()
	stream.LastHeartbeat = time.Now()

	data, err := json.Marshal(stream)
	if err != nil {
		return fmt.Errorf("failed to marshal stream: %w", err)
	}

	key := r.prefix + stream.ID

	// Use SET with NX to prevent overwriting existing streams
	ok, err := r.client.SetNX(ctx, key, data, r.ttl).Result()
	if err != nil {
		return fmt.Errorf("failed to register stream: %w", err)
	}
	if !ok {
		return fmt.Errorf("stream %s already exists", stream.ID)
	}

	// Add to active streams set
	if err := r.client.SAdd(ctx, r.prefix+"active", stream.ID).Err(); err != nil {
		// Rollback the registration
		r.client.Del(ctx, key)
		return fmt.Errorf("failed to add to active set: %w", err)
	}

	r.logger.WithFields(logrus.Fields{
		"stream_id": stream.ID,
		"type":      stream.Type,
		"source":    stream.SourceAddr,
	}).Info("Stream registered")

	return nil
}

// Unregister removes a stream from the registry
func (r *RedisRegistry) Unregister(ctx context.Context, streamID string) error {
	key := r.prefix + streamID

	// Remove from Redis
	deleted, err := r.client.Del(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("failed to unregister stream: %w", err)
	}

	if deleted == 0 {
		return fmt.Errorf("stream %s not found", streamID)
	}

	// Remove from active set
	if err := r.client.SRem(ctx, r.prefix+"active", streamID).Err(); err != nil {
		r.logger.Warnf("Failed to remove stream %s from active set: %v", streamID, err)
	}

	r.logger.WithField("stream_id", streamID).Info("Stream unregistered")

	return nil
}

// Get retrieves a stream by ID
func (r *RedisRegistry) Get(ctx context.Context, streamID string) (*Stream, error) {
	key := r.prefix + streamID

	data, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("stream %s not found", streamID)
		}
		return nil, fmt.Errorf("failed to get stream: %w", err)
	}

	var stream Stream
	if err := json.Unmarshal(data, &stream); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stream: %w", err)
	}

	return &stream, nil
}

// List returns all active streams
func (r *RedisRegistry) List(ctx context.Context) ([]*Stream, error) {
	// Use Lua script for atomic operation
	script := redis.NewScript(`
		local active_key = KEYS[1]
		local prefix = ARGV[1]
		local active = redis.call('SMEMBERS', active_key)
		local result = {}
		local to_remove = {}
		
		for i, id in ipairs(active) do
			local stream = redis.call('GET', prefix .. id)
			if stream then
				table.insert(result, stream)
			else
				-- Track streams that need to be removed from active set
				table.insert(to_remove, id)
			end
		end
		
		-- Clean up expired streams from active set
		for i, id in ipairs(to_remove) do
			redis.call('SREM', active_key, id)
		end
		
		return result
	`)
	
	res, err := script.Run(ctx, r.client, []string{r.prefix + "active"}, r.prefix).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list streams: %w", err)
	}
	
	// Parse results
	values, ok := res.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected result type from script")
	}
	
	streams := make([]*Stream, 0, len(values))
	for _, val := range values {
		data, ok := val.(string)
		if !ok {
			r.logger.Warn("Invalid data type in result")
			continue
		}
		
		var stream Stream
		if err := json.Unmarshal([]byte(data), &stream); err != nil {
			r.logger.WithError(err).Warn("Failed to unmarshal stream")
			continue
		}
		
		streams = append(streams, &stream)
	}
	
	return streams, nil
}

// ListPaginated returns a paginated list of active streams
func (r *RedisRegistry) ListPaginated(ctx context.Context, cursor uint64, count int64) ([]*Stream, uint64, error) {
	// Use SSCAN for pagination
	streamIDs, nextCursor, err := r.client.SScan(ctx, r.prefix+"active", cursor, "*", count).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to scan streams: %w", err)
	}
	
	if len(streamIDs) == 0 {
		return []*Stream{}, nextCursor, nil
	}
	
	// Build keys
	keys := make([]string, len(streamIDs))
	for i, id := range streamIDs {
		keys[i] = r.prefix + id
	}
	
	// Use pipeline for atomic batch get
	pipe := r.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.Get(ctx, key)
	}
	
	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, 0, fmt.Errorf("failed to get streams: %w", err)
	}
	
	streams := make([]*Stream, 0, len(cmds))
	toRemove := make([]string, 0)
	
	for i, cmd := range cmds {
		data, err := cmd.Result()
		if err == redis.Nil {
			// Stream expired or deleted, track for removal
			toRemove = append(toRemove, streamIDs[i])
			continue
		} else if err != nil {
			r.logger.WithError(err).Warnf("Failed to get stream %s", streamIDs[i])
			continue
		}
		
		var stream Stream
		if err := json.Unmarshal([]byte(data), &stream); err != nil {
			r.logger.WithError(err).Warnf("Failed to unmarshal stream %s", streamIDs[i])
			continue
		}
		
		streams = append(streams, &stream)
	}
	
	// Clean up expired streams
	if len(toRemove) > 0 {
		// Convert []string to []interface{} for SRem
		toRemoveInterface := make([]interface{}, len(toRemove))
		for i, id := range toRemove {
			toRemoveInterface[i] = id
		}
		if err := r.client.SRem(ctx, r.prefix+"active", toRemoveInterface...).Err(); err != nil {
			r.logger.WithError(err).Warn("Failed to remove expired streams from active set")
		}
	}
	
	return streams, nextCursor, nil
}

// UpdateHeartbeat updates the heartbeat timestamp for a stream
func (r *RedisRegistry) UpdateHeartbeat(ctx context.Context, streamID string) error {
	key := r.prefix + streamID

	// Get current stream data
	data, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("stream %s not found", streamID)
		}
		return fmt.Errorf("failed to get stream: %w", err)
	}

	var stream Stream
	if err := json.Unmarshal(data, &stream); err != nil {
		return fmt.Errorf("failed to unmarshal stream: %w", err)
	}

	// Update heartbeat
	stream.LastHeartbeat = time.Now()

	updatedData, err := json.Marshal(stream)
	if err != nil {
		return fmt.Errorf("failed to marshal stream: %w", err)
	}

	// Update with new TTL
	if err := r.client.Set(ctx, key, updatedData, r.ttl).Err(); err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}

	return nil
}

// UpdateStatus updates the status of a stream
func (r *RedisRegistry) UpdateStatus(ctx context.Context, streamID string, status StreamStatus) error {
	key := r.prefix + streamID

	// Get current stream data
	data, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("stream %s not found", streamID)
		}
		return fmt.Errorf("failed to get stream: %w", err)
	}

	var stream Stream
	if err := json.Unmarshal(data, &stream); err != nil {
		return fmt.Errorf("failed to unmarshal stream: %w", err)
	}

	// Update status
	stream.Status = status
	stream.LastHeartbeat = time.Now()

	updatedData, err := json.Marshal(stream)
	if err != nil {
		return fmt.Errorf("failed to marshal stream: %w", err)
	}

	// Update with new TTL
	if err := r.client.Set(ctx, key, updatedData, r.ttl).Err(); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	r.logger.WithFields(logrus.Fields{
		"stream_id": streamID,
		"status":    status,
	}).Debug("Stream status updated")

	return nil
}

// UpdateStats updates the statistics for a stream
func (r *RedisRegistry) UpdateStats(ctx context.Context, streamID string, stats *StreamStats) error {
	key := r.prefix + streamID

	// Get current stream data
	data, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("stream %s not found", streamID)
		}
		return fmt.Errorf("failed to get stream: %w", err)
	}

	var stream Stream
	if err := json.Unmarshal(data, &stream); err != nil {
		return fmt.Errorf("failed to unmarshal stream: %w", err)
	}

	// Update stats
	stream.BytesReceived = stats.BytesReceived
	stream.PacketsReceived = stats.PacketsReceived
	stream.PacketsLost = stats.PacketsLost
	stream.Bitrate = stats.Bitrate
	stream.LastHeartbeat = time.Now()

	updatedData, err := json.Marshal(stream)
	if err != nil {
		return fmt.Errorf("failed to marshal stream: %w", err)
	}

	// Update with new TTL
	if err := r.client.Set(ctx, key, updatedData, r.ttl).Err(); err != nil {
		return fmt.Errorf("failed to update stats: %w", err)
	}

	return nil
}

// Delete removes a stream from the registry (alias for Unregister)
func (r *RedisRegistry) Delete(ctx context.Context, streamID string) error {
	return r.Unregister(ctx, streamID)
}

// Update updates an existing stream in the registry
func (r *RedisRegistry) Update(ctx context.Context, stream *Stream) error {
	if stream == nil {
		return fmt.Errorf("stream cannot be nil")
	}
	
	key := r.prefix + stream.ID
	
	// Update heartbeat
	stream.LastHeartbeat = time.Now()
	
	data, err := json.Marshal(stream)
	if err != nil {
		return fmt.Errorf("failed to marshal stream: %w", err)
	}
	
	// Update with TTL
	if err := r.client.Set(ctx, key, data, r.ttl).Err(); err != nil {
		return fmt.Errorf("failed to update stream: %w", err)
	}
	
	r.logger.WithField("stream_id", stream.ID).Debug("Stream updated")
	return nil
}

// Close closes the Redis client connection
func (r *RedisRegistry) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}
