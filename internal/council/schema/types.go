package schema

type TaskBrief struct {
	Requirement    string     `json:"requirement"`
	Constraints    []string   `json:"constraints"`
	Acceptance     []string   `json:"acceptance"`
	WorkspaceFacts []Evidence `json:"workspace_facts"`
}
type Evidence struct {
	Path  string `json:"path"`
	Line  int    `json:"line,omitempty"`
	Claim string `json:"claim"`
}
type FileChange struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Rationale string `json:"rationale"`
}
type Proposal struct {
	ID       string       `json:"id"`
	Summary  string       `json:"summary"`
	Approach []string     `json:"approach"`
	Files    []FileChange `json:"files"`
	Risks    []string     `json:"risks"`
	Tests    []string     `json:"tests"`
	Evidence []Evidence   `json:"evidence"`
}
type PeerReview struct {
	ReviewerSeatID string   `json:"reviewer_seat_id"`
	ProposalAlias  string   `json:"proposal_alias"`
	Correctness    []string `json:"correctness"`
	Risks          []string `json:"risks"`
	Missing        []string `json:"missing"`
	Verdict        string   `json:"verdict"`
}
type CouncilDecision struct {
	SelectedAliases []string            `json:"selected_aliases"`
	Reasons         []string            `json:"reasons"`
	Rejected        map[string][]string `json:"rejected"`
	PlanSummary     []string            `json:"plan_summary"`
}
type RedTeamReport struct {
	Blocking       []string `json:"blocking"`
	NonBlocking    []string `json:"non_blocking"`
	Recommendation string   `json:"recommendation"`
}
type Command struct {
	Executable     string   `json:"executable"`
	Args           []string `json:"args"`
	WorkDir        string   `json:"work_dir"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	Purpose        string   `json:"purpose"`
}
type Patch struct {
	Path        string `json:"path"`
	UnifiedDiff string `json:"unified_diff"`
	BeforeHash  string `json:"before_hash"`
}
type ExecutionPlan struct {
	Version    int       `json:"version"`
	Patches    []Patch   `json:"patches"`
	Commands   []Command `json:"commands"`
	Acceptance []string  `json:"acceptance"`
	Recovery   []string  `json:"recovery"`
}
type StepResult struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	ExitCode       int    `json:"exit_code"`
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr"`
	DurationMillis int64  `json:"duration_millis"`
	Status         string `json:"status"`
}
type VerificationReport struct {
	Passed         bool              `json:"passed"`
	ActualDiff     string            `json:"actual_diff"`
	Steps          []StepResult      `json:"steps"`
	Acceptance     map[string]string `json:"acceptance"`
	ReviewFindings []string          `json:"review_findings"`
}
