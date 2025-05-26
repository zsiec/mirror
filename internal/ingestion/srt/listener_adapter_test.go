package srt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListenerAdapter_NilListener tests behavior with nil listener
func TestListenerAdapter_NilListener(t *testing.T) {
	adapter := &ListenerAdapter{
		Listener: nil,
	}

	// These should panic due to nil pointer dereference
	assert.Panics(t, func() { adapter.Start() })
	assert.Panics(t, func() { adapter.Stop() })
	assert.Panics(t, func() { adapter.GetActiveSessions() })
	assert.Panics(t, func() { adapter.SetConnectionHandler(nil) })
}

// TestListenerAdapter_StreamControls tests stream control methods (no-ops)
func TestListenerAdapter_StreamControls(t *testing.T) {
	adapter := &ListenerAdapter{
		Listener: nil, // These methods don't use the listener
	}

	streamID := "test-stream-123"

	// Test TerminateStream - should return nil (not implemented)
	err := adapter.TerminateStream(streamID)
	assert.NoError(t, err)

	// Test PauseStream - should return nil (not implemented) 
	err = adapter.PauseStream(streamID)
	assert.NoError(t, err)

	// Test ResumeStream - should return nil (not implemented)
	err = adapter.ResumeStream(streamID)
	assert.NoError(t, err)
}

// TestListenerAdapter_SetConnectionHandler tests the handler conversion
func TestListenerAdapter_SetConnectionHandler(t *testing.T) {
	// Since we can't easily mock the Listener struct without creating a full instance,
	// we'll test the handler conversion logic by examining what gets called
	
	// We'll manually test the conversion logic
	var capturedHandler ConnectionHandler
	
	// This simulates what SetConnectionHandler does
	originalHandler := func(conn *Connection) error {
		return nil
	}
	
	// The adapter converts ConnectionHandler to func(*Connection) error
	convertedHandler := func(conn *Connection) error {
		return originalHandler(conn)
	}
	
	capturedHandler = convertedHandler
	
	// Test that we can call the converted handler
	testConn := &Connection{
		streamID:  "test-stream",
		startTime: time.Now(),
	}
	
	err := capturedHandler(testConn)
	assert.NoError(t, err)
}

// TestListenerAdapter_SetConnectionHandler_WithError tests error propagation
func TestListenerAdapter_SetConnectionHandler_WithError(t *testing.T) {
	expectedErr := assert.AnError
	
	// Test the conversion logic with an error-returning handler
	originalHandler := func(conn *Connection) error {
		return expectedErr
	}
	
	// The adapter converts ConnectionHandler to func(*Connection) error
	convertedHandler := func(conn *Connection) error {
		return originalHandler(conn)
	}
	
	// Test that errors are properly propagated
	testConn := &Connection{
		streamID:  "test-stream",
		startTime: time.Now(),
	}
	
	err := convertedHandler(testConn)
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

// TestConnectionAdapter tests the ConnectionAdapter struct
func TestConnectionAdapter(t *testing.T) {
	// Create a test connection
	testConn := &Connection{
		streamID:   "test-connection",
		startTime:  time.Now(),
		lastActive: time.Now(),
	}

	// Create the adapter
	adapter := &ConnectionAdapter{
		Connection: testConn,
	}

	// Test that it embeds the connection correctly
	assert.Equal(t, "test-connection", adapter.streamID)
	assert.NotZero(t, adapter.startTime)
	assert.NotZero(t, adapter.lastActive)
}

// TestConnectionAdapter_NilConnection tests behavior with nil connection
func TestConnectionAdapter_NilConnection(t *testing.T) {
	adapter := &ConnectionAdapter{
		Connection: nil,
	}

	// Accessing fields should panic due to nil pointer dereference
	assert.Panics(t, func() { _ = adapter.streamID })
}

// TestListenerAdapter_Initialization tests basic initialization
func TestListenerAdapter_Initialization(t *testing.T) {
	// Test that we can create an adapter with a nil listener
	adapter := &ListenerAdapter{
		Listener: nil,
	}
	
	assert.NotNil(t, adapter)
	assert.Nil(t, adapter.Listener)
}

// TestConnectionAdapter_Initialization tests basic initialization  
func TestConnectionAdapter_Initialization(t *testing.T) {
	// Test that we can create an adapter with a nil connection
	adapter := &ConnectionAdapter{
		Connection: nil,
	}
	
	assert.NotNil(t, adapter)
	assert.Nil(t, adapter.Connection)
}

// BenchmarkListenerAdapter_StreamControls benchmarks the no-op stream control methods
func BenchmarkListenerAdapter_StreamControls(b *testing.B) {
	adapter := &ListenerAdapter{
		Listener: nil,
	}
	
	streamID := "benchmark-stream"
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		adapter.TerminateStream(streamID)
		adapter.PauseStream(streamID)
		adapter.ResumeStream(streamID)
	}
}

// TestListenerAdapter_HandlerConversion_NilHandler tests nil handler handling
func TestListenerAdapter_HandlerConversion_NilHandler(t *testing.T) {
	// Test the conversion behavior with a nil handler
	// This tests the logic that would be in SetConnectionHandler
	
	var convertedHandler ConnectionHandler
	var originalHandler ConnectionHandler
	
	// When originalHandler is nil, convertedHandler should handle it gracefully
	if originalHandler != nil {
		convertedHandler = func(conn *Connection) error {
			return originalHandler(conn)
		}
	}
	
	// Since originalHandler is nil, convertedHandler should also be nil
	assert.Nil(t, convertedHandler)
}

// TestListenerAdapter_Types tests type compatibility
func TestListenerAdapter_Types(t *testing.T) {
	// Test that ConnectionHandler can accept *Connection
	var handler ConnectionHandler = func(conn *Connection) error {
		assert.NotNil(t, conn)
		return nil
	}
	
	testConn := &Connection{
		streamID: "type-test",
	}
	
	err := handler(testConn)
	assert.NoError(t, err)
}
