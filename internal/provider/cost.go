package provider

type Price struct {
	InputPerMillionMicros  int64
	OutputPerMillionMicros int64
}

var PriceTable = map[string]Price{"gpt-4o": {InputPerMillionMicros: 5000000, OutputPerMillionMicros: 15000000}, "claude-3-5-sonnet": {InputPerMillionMicros: 3000000, OutputPerMillionMicros: 15000000}, "deepseek-chat": {InputPerMillionMicros: 140000, OutputPerMillionMicros: 280000}}

func EstimateCost(model string, usage Usage) int64 {
	p, ok := PriceTable[model]
	if !ok {
		return 0
	}
	return usage.InputTokens*p.InputPerMillionMicros/1000000 + usage.OutputTokens*p.OutputPerMillionMicros/1000000
}
