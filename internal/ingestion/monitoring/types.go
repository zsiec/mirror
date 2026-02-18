package monitoring

// Packet represents a network packet for monitoring purposes
// TODO: Move to types package when fully defined
type Packet struct {
	SequenceNumber uint16
	Timestamp      uint32
	Data           []byte
}
