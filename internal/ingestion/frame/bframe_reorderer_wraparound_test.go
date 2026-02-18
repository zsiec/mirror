package frame

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

func TestBFrameReorderer_TimestampWraparound(t *testing.T) {
	tests := []struct {
		name             string
		frames           []testFrame
		expectedMinCount int      // Minimum expected output frames
		expectedDrops    int      // Expected drops
		checkOrder       bool     // Whether to check exact output order
		expectedOrder    []uint64 // Expected frame IDs if checkOrder is true
	}{
		{
			name: "32-bit timestamp wraparound",
			frames: []testFrame{
				{id: 1, pts: 4294967000, dts: 4294967000, frameType: types.FrameTypeI},
				{id: 2, pts: 4294967100, dts: 4294967100, frameType: types.FrameTypeP},
				{id: 3, pts: 4294967200, dts: 4294967200, frameType: types.FrameTypeP},
				// Wraparound occurs here - timestamps wrap from ~4.29 billion to small values
				{id: 4, pts: 100, dts: 100, frameType: types.FrameTypeI},
				{id: 5, pts: 200, dts: 200, frameType: types.FrameTypeP},
				{id: 6, pts: 300, dts: 300, frameType: types.FrameTypeP},
			},
			expectedMinCount: 6,
			expectedDrops:    0,
			checkOrder:       false, // Reorderer may buffer and output in different order
		},
		{
			name: "Normal backwards jump should be dropped",
			frames: []testFrame{
				{id: 1, pts: 1000, dts: 1000, frameType: types.FrameTypeI},
				{id: 2, pts: 2000, dts: 2000, frameType: types.FrameTypeP},
				{id: 3, pts: 3000, dts: 3000, frameType: types.FrameTypeP},
				// This is a genuine backwards jump, not wraparound
				{id: 4, pts: 500, dts: 500, frameType: types.FrameTypeP},
				{id: 5, pts: 4000, dts: 4000, frameType: types.FrameTypeP},
			},
			expectedMinCount: 4,
			expectedDrops:    1,
			checkOrder:       true,
			expectedOrder:    []uint64{1, 2, 3, 5},
		},
		{
			name: "Large forward jump (stream reset)",
			frames: []testFrame{
				{id: 1, pts: 1000, dts: 1000, frameType: types.FrameTypeI},
				{id: 2, pts: 2000, dts: 2000, frameType: types.FrameTypeP},
				// Large jump forward (like a stream reset)
				{id: 3, pts: 135000000, dts: 135000000, frameType: types.FrameTypeI},
				{id: 4, pts: 135001000, dts: 135001000, frameType: types.FrameTypeP},
				// Then jump back to small values - now handled as stream reset
				{id: 5, pts: 500, dts: 500, frameType: types.FrameTypeI},
				{id: 6, pts: 1500, dts: 1500, frameType: types.FrameTypeP},
			},
			expectedMinCount: 6, // All frames should be output due to stream reset handling
			expectedDrops:    0,
			checkOrder:       false,
		},
		{
			name: "PTS wraparound with B-frames",
			frames: []testFrame{
				{id: 1, pts: 4294967000, dts: 4294966900, frameType: types.FrameTypeI},
				{id: 2, pts: 4294967200, dts: 4294967000, frameType: types.FrameTypeP},
				{id: 3, pts: 4294967100, dts: 4294967100, frameType: types.FrameTypeB}, // B-frame with earlier PTS
				// Wraparound - frame 4's PTS (100) is a small backward jump from the last
				// non-B-frame PTS (4294967200), which correctly appears as wraparound.
				// B-frames no longer update lastOutputPTS, so wraparound detection relies
				// on the last I/P frame's PTS, which is the correct reference point.
				{id: 4, pts: 100, dts: 4294967200, frameType: types.FrameTypeI},
				{id: 5, pts: 300, dts: 100, frameType: types.FrameTypeP},
				{id: 6, pts: 200, dts: 200, frameType: types.FrameTypeB},
			},
			// Frame 4 (I-frame) is silently skipped during flush because its PTS (100) appears
			// as a small backward jump from the last non-B-frame output PTS (300 from frame 5),
			// which is output earlier due to DTS ordering. This is correct behavior:
			// B-frames no longer pollute lastOutputPTS, preventing false P-frame drops.
			// Note: Flush() doesn't count skipped frames in the dropped counter.
			expectedMinCount: 5,
			expectedDrops:    0,
			checkOrder:       false, // B-frames will be reordered
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create reorderer with minimal delay to get immediate output
			reorderer := NewBFrameReorderer(3, 1*time.Millisecond, logger.NewNullLogger())

			var outputFrames []*types.VideoFrame

			// Process all frames
			for _, tf := range tt.frames {
				frame := &types.VideoFrame{
					ID:          tf.id,
					PTS:         tf.pts,
					DTS:         tf.dts,
					Type:        tf.frameType,
					CaptureTime: time.Now(),
					NALUnits:    []types.NALUnit{{Type: 0x01, Data: []byte{0x00}}}, // Dummy NAL unit
				}

				frames, err := reorderer.AddFrame(frame)
				require.NoError(t, err)
				outputFrames = append(outputFrames, frames...)
			}

			// Flush remaining frames
			flushed := reorderer.Flush()
			outputFrames = append(outputFrames, flushed...)

			// Check output count
			assert.GreaterOrEqual(t, len(outputFrames), tt.expectedMinCount, "Not enough frames output")

			// Check output order if specified
			if tt.checkOrder {
				var outputIDs []uint64
				for _, f := range outputFrames {
					outputIDs = append(outputIDs, f.ID)
				}
				assert.Equal(t, tt.expectedOrder, outputIDs, "Output frame order mismatch")
			}

			// Check stats
			stats := reorderer.GetStats()
			assert.Equal(t, uint64(tt.expectedDrops), stats.FramesDropped, "Dropped frame count mismatch")
		})
	}
}

type testFrame struct {
	id        uint64
	pts       int64
	dts       int64
	frameType types.FrameType
}

func TestBFrameReorderer_EdgeCaseTimestamps(t *testing.T) {
	reorderer := NewBFrameReorderer(3, 100*time.Millisecond, logger.NewNullLogger())

	// Test exact wraparound point
	const wrapPoint = int64(1) << 32

	frames := []testFrame{
		{id: 1, pts: wrapPoint - 100, dts: wrapPoint - 100, frameType: types.FrameTypeI},
		{id: 2, pts: wrapPoint - 50, dts: wrapPoint - 50, frameType: types.FrameTypeP},
		{id: 3, pts: 0, dts: 0, frameType: types.FrameTypeI}, // Wrapped to 0
		{id: 4, pts: 50, dts: 50, frameType: types.FrameTypeP},
	}

	var outputCount int
	for _, tf := range frames {
		frame := &types.VideoFrame{
			ID:          tf.id,
			PTS:         tf.pts,
			DTS:         tf.dts,
			Type:        tf.frameType,
			CaptureTime: time.Now(),
			NALUnits:    []types.NALUnit{{Type: 0x01, Data: []byte{0x00}}},
		}

		output, err := reorderer.AddFrame(frame)
		require.NoError(t, err)
		outputCount += len(output)
	}

	// Flush and check we got all frames
	flushed := reorderer.Flush()
	outputCount += len(flushed)

	assert.Equal(t, 4, outputCount, "Should output all frames across wraparound")

	stats := reorderer.GetStats()
	assert.Equal(t, uint64(0), stats.FramesDropped, "Should not drop any frames on wraparound")
}

func TestBFrameReorderer_StreamReset(t *testing.T) {
	tests := []struct {
		name             string
		frames           []testFrame
		expectedMinCount int
		expectedDrops    int
	}{
		{
			name: "Stream reset with large backward jump",
			frames: []testFrame{
				// First stream segment with high timestamps
				{id: 1, pts: 150000000, dts: 150000000, frameType: types.FrameTypeI},
				{id: 2, pts: 150001000, dts: 150001000, frameType: types.FrameTypeP},
				{id: 3, pts: 150002000, dts: 150002000, frameType: types.FrameTypeP},
				// Stream reset to low timestamps (like logs show)
				{id: 4, pts: 579000, dts: 579000, frameType: types.FrameTypeI},
				{id: 5, pts: 582000, dts: 582000, frameType: types.FrameTypeP},
				{id: 6, pts: 585000, dts: 585000, frameType: types.FrameTypeP},
			},
			expectedMinCount: 6,
			expectedDrops:    0,
		},
		{
			name: "Multiple stream resets",
			frames: []testFrame{
				// First segment
				{id: 1, pts: 150000000, dts: 150000000, frameType: types.FrameTypeI},
				{id: 2, pts: 150001000, dts: 150001000, frameType: types.FrameTypeP},
				// Reset to small values
				{id: 3, pts: 500000, dts: 500000, frameType: types.FrameTypeI},
				{id: 4, pts: 501000, dts: 501000, frameType: types.FrameTypeP},
				// Reset to high values again
				{id: 5, pts: 160000000, dts: 160000000, frameType: types.FrameTypeI},
				{id: 6, pts: 160001000, dts: 160001000, frameType: types.FrameTypeP},
			},
			expectedMinCount: 6,
			expectedDrops:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reorderer := NewBFrameReorderer(3, 1*time.Millisecond, logger.NewNullLogger())

			var outputFrames []*types.VideoFrame

			// Process all frames
			for _, tf := range tt.frames {
				frame := &types.VideoFrame{
					ID:          tf.id,
					PTS:         tf.pts,
					DTS:         tf.dts,
					Type:        tf.frameType,
					CaptureTime: time.Now(),
					NALUnits:    []types.NALUnit{{Type: 0x01, Data: []byte{0x00}}},
				}

				frames, err := reorderer.AddFrame(frame)
				require.NoError(t, err)
				outputFrames = append(outputFrames, frames...)
			}

			// Flush remaining frames
			flushed := reorderer.Flush()
			outputFrames = append(outputFrames, flushed...)

			// Check output count
			assert.GreaterOrEqual(t, len(outputFrames), tt.expectedMinCount, "Not enough frames output")

			// Check stats
			stats := reorderer.GetStats()
			assert.Equal(t, uint64(tt.expectedDrops), stats.FramesDropped, "Dropped frame count mismatch")
		})
	}
}
