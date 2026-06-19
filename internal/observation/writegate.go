package observation

// WriteGate runs post-write checks (e.g. near-duplicate detection) for an observation.
type WriteGate interface {
	CheckAsync(observation Observation)
}

type noopWriteGate struct{}

func (noopWriteGate) CheckAsync(Observation) {}

// NewNoopWriteGate returns a WriteGate that performs no checks.
func NewNoopWriteGate() WriteGate {
	return noopWriteGate{}
}
