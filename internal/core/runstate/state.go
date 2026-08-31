package runstate

type State string

const (
	DRAFT             State = "DRAFT"
	ANALYZING         State = "ANALYZING"
	PROPOSING         State = "PROPOSING"
	REVIEWING         State = "REVIEWING"
	JUDGING           State = "JUDGING"
	RED_TEAM          State = "RED_TEAM"
	AWAITING_APPROVAL State = "AWAITING_APPROVAL"
	EXECUTING         State = "EXECUTING"
	VERIFYING         State = "VERIFYING"
	REPLANNING        State = "REPLANNING"
	SUCCEEDED         State = "SUCCEEDED"
	FAILED            State = "FAILED"
	CANCELLED         State = "CANCELLED"
)

var legalTransitions = map[State]map[State]bool{
	DRAFT:             {ANALYZING: true, CANCELLED: true},
	ANALYZING:         {PROPOSING: true, FAILED: true, CANCELLED: true},
	PROPOSING:         {REVIEWING: true, FAILED: true, CANCELLED: true},
	REVIEWING:         {JUDGING: true, FAILED: true, CANCELLED: true},
	JUDGING:           {RED_TEAM: true, FAILED: true, CANCELLED: true},
	RED_TEAM:          {JUDGING: true, AWAITING_APPROVAL: true, FAILED: true, CANCELLED: true},
	AWAITING_APPROVAL: {EXECUTING: true, CANCELLED: true},
	EXECUTING:         {VERIFYING: true, FAILED: true, CANCELLED: true},
	VERIFYING:         {SUCCEEDED: true, REPLANNING: true, FAILED: true, CANCELLED: true},
	REPLANNING:        {REVIEWING: true, FAILED: true, CANCELLED: true},
}

func CanTransition(from, to State) bool {
	nextStates, ok := legalTransitions[from]
	if !ok {
		return false
	}

	return nextStates[to]
}
