package council

func proposalAlias(index int) string {
	if index < 0 {
		return "Proposal"
	}
	index++
	name := ""
	for index > 0 {
		index--
		name = string(rune('A'+index%26)) + name
		index /= 26
	}
	return "Proposal " + name
}
