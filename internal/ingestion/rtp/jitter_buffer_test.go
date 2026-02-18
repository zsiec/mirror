package rtp

import (
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJitterBuffer_Basic(t *testing.T) {
	jb := NewJitterBuffer(100, 50*time.Millisecond, 90000)

	// Add packets with same timestamp (simulating burst)
	for i := 0; i < 5; i++ {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: uint16(i),
				Timestamp:      0, // All same timestamp
			},
			Payload: []byte{byte(i)},
		}
		err := jb.Add(packet)
		require.NoError(t, err)
	}

	// Check initial stats
	stats := jb.GetStats()
	assert.Equal(t, uint64(5), stats.PacketsBuffered)
	assert.Equal(t, 5, stats.CurrentDepth)

	// Wait for target delay
	time.Sleep(60 * time.Millisecond)

	// Get all packets (they should all be ready since they have same timestamp)
	packets, err := jb.Get()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(packets), 1)

	// Drain remaining packets
	for jb.GetCurrentDepth() > 0 {
		time.Sleep(10 * time.Millisecond)
		morePackets, err := jb.Get()
		require.NoError(t, err)
		packets = append(packets, morePackets...)
	}

	// Verify we got all packets
	assert.Equal(t, 5, len(packets))

	// Check final stats
	stats = jb.GetStats()
	assert.Equal(t, uint64(5), stats.PacketsDelivered)
	assert.Equal(t, 0, stats.CurrentDepth)
}

func TestJitterBuffer_OutOfOrder(t *testing.T) {
	jb := NewJitterBuffer(100, 50*time.Millisecond, 90000)

	// Add packets out of order with same timestamp
	sequences := []uint16{2, 0, 4, 1, 3}
	for _, seq := range sequences {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: seq,
				Timestamp:      0, // Same timestamp for all
			},
			Payload: []byte{byte(seq)},
		}
		err := jb.Add(packet)
		require.NoError(t, err)
	}

	// Wait for target delay
	time.Sleep(60 * time.Millisecond)

	// Collect all packets
	var allPackets []*rtp.Packet
	for i := 0; i < 10 && len(allPackets) < 5; i++ {
		packets, err := jb.Get()
		require.NoError(t, err)
		allPackets = append(allPackets, packets...)
		if len(allPackets) < 5 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Verify we got all packets in correct order
	require.Equal(t, 5, len(allPackets))
	for i, p := range allPackets {
		assert.Equal(t, uint16(i), p.SequenceNumber)
	}
}

func TestJitterBuffer_SequenceWrapAround(t *testing.T) {
	jb := NewJitterBuffer(100, 50*time.Millisecond, 90000)

	// Add packets around wraparound point
	sequences := []uint16{65534, 65535, 0, 1, 2}
	for i, seq := range sequences {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: seq,
				Timestamp:      uint32(i * 3000),
			},
			Payload: []byte{byte(i)},
		}
		err := jb.Add(packet)
		require.NoError(t, err)
	}

	// Wait and collect packets as they become ready
	time.Sleep(60 * time.Millisecond)

	var allPackets []*rtp.Packet
	timeout := time.After(2 * time.Second)
	for len(allPackets) < 5 {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for packets, got %d of 5", len(allPackets))
		default:
			packets, err := jb.Get()
			require.NoError(t, err)
			if len(packets) > 0 {
				allPackets = append(allPackets, packets...)
			} else {
				time.Sleep(20 * time.Millisecond)
			}
		}
	}

	// Verify correct ordering across wraparound
	expectedSeqs := []uint16{65534, 65535, 0, 1, 2}
	for i, p := range allPackets {
		assert.Equal(t, expectedSeqs[i], p.SequenceNumber)
	}
}

func TestJitterBuffer_BufferOverflow(t *testing.T) {
	jb := NewJitterBuffer(5, 50*time.Millisecond, 90000)

	// Add more packets than buffer size
	for i := 0; i < 10; i++ {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: uint16(i),
				Timestamp:      uint32(i * 3000),
			},
			Payload: []byte{byte(i)},
		}
		err := jb.Add(packet)
		require.NoError(t, err)
	}

	// Check stats
	stats := jb.GetStats()
	assert.Equal(t, uint64(10), stats.PacketsBuffered)
	assert.Equal(t, uint64(5), stats.PacketsDropped) // Oldest 5 dropped
	assert.Equal(t, uint64(5), stats.OverrunCount)
	assert.Equal(t, 5, stats.CurrentDepth)
	assert.Equal(t, 5, stats.MaxDepth)

	// Wait for the remaining packets to be ready
	// Packets 5-9 have timestamps 15000-27000 (167ms-300ms from base)
	// With 50ms delay, packet 5 is ready at 217ms
	time.Sleep(220 * time.Millisecond)

	var allPackets []*rtp.Packet
	timeout := time.After(2 * time.Second)
	for len(allPackets) < 5 {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for packets, got %d of 5", len(allPackets))
		default:
			packets, err := jb.Get()
			require.NoError(t, err)
			if len(packets) > 0 {
				allPackets = append(allPackets, packets...)
			} else {
				time.Sleep(20 * time.Millisecond)
			}
		}
	}

	// Should get the 5 newest packets (5-9)
	assert.Len(t, allPackets, 5)
	for i, p := range allPackets {
		assert.Equal(t, uint16(i+5), p.SequenceNumber)
	}
}

func TestJitterBuffer_LatePackets(t *testing.T) {
	jb := NewJitterBuffer(100, 50*time.Millisecond, 90000)

	// Add first packet to establish base time
	packet1 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 0,
			Timestamp:      0,
		},
		Payload: []byte{0},
	}
	err := jb.Add(packet1)
	require.NoError(t, err)

	// Wait for first packet to be ready and get it
	time.Sleep(60 * time.Millisecond)
	packets, err := jb.Get()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(packets), 1)

	// Wait some more, then add a packet that should have been played earlier
	time.Sleep(100 * time.Millisecond)

	// Add a packet with timestamp that should have played 50ms ago
	packet2 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 1,
			Timestamp:      0, // Same timestamp as first packet, but arriving late
		},
		Payload: []byte{1},
	}
	err = jb.Add(packet2)
	require.NoError(t, err)

	// Try to get the late packet
	packets, err = jb.Get()
	require.NoError(t, err)

	// Check stats - should have at least one late packet
	stats := jb.GetStats()
	assert.GreaterOrEqual(t, stats.PacketsDelivered+stats.PacketsLate, uint64(2))
}

func TestJitterBuffer_Underrun(t *testing.T) {
	jb := NewJitterBuffer(100, 50*time.Millisecond, 90000)

	// Getting from an empty buffer before any packets have been delivered
	// should NOT count as underrun — underrun means the buffer ran dry
	// after it was previously producing packets.
	packets, err := jb.Get()
	require.NoError(t, err)
	assert.Nil(t, packets)

	stats := jb.GetStats()
	assert.Equal(t, uint64(0), stats.UnderrunCount, "no underrun expected before any packets delivered")

	// Add a packet and let it be delivered
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 1,
			Timestamp:      0,
		},
	}
	require.NoError(t, jb.Add(packet))
	time.Sleep(60 * time.Millisecond) // Wait for playout delay

	delivered, err := jb.Get()
	require.NoError(t, err)
	assert.NotEmpty(t, delivered)

	// Now getting from empty buffer after having delivered packets IS an underrun
	packets, err = jb.Get()
	require.NoError(t, err)
	assert.Nil(t, packets)

	stats = jb.GetStats()
	assert.Equal(t, uint64(1), stats.UnderrunCount, "underrun expected after buffer drained")

	// Subsequent empty Gets should NOT increment underrun count
	packets, err = jb.Get()
	require.NoError(t, err)
	assert.Nil(t, packets)

	stats = jb.GetStats()
	assert.Equal(t, uint64(1), stats.UnderrunCount, "repeated empty gets should not count as additional underruns")
}

func TestJitterBuffer_Flush(t *testing.T) {
	jb := NewJitterBuffer(100, 50*time.Millisecond, 90000)

	// Add some packets
	for i := 0; i < 5; i++ {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: uint16(i),
				Timestamp:      uint32(i * 3000),
			},
		}
		err := jb.Add(packet)
		require.NoError(t, err)
	}

	assert.Equal(t, 5, jb.GetCurrentDepth())

	// Flush buffer
	jb.Flush()

	assert.Equal(t, 0, jb.GetCurrentDepth())

	// Getting packets should return empty
	packets, err := jb.Get()
	require.NoError(t, err)
	assert.Nil(t, packets)
}

func TestJitterBuffer_TargetDelayUpdate(t *testing.T) {
	jb := NewJitterBuffer(100, 50*time.Millisecond, 90000)

	// Add a packet
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 0,
			Timestamp:      0,
		},
	}
	err := jb.Add(packet)
	require.NoError(t, err)

	// Update target delay
	jb.SetTargetDelay(100 * time.Millisecond)

	// Packet shouldn't be ready yet at 60ms
	time.Sleep(60 * time.Millisecond)
	packets, err := jb.Get()
	require.NoError(t, err)
	assert.Len(t, packets, 0)

	// Should be ready at 110ms
	time.Sleep(50 * time.Millisecond)
	packets, err = jb.Get()
	require.NoError(t, err)
	assert.Len(t, packets, 1)
}
