package group

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Policy is a group's review/context policy. Every field already exists now so
// later increments don't need a data migration; P6–P9 enforce them.
type Policy struct {
	ProposalReviewRule    string `json:"proposal_review_rule"`
	ProposalQuorum        *int   `json:"proposal_quorum"`
	ResultReviewRule      string `json:"result_review_rule"`
	ResultQuorum          *int   `json:"result_quorum"`
	MinReviewers          int    `json:"min_reviewers"`
	MaxReviewers          int    `json:"max_reviewers"`
	MaxProposalRounds     int    `json:"max_proposal_rounds"`
	MaxResultRounds       int    `json:"max_result_rounds"`
	ReviewDeadlineMinutes int    `json:"review_deadline_minutes"`
	RedteamRequired       bool   `json:"redteam_required"`
	RequireHumanApproval  bool   `json:"require_human_approval"`
	ContextMessages       int    `json:"context_messages"`
	ContextTokenBudget    int    `json:"context_token_budget"`
	AIFreeChat            bool   `json:"ai_free_chat"`
}

func DefaultPolicy() Policy {
	return Policy{
		ProposalReviewRule: "unanimous", ResultReviewRule: "unanimous",
		MinReviewers: 1, MaxReviewers: 5, MaxProposalRounds: 3, MaxResultRounds: 2,
		ReviewDeadlineMinutes: 30, RedteamRequired: true, RequireHumanApproval: true,
		ContextMessages: 40, ContextTokenBudget: 24000, AIFreeChat: false,
	}
}

// Budget caps; 0 means no cap. Enforced from P6 (metering).
type Budget struct {
	USDCap               float64 `json:"usd_cap"`
	TokenCap             int64   `json:"token_cap"`
	PerRequirementUSDCap float64 `json:"per_requirement_usd_cap"`
}

// overlay applies a partial JSON object onto base, rejecting unknown keys.
func overlay[T any](base T, patch map[string]any) (T, error) {
	if len(patch) == 0 {
		return base, nil
	}
	b, err := json.Marshal(base)
	if err != nil {
		return base, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return base, err
	}
	for k, v := range patch {
		if _, ok := m[k]; !ok {
			return base, fmt.Errorf("unknown key %q", k)
		}
		m[k] = v
	}
	merged, err := json.Marshal(m)
	if err != nil {
		return base, err
	}
	var out T
	dec := json.NewDecoder(bytes.NewReader(merged))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return base, err
	}
	return out, nil
}

func applyPolicy(base Policy, patch map[string]any) (Policy, error) {
	if v, ok := patch["require_human_approval"]; ok {
		if b, isBool := v.(bool); !isBool || !b {
			return base, errHumanApproval
		}
	}
	p, err := overlay(base, patch)
	if err != nil {
		return base, invalidPolicy(err.Error())
	}
	p.RequireHumanApproval = true // D1: locked in v1
	if err := validatePolicy(p); err != nil {
		return base, err
	}
	return p, nil
}

func validRule(r string) bool { return r == "unanimous" || r == "quorum" }

func validatePolicy(p Policy) error {
	if !validRule(p.ProposalReviewRule) || !validRule(p.ResultReviewRule) {
		return invalidPolicy("review rules must be unanimous|quorum")
	}
	if p.MinReviewers < 1 || p.MaxReviewers < p.MinReviewers || p.MaxReviewers > 8 {
		return invalidPolicy("reviewers: 1 <= min_reviewers <= max_reviewers <= 8")
	}
	checkQuorum := func(rule string, q *int, name string) error {
		if rule != "quorum" {
			return nil
		}
		if q == nil || *q < 1 || *q > p.MaxReviewers {
			return invalidPolicy(name + " must be in 1..max_reviewers when the rule is quorum")
		}
		return nil
	}
	if err := checkQuorum(p.ProposalReviewRule, p.ProposalQuorum, "proposal_quorum"); err != nil {
		return err
	}
	if err := checkQuorum(p.ResultReviewRule, p.ResultQuorum, "result_quorum"); err != nil {
		return err
	}
	if p.MaxProposalRounds < 1 || p.MaxProposalRounds > 10 || p.MaxResultRounds < 1 || p.MaxResultRounds > 10 {
		return invalidPolicy("round limits must be in 1..10")
	}
	if p.ReviewDeadlineMinutes < 5 || p.ReviewDeadlineMinutes > 1440 {
		return invalidPolicy("review_deadline_minutes must be in 5..1440")
	}
	if p.ContextMessages < 0 || p.ContextMessages > 200 {
		return invalidPolicy("context_messages must be in 0..200")
	}
	if p.ContextTokenBudget < 1000 || p.ContextTokenBudget > 200000 {
		return invalidPolicy("context_token_budget must be in 1000..200000")
	}
	return nil
}

func applyBudget(base Budget, patch map[string]any) (Budget, error) {
	b, err := overlay(base, patch)
	if err != nil {
		return base, newErr(422, "invalid_budget", err.Error())
	}
	if b.USDCap < 0 || b.TokenCap < 0 || b.PerRequirementUSDCap < 0 {
		return base, newErr(422, "invalid_budget", "budget caps must be >= 0 (0 = no cap)")
	}
	return b, nil
}

func decodePolicy(raw []byte) Policy {
	p := DefaultPolicy()
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	p.RequireHumanApproval = true
	return p
}

func decodeBudget(raw []byte) Budget {
	var b Budget
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &b)
	}
	return b
}

// validateParamsOverride mirrors the P4 aimodel param bounds.
func validateParamsOverride(p map[string]any) ([]byte, error) {
	if len(p) == 0 {
		return nil, nil
	}
	for k, v := range p {
		switch k {
		case "temperature":
			f, ok := v.(float64)
			if !ok || f < 0 || f > 2 {
				return nil, invalidParams("temperature must be in [0, 2]")
			}
		case "max_output_tokens":
			f, ok := v.(float64)
			if !ok || f < 1 || f > 200000 {
				return nil, invalidParams("max_output_tokens must be in [1, 200000]")
			}
		case "timeout_ms":
			f, ok := v.(float64)
			if !ok || f < 1000 || f > 600000 {
				return nil, invalidParams("timeout_ms must be in [1000, 600000]")
			}
		case "reasoning_effort":
			s, _ := v.(string)
			if s != "low" && s != "medium" && s != "high" {
				return nil, invalidParams("reasoning_effort must be low|medium|high")
			}
		default:
			return nil, newErr(422, "params_unknown_key", "unknown param: "+k)
		}
	}
	return json.Marshal(p)
}
