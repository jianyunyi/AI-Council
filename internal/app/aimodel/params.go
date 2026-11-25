package aimodel

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// validateParams allows only known keys with bounded values.
func validateParams(p map[string]any) (string, error) {
	if p == nil {
		return "{}", nil
	}
	num := func(k string, min, max float64) error {
		v, ok := p[k]
		if !ok {
			return nil
		}
		f, ok := v.(float64)
		if !ok || f < min || f > max {
			return newErr(http.StatusUnprocessableEntity, "params_out_of_range",
				fmt.Sprintf("%s must be a number in [%v, %v]", k, min, max))
		}
		return nil
	}
	for k := range p {
		switch k {
		case "temperature", "max_output_tokens", "timeout_ms", "reasoning_effort":
		default:
			return "", newErr(http.StatusUnprocessableEntity, "params_unknown_key", "unknown param: "+k)
		}
	}
	if err := num("temperature", 0, 2); err != nil {
		return "", err
	}
	if err := num("max_output_tokens", 1, 200000); err != nil {
		return "", err
	}
	if err := num("timeout_ms", 1000, 600000); err != nil {
		return "", err
	}
	if v, ok := p["reasoning_effort"]; ok {
		s, _ := v.(string)
		if s != "low" && s != "medium" && s != "high" {
			return "", newErr(http.StatusUnprocessableEntity, "params_out_of_range", "reasoning_effort must be low|medium|high")
		}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
