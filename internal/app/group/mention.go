package group

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var handleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// MentionToken is one @handle occurrence. Start/End are UTF-16 code-unit
// offsets (JavaScript string indices) over the original body, End exclusive.
type MentionToken struct {
	Raw    string
	Handle string
	Start  int
	End    int
}

func isHandleRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-'
}

func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// ParseMentions finds @handle tokens. It ignores fenced code blocks (``` or ~~~),
// inline code spans, blockquote lines ("> ..."), and '@' preceded by a word
// character, '.', '@' or '/' (emails, paths, @@).
func ParseMentions(body string) []MentionToken {
	var out []MentionToken
	offset := 0 // UTF-16 offset of the current line start
	inFence := false
	fenceMarker := ""
	lines := strings.SplitAfter(body, "\n")
	for _, line := range lines {
		lineLen := 0
		for _, r := range line {
			lineLen += utf16Len(r)
		}
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inFence {
				inFence, fenceMarker = true, marker
			} else if marker == fenceMarker {
				inFence = false
			}
			offset += lineLen
			continue
		}
		if inFence || strings.HasPrefix(trimmed, ">") {
			offset += lineLen
			continue
		}
		out = append(out, scanLine(line, offset)...)
		offset += lineLen
	}
	return out
}

func scanLine(line string, base int) []MentionToken {
	var out []MentionToken
	runes := []rune(line)
	pos := base // UTF-16 offset of runes[i]
	inCode := false
	var prev rune
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '`' {
			inCode = !inCode
			prev = r
			pos += utf16Len(r)
			continue
		}
		if !inCode && r == '@' && !blocksMention(prev, i) {
			j := i + 1
			for j < len(runes) && isHandleRune(runes[j]) {
				j++
			}
			raw := string(runes[i+1 : j])
			handle := strings.ToLower(raw)
			if handleRe.MatchString(handle) {
				end := pos + 1
				for _, hr := range runes[i+1 : j] {
					end += utf16Len(hr)
				}
				out = append(out, MentionToken{Raw: "@" + raw, Handle: handle, Start: pos, End: end})
				for k := i; k < j; k++ {
					pos += utf16Len(runes[k])
				}
				prev = runes[j-1]
				i = j - 1
				continue
			}
		}
		prev = r
		pos += utf16Len(r)
	}
	return out
}

func blocksMention(prev rune, idx int) bool {
	if idx == 0 {
		return false
	}
	return unicode.IsLetter(prev) || unicode.IsDigit(prev) || prev == '_' || prev == '.' || prev == '@' || prev == '/'
}

func validBodyLen(s string) bool {
	n := utf8.RuneCountInString(s)
	return n >= 1 && n <= 16000
}
