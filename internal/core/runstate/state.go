package runstate

type State string

const (
	Draft            State = "DRAFT"
	Analyzing        State = "ANALYZING"
	Proposing        State = "PROPOSING"
	Reviewing        State = "REVIEWING"
	Judging          State = "JUDGING"
	RedTeam          State = "RED_TEAM"
	AwaitingApproval State = "AWAITING_APPROVAL"
	Executing        State = "EXECUTING"
	Verifying        State = "VERIFYING"
	Replanning       State = "REPLANNING"
	Succeeded        State = "SUCCEEDED"
	Failed           State = "FAILED"
	Cancelled        State = "CANCELLED"
)

var legalTransitions = map[State]map[State]bool{
	Draft:            {Analyzing: true, Cancelled: true},
	Analyzing:        {Proposing: true, Failed: true, Cancelled: true},
	Proposing:        {Reviewing: true, Failed: true, Cancelled: true},
	Reviewing:        {Judging: true, Failed: true, Cancelled: true},
	Judging:          {RedTeam: true, Failed: true, Cancelled: true},
	RedTeam:          {Judging: true, AwaitingApproval: true, Failed: true, Cancelled: true},
	AwaitingApproval: {Executing: true, Cancelled: true},
	Executing:        {Verifying: true, Failed: true, Cancelled: true},
	Verifying:        {Succeeded: true, Replanning: true, Failed: true, Cancelled: true},
	Replanning:       {Reviewing: true, Failed: true, Cancelled: true},
}

func CanTransition(from, to State) bool {
	nextStates, ok := legalTransitions[from]
	if !ok {
		return false
	}

	return nextStates[to]
}
