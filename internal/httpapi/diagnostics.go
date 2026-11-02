package httpapi

import (
	"sort"
	"unicode/utf8"

	"github.com/MontFerret/playground/internal/playground"
)

type (
	httpDiagnostic struct {
		Code     string              `json:"code,omitempty"`
		Severity string              `json:"severity"`
		Phase    string              `json:"phase"`
		Message  string              `json:"message"`
		Hint     string              `json:"hint,omitempty"`
		Location *diagnosticLocation `json:"location,omitempty"`
	}

	diagnosticLocation struct {
		Source string           `json:"source"`
		Range  *diagnosticRange `json:"range,omitempty"`
	}

	diagnosticRange struct {
		Start diagnosticPosition `json:"start"`
		End   diagnosticPosition `json:"end"`
	}

	diagnosticPosition struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	}

	sourceIndex struct {
		text        string
		lines       []int
		adjustments []utf16Adjustment
	}

	utf16Adjustment struct {
		end        int
		extraBytes int
	}
)

func newSourceIndex(text string) *sourceIndex {
	index := &sourceIndex{text: text, lines: []int{0}}
	extraBytes := 0
	for offset, character := range text {
		if character == '\n' {
			index.lines = append(index.lines, offset+1)
		}

		if character >= utf8.RuneSelf {
			size := utf8.RuneLen(character)
			units := 1
			if character > 0xFFFF {
				units = 2
			}

			extraBytes += size - units
			index.adjustments = append(index.adjustments, utf16Adjustment{end: offset + size, extraBytes: extraBytes})
		}
	}

	return index
}

func (s *sourceIndex) diagnostics(values []playground.Diagnostic) []httpDiagnostic {
	result := make([]httpDiagnostic, 0, len(values))
	for _, value := range values {
		converted := httpDiagnostic{
			Severity: "error", Phase: value.Phase, Message: value.Message, Hint: value.Hint,
			Location: &diagnosticLocation{Source: value.Source},
		}
		if value.Span != nil && value.Span.End >= value.Span.Start {
			start, startOK := s.position(value.Span.Start)
			end, endOK := s.position(value.Span.End)
			if startOK && endOK {
				converted.Location.Range = &diagnosticRange{Start: start, End: end}
			}
		}

		result = append(result, converted)
	}

	return result
}

func (s *sourceIndex) position(offset int) (diagnosticPosition, bool) {
	if offset < 0 || offset > len(s.text) || (offset < len(s.text) && !utf8.RuneStart(s.text[offset])) {
		return diagnosticPosition{}, false
	}

	line := sort.Search(len(s.lines), func(i int) bool { return s.lines[i] > offset }) - 1
	end := offset
	// Both bytes of CRLF map to the preceding line's content boundary; the
	// offset after LF maps to the next line, including an empty final line.
	if end > s.lines[line] && s.text[end-1] == '\r' && end < len(s.text) && s.text[end] == '\n' {
		end--
	}

	// Sparse cumulative corrections keep ASCII cheap and avoid rescanning a
	// long line for every diagnostic endpoint.
	character := end - s.lines[line] - s.extraBytesAt(end) + s.extraBytesAt(s.lines[line])

	return diagnosticPosition{Line: line, Character: character}, true
}

func (s *sourceIndex) extraBytesAt(offset int) int {
	i := sort.Search(len(s.adjustments), func(i int) bool { return s.adjustments[i].end > offset })
	if i == 0 {
		return 0
	}

	return s.adjustments[i-1].extraBytes
}
