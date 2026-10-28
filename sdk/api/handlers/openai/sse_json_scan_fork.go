package openai

import (
	"unicode"
	"unicode/utf8"
)

// responsesSSEJSONScan incrementally tracks only the JSON string-boundary guard.
// Owners must reset it whenever pending is cleared or a frame prefix is consumed.
// Other framing/validation passes deliberately remain unchanged.
type responsesSSEJSONScan struct {
	offset                                         int
	field                                          int // matched bytes of "data:", -1 for an ignored line, 5 for data
	found, started, json, inString, escaped, space bool
}

func (s *responsesSSEJSONScan) inside(pending []byte) bool {
	for s.offset < len(pending) {
		tail := pending[s.offset:]
		if !utf8.FullRune(tail) {
			break
		}
		r, size := utf8.DecodeRune(tail)
		s.offset += size
		if r == '\n' {
			s.field = 0
			s.space = false // Each SSE data line is independently trimmed.
			continue
		}
		if s.field < 0 {
			continue
		}
		if s.field < 5 {
			if s.field == 0 && unicode.IsSpace(r) {
				continue
			}
			if r != rune("data:"[s.field]) {
				s.field = -1
				continue
			}
			s.field++
			if s.field == 5 {
				if s.found {
					s.feed('\n')
				}
				s.found = true
			}
			continue
		}
		if unicode.IsSpace(r) {
			s.space = true
			continue
		}
		// Deferred whitespace is internal only once followed by non-space data.
		// Any whitespace has the same effect on quote/escape state as one space.
		if s.space && s.started {
			s.feed(' ')
		}
		s.space = false
		s.feed(r)
	}
	return s.json && s.inString
}

func (s *responsesSSEJSONScan) feed(r rune) {
	if !s.started {
		if unicode.IsSpace(r) {
			return
		}
		s.started = true
		s.json = r == '{' || r == '[' || r == '"'
	}
	if !s.json {
		return
	}
	if s.escaped {
		s.escaped = false
		return
	}
	if s.inString && r == '\\' {
		s.escaped = true
		return
	}
	if r == '"' {
		s.inString = !s.inString
	}
}
