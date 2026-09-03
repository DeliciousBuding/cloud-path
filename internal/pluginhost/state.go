package pluginhost

import "fmt"

// State is the lifecycle state of one plugin process under the Supervisor.
// The canonical uppercase names are the UI/observability contract and must
// remain stable.
type State uint8

const (
	StateStopped State = iota
	StateStarting
	StateHealthy
	StateDegraded
	StateCrashed
	StateBackoff
	StateDisabled
)

// String returns the canonical uppercase state name.
func (s State) String() string {
	switch s {
	case StateStopped:
		return "STOPPED"
	case StateStarting:
		return "STARTING"
	case StateHealthy:
		return "HEALTHY"
	case StateDegraded:
		return "DEGRADED"
	case StateCrashed:
		return "CRASHED"
	case StateBackoff:
		return "BACKOFF"
	case StateDisabled:
		return "DISABLED"
	default:
		return fmt.Sprintf("STATE(%d)", uint8(s))
	}
}

// stateTransitions is the allowed transition table for the plugin state
// machine. STOPPED is reachable from every running state during a graceful
// shutdown; DISABLED is a guarded state entered by an explicit Disable call
// or by exhausting the restart budget.
var stateTransitions = map[State]map[State]bool{
	StateStopped: {
		StateStarting: true,
		StateDisabled: true,
	},
	StateStarting: {
		StateHealthy:  true,
		StateDegraded: true,
		StateCrashed:  true,
		StateDisabled: true,
		StateStopped:  true,
	},
	StateHealthy: {
		StateDegraded: true,
		StateCrashed:  true,
		StateDisabled: true,
		StateStopped:  true,
	},
	StateDegraded: {
		StateHealthy:  true,
		StateCrashed:  true,
		StateDisabled: true,
		StateStopped:  true,
	},
	StateCrashed: {
		StateBackoff:  true,
		StateDisabled: true,
		StateStopped:  true,
	},
	StateBackoff: {
		StateStarting: true,
		StateDisabled: true,
		StateStopped:  true,
	},
	StateDisabled: {
		StateStopped: true,
	},
}

// CanTransition reports whether from -> to is a legal transition.
func (s State) CanTransition(to State) bool {
	next, ok := stateTransitions[s]
	if !ok {
		return false
	}
	return next[to]
}
