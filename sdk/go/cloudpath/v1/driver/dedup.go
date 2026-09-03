package driver

import (
	"fmt"
	"sort"
	"sync"
)

// SequenceTracker implements Core-side deduplication: for a given
// (plugin_instance_id, device_id) scope only a sequence strictly greater
// than the last accepted one is admitted. Duplicates and stale/out-of-order
// frames are therefore dropped at the boundary.
type SequenceTracker struct {
	mu   sync.Mutex
	last map[string]uint64
}

// NewSequenceTracker returns an empty tracker.
func NewSequenceTracker() *SequenceTracker {
	return &SequenceTracker{last: make(map[string]uint64)}
}

func (t *SequenceTracker) key(instanceID, deviceID string) string {
	return instanceID + "\x00" + deviceID
}

// Accept reports whether sequence is new for the scope and, when it is,
// records it. Sequence 0 is rejected because protocol messages must carry a
// positive sequence.
func (t *SequenceTracker) Accept(instanceID, deviceID string, sequence uint64) bool {
	if sequence == 0 {
		return false
	}
	key := t.key(instanceID, deviceID)
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.last[key]; ok && sequence <= prev {
		return false
	}
	t.last[key] = sequence
	return true
}

// Last returns the highest accepted sequence for the scope (0 if none).
func (t *SequenceTracker) Last(instanceID, deviceID string) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last[t.key(instanceID, deviceID)]
}

// AcceptMessage applies Accept to a DriverMessage's own scope fields.
func (t *SequenceTracker) AcceptMessage(m *DriverMessage) bool {
	if m == nil {
		return false
	}
	return t.Accept(m.PluginInstanceID, m.DeviceID, m.Sequence)
}

// Count returns how many distinct scopes have been observed.
func (t *SequenceTracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.last)
}

// Reset clears all scopes. Useful when a plugin restarts and replays from a
// fresh sequence space.
func (t *SequenceTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = make(map[string]uint64)
}

// NegotiateProtocolVersion picks the highest version that is supported by
// both sides and lies inside [minSupported, maxSupported]. It returns
// (0, false) when the sets are disjoint. prefer is the caller's preferred
// version and breaks ties when both sides support multiple versions.
func NegotiateProtocolVersion(supported []uint32, prefer uint32, minSupported, maxSupported uint32) (uint32, bool) {
	if minSupported > maxSupported {
		return 0, false
	}
	best := uint32(0)
	for _, v := range supported {
		if v < minSupported || v > maxSupported {
			continue
		}
		if v > best {
			best = v
		}
	}
	if best == 0 {
		return 0, false
	}
	if contains(supported, prefer) && prefer >= minSupported && prefer <= maxSupported {
		return prefer, true
	}
	return best, true
}
func contains(xs []uint32, v uint32) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// ValidateDriverMessage checks the report envelope invariants that every
// implementation must enforce before sending: instance ID, positive
// sequence, schema version and a body. It returns a descriptive error.
func ValidateDriverMessage(m *DriverMessage) error {
	if m == nil {
		return fmt.Errorf("driver: nil message")
	}
	if m.PluginInstanceID == "" {
		return fmt.Errorf("driver: plugin_instance_id is required")
	}
	if m.Sequence == 0 {
		return fmt.Errorf("driver: sequence must be positive")
	}
	if m.SchemaVersion == "" {
		return fmt.Errorf("driver: schema_version is required")
	}
	if m.DeviceID == "" {
		return fmt.Errorf("driver: device_id is required")
	}
	if m.Union == nil {
		return fmt.Errorf("driver: message body is required")
	}
	return nil
}

// SortMessagesBySequence is a helper for replay buffers: it returns a copy
// sorted ascending by sequence (stable) without mutating the input.
func SortMessagesBySequence(msgs []*DriverMessage) []*DriverMessage {
	out := make([]*DriverMessage, len(msgs))
	copy(out, msgs)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Sequence < out[j].Sequence
	})
	return out
}
