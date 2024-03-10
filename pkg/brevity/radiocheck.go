package brevity

// RadioCheckRequest is a request for a RADIO CHECK.
type RadioCheckRequest interface {
	// Callsign of the friendly aircraft requesting the RADIO CHECK.
	Callsign() string
}

// RadioCheckResponse is a response to a RADIO CHECK.
type RadioCheckResponse interface {
	// Callsign of the friendly aircraft requesting the RADIO CHECK.
	// If the callsign was misheard, this may not be the actual callsign of any actual aircraft.
	Callsign() string
	// Status is true if the RADIO CHECK was correlated to an aircraft on frequency, otherwise false.
	// If this is false, the RADIO CHECK was received but not fully understood.
	Status() bool
}
