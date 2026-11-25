package group

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aicouncil/aicouncil/internal/council"
	"github.com/aicouncil/aicouncil/internal/council/schema"
)

// Proposal prompt construction, plan parsing and validation (P6 §3 of the
// plan). The assignee model is asked for ONE schema.ExecutionPlan as JSON.
// Anything else — prose, markdown fences, partial JSON — goes through one
// repair round-trip; a plan that parses but is structurally invalid
// (council.ValidatePlan) is NOT repaired: it fails as invalid_plan, since
// the model demonstrably answered in-format but with an unusable plan.

const proposalSystemPrompt = "You are an AI engineer in a shared planning group. " +
	"A human dispatched a requirement to you. Respond with a single JSON object that is a valid execution plan " +
	"(an object with a \"patches\" array and/or a \"commands\" array; each command has an \"executable\" and \"args\"). " +
	"Do not add prose, markdown fences, or commentary outside the JSON. " +
	"The plan is a proposal only: it will be reviewed by other members and must be approved by a human before anything executes."

// buildProposalMessages assembles [system, ...context, user]. Context
// messages are rendered as plain transcript lines inside the user message
// (single user turn keeps provider compatibility); repair, when non-nil,
// appends the failed reply and the fix instruction. priorFeedback (P7)
// carries the previous round's review feedback on revision runs.
func buildProposalMessages(reqTitle, reqBody string, acceptance []string, contextLines []string, repair *repairContext, priorFeedback string) []InvokeMessage {
	var b strings.Builder
	b.WriteString("Requirement: ")
	b.WriteString(reqTitle)
	b.WriteString("\n\n")
	b.WriteString(reqBody)
	if len(acceptance) > 0 {
		b.WriteString("\n\nAcceptance criteria:")
		for _, a := range acceptance {
			b.WriteString("\n- ")
			b.WriteString(a)
		}
	}
	if priorFeedback != "" {
		b.WriteString("\n\nReviewer feedback on your previous proposal (address every point):\n")
		b.WriteString(priorFeedback)
	}
	if len(contextLines) > 0 {
		b.WriteString("\n\nRecent group discussion (oldest first):\n")
		b.WriteString(strings.Join(contextLines, "\n"))
	}
	msgs := []InvokeMessage{
		{Role: "system", Content: proposalSystemPrompt},
		{Role: "user", Content: b.String()},
	}
	if repair != nil {
		msgs = append(msgs,
			InvokeMessage{Role: "assistant", Content: repair.badReply},
			InvokeMessage{Role: "user", Content: "That reply was not valid JSON for an execution plan (" + repair.problem + "). Reply again with ONLY the JSON object, no prose, no markdown fences."},
		)
	}
	return msgs
}

type repairContext struct {
	badReply string
	problem  string
}

// parsePlan extracts and validates a plan from a model reply. It tolerates
// markdown fences and leading/trailing prose around the JSON object, then
// applies council.ValidatePlan with an empty red-team report (blocking
// findings are a P7 concept; structural issues are what matters here).
func parsePlan(content string) (schema.ExecutionPlan, []council.PlanIssue, error) {
	raw := extractJSONObject(content)
	if raw == "" {
		return schema.ExecutionPlan{}, nil, fmt.Errorf("no JSON object found in reply")
	}
	var plan schema.ExecutionPlan
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(&plan); err != nil {
		return schema.ExecutionPlan{}, nil, fmt.Errorf("invalid plan JSON: %w", err)
	}
	issues := council.ValidatePlan(plan, schema.RedTeamReport{})
	return plan, issues, nil
}

// extractJSONObject returns the first balanced {...} span of s, or "".
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// planSummary renders a one-paragraph human summary of a plan for the
// proposal card (the full plan ships in the payload / requirement view).
func planSummary(plan schema.ExecutionPlan) string {
	parts := make([]string, 0, 4)
	if len(plan.Patches) > 0 {
		files := make([]string, 0, len(plan.Patches))
		for _, p := range plan.Patches {
			files = append(files, p.Path)
		}
		parts = append(parts, fmt.Sprintf("%d patch(es): %s", len(plan.Patches), strings.Join(files, ", ")))
	}
	if len(plan.Commands) > 0 {
		parts = append(parts, fmt.Sprintf("%d command(s)", len(plan.Commands)))
	}
	if len(plan.VerificationCommands) > 0 {
		parts = append(parts, fmt.Sprintf("%d verification command(s)", len(plan.VerificationCommands)))
	}
	if len(plan.Acceptance) > 0 {
		parts = append(parts, "acceptance: "+strings.Join(plan.Acceptance, "; "))
	}
	if len(parts) == 0 {
		return "Empty plan."
	}
	return strings.Join(parts, " · ")
}

func summarizeIssues(issues []council.PlanIssue) string {
	msgs := make([]string, 0, len(issues))
	for _, is := range issues {
		msgs = append(msgs, is.Code+": "+is.Message)
	}
	return strings.Join(msgs, "; ")
}

func issueCodes(issues []council.PlanIssue) []string {
	out := make([]string, 0, len(issues))
	for _, is := range issues {
		out = append(out, is.Code)
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return truncateRunes(s, 200)
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// truncate caps a string at max bytes (error strings, failure details).
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
