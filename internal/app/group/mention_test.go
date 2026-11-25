package group

import "testing"

func TestParseMentions(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []MentionToken
	}{
		{"simple", "hi @claude-x please", []MentionToken{{Raw: "@claude-x", Handle: "claude-x", Start: 3, End: 12}}},
		{"cjk offsets", "你好 @claude-x", []MentionToken{{Raw: "@claude-x", Handle: "claude-x", Start: 3, End: 12}}},
		{"astral emoji counts 2", "😀 @ab", []MentionToken{{Raw: "@ab", Handle: "ab", Start: 3, End: 6}}},
		{"uppercase normalized", "@GPT-4o", []MentionToken{{Raw: "@GPT-4o", Handle: "gpt-4o", Start: 0, End: 7}}},
		{"email ignored", "mail a@bc.com", nil},
		{"double at ignored", "@@ab", nil},
		{"too short", "@a", nil},
		{"inline code ignored", "see `@ab` and @cd", []MentionToken{{Raw: "@cd", Handle: "cd", Start: 14, End: 17}}},
		{"quote ignored", "> @ab said\n@cd", []MentionToken{{Raw: "@cd", Handle: "cd", Start: 11, End: 14}}},
		{"fence ignored", "```\n@ab\n```\n@cd", []MentionToken{{Raw: "@cd", Handle: "cd", Start: 12, End: 15}}},
		{"two mentions", "@ab,@cd", []MentionToken{{Raw: "@ab", Handle: "ab", Start: 0, End: 3}, {Raw: "@cd", Handle: "cd", Start: 4, End: 7}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseMentions(c.body)
			if len(got) != len(c.want) {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("token %d: got %+v want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}
