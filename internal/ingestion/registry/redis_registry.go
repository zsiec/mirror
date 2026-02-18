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
func NewRedisRegistry(client *redis.Client, logger *logrus.Logger, ttl time.Duration) *RedisRegistry {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &RedisRegistry{
		client: client,
		logger: logger,
		prefix: "mirror:streams:",
		ttl:    ttl,
	}
}

// Register adds a new stream to the registry
func (r *RedisRegistry) Register(ctx context.Context, stream *Stream) error {
	// Check if stream already exists to preserve CreatedAt timestamp
	key := r.prefix + stream.ID
	existingData, err := r.client.Get(ctx, key).Bytes()
	if err == nil {
		// Stream exists, preserve CreatedAt but update LastHeartbeat
		var existingStream Stream
		if err := json.Unmarshal(existingData, &existingStream); err == nil {
			stream.CreatedAt = existingStream.CreatedAt // Preserve original creation time
		}
	} else if err == redis.Nil {
		// New stream, set CreatedAt
		stream.CreatedAt = time.Now()
		existingData = nil // Ensure we treat as new stream
	} else {
		// Actual Redis error — propagate it
		return fmt.Errorf("failed to check existing stream: %w", err)
	}
	stream.LastHeartbeat = time.Now()

	data, err := json.Marshal(stream)
	if err != nil {
		return fmt.Errorf("failed to marshal stream: %w", err)
	}

	// Check if this is a new stream or update
	if existingData == nil {
		// New stream - use Lua script for atomic SetNX + SAdd
		registerScript := redis.NewScript(`
			local key = KEYS[1]
			local active_key = KEYS[2]
			local data = ARGV[1]
			local ttl = tonumber(ARGV[2])
			local stream_id = ARGV[3]
			local ok = redis.call('SET', key, data, 'PX', ttl, 'NX')
			if not ok then
				return 0
			end
			redis.call('SADD', active_key, stream_id)
			return 1
		`)

		result, err := registerScript.Run(ctx, r.client,
			[]string{key, r.prefix + "active"},
			data, r.ttl.Milliseconds(), stream.ID).Int()
		if err != nil {
			return fmt.Errorf("failed to register stream: %w", err)
		}
		if result == 0 {
			return fmt.Errorf("stream %s already exists", stream.ID)
		}

		r.logger.WithFields(logrus.Fields{
			"stream_id": stream.ID,
			"type":      stream.Type,
			"source":    stream.SourceAddr,
		}).Info("Stream registered")
	} else {
		// Existing stream - just update with new TTL
		if err := r.client.Set(ctx, key, data, r.ttl).Err(); err != nil {
			return fmt.Errorf("failed to update stream: %w", err)
		}

		r.logger.WithFields(logrus.Fields{
			"stream_id": stream.ID,
		}).Debug("Stream updated")
	}

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

	// Use Lua script for atomic read-modify-write
	script := redis.NewScript(`
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local now = ARGV[2]
		local data = redis.call('GET', key)
		if not data then
			return redis.error_reply("stream not found")
		end
		local stream = cjson.decode(data)
		stream.last_heartbeat = now
		local updated = cjson.encode(stream)
		redis.call('SET', key, updated, 'PX', ttl)
		return "OK"
	`)

	ttlMs := r.ttl.Milliseconds()
	now := time.Now().Format(time.RFC3339Nano)

	_, err := script.Run(ctx, r.client, []string{key}, ttlMs, now).Result()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("stream %s not found", streamID)
		}
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}

	return nil
}

// UpdateStatus updates the status of a stream
func (r *RedisRegistry) UpdateStatus(ctx context.Context, streamID string, status StreamStatus) error {
	key := r.prefix + streamID

	// Use Lua script for atomic read-modify-write
	script := redis.NewScript(`
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local status = ARGV[2]
		local now = ARGV[3]
		local data = redis.call('GET', key)
		if not data then
			return redis.error_reply("stream not found")
		end
		local stream = cjson.decode(data)
		stream.status = status
		stream.last_heartbeat = now
		local updated = cjson.encode(stream)
		redis.call('SET', key, updated, 'PX', ttl)
		return "OK"
	`)

	ttlMs := r.ttl.Milliseconds()
	now := time.Now().Format(time.RFC3339Nano)

	_, err := script.Run(ctx, r.client, []string{key}, ttlMs, string(status), now).Result()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("stream %s not found", streamID)
		}
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

	// Marshal the stats to pass to Lua script
	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("failed to marshal stats: %w", err)
	}

	// Use Lua script for atomic read-modify-write
	// Stats fields are at the top level of the Stream struct, not nested
	script := redis.NewScript(`
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local stats_json = ARGV[2]
		local now = ARGV[3]
		local data = redis.call('GET', key)
		if not data then
			return redis.error_reply("stream not found")
		end
		local stream = cjson.decode(data)
		local stats = cjson.decode(stats_json)
		stream.bytes_received = stats.BytesReceived
		stream.packets_received = stats.PacketsReceived
		stream.packets_lost = stats.PacketsLost
		stream.bitrate = stats.Bitrate
		stream.last_heartbeat = now
		local updated = cjson.encode(stream)
		redis.call('SET', key, updated, 'PX', ttl)
		return "OK"
	`)

	ttlMs := r.ttl.Milliseconds()
	now := time.Now().Format(time.RFC3339Nano)

	_, err = script.Run(ctx, r.client, []string{key}, ttlMs, string(statsJSON), now).Result()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("stream %s not found", streamID)
		}
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

	// Use XX flag to only update if key already exists (prevents ghost streams)
	ok, err := r.client.SetXX(ctx, key, data, r.ttl).Result()
	if err != nil {
		return fmt.Errorf("failed to update stream: %w", err)
	}
	if !ok {
		return fmt.Errorf("stream %s not found", stream.ID)
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
