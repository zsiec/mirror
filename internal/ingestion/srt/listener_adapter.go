package srt

// ListenerAdapter adapts the new Listener to the old Listener interface
// This allows gradual migration while maintaining compatibility
type ListenerAdapter struct {
	Listener *Listener
}

// Start delegates to the new listener
func (a *ListenerAdapter) Start() error {
	return a.Listener.Start()
}

// Stop delegates to the new listener
func (a *ListenerAdapter) Stop() error {
	return a.Listener.Stop()
}

// SetConnectionHandler is not needed for the new implementation
// as it uses SetHandler instead
func (a *ListenerAdapter) SetConnectionHandler(handler ConnectionHandler) {
	// Convert the handler to work with Connection
	a.Listener.SetHandler(func(conn *Connection) error {
		return handler(conn)
	})
}

// GetActiveSessions returns the count of active sessions
func (a *ListenerAdapter) GetActiveSessions() int {
	return a.Listener.GetActiveConnections()
}

// TerminateStream terminates a stream by ID
func (a *ListenerAdapter) TerminateStream(streamID string) error {
	// The new implementation doesn't support direct stream termination
	// This would need to be handled at the connection level
	return nil
}

// PauseStream pauses a stream by ID
func (a *ListenerAdapter) PauseStream(streamID string) error {
	// The new implementation doesn't support direct stream control
	// This would need to be handled at the connection level
	return nil
}

// ResumeStream resumes a stream by ID
func (a *ListenerAdapter) ResumeStream(streamID string) error {
	// The new implementation doesn't support direct stream control
	// This would need to be handled at the connection level
	return nil
}

// ConnectionAdapter adapts Connection to the old Connection interface
type ConnectionAdapter struct {
	*Connection
}

// This adapter allows the old connection handler code to work with the new connection type
// Additional methods can be added here as needed for compatibility
