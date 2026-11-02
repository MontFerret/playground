package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MontFerret/api/source"

	"github.com/MontFerret/playground/internal/playground"
)

func TestDiagnosticCoordinates(t *testing.T) {
	text := "aé😀\r\nβ\n"
	index := newSourceIndex(text)
	for _, test := range []struct {
		offset, line, character int
	}{
		{0, 0, 0}, {1, 0, 1}, {3, 0, 2}, {7, 0, 4},
		{8, 0, 4}, {9, 1, 0}, {11, 1, 1}, {12, 2, 0},
	} {
		position, ok := index.position(test.offset)
		if !ok || position.Line != test.line || position.Character != test.character {
			t.Errorf("offset %d: %+v %v", test.offset, position, ok)
		}
	}

	for _, offset := range []int{-1, 2, 4, 5, 6, 10, 13} {
		if _, ok := index.position(offset); ok {
			t.Errorf("invalid byte boundary %d accepted", offset)
		}
	}

	for _, span := range []*source.Span{nil, {Start: -1, End: -1}, {Start: 2, End: 3}, {Start: 7, End: 3}, {Start: 0, End: 99}} {
		values := index.diagnostics([]playground.Diagnostic{{Source: "label", Span: span}})
		if values[0].Location.Range != nil || values[0].Location.Source != "label" {
			t.Fatalf("missing location fabricated: %+v", values)
		}
	}

	values := index.diagnostics([]playground.Diagnostic{{Span: &source.Span{Start: 1, End: 11}}})
	want := diagnosticRange{Start: diagnosticPosition{Line: 0, Character: 1}, End: diagnosticPosition{Line: 1, Character: 1}}
	if *values[0].Location.Range != want {
		t.Fatalf("multiline span: %+v", values[0].Location.Range)
	}

	position, ok := newSourceIndex("").position(0)
	if !ok || position != (diagnosticPosition{}) {
		t.Fatalf("empty EOF: %+v %v", position, ok)
	}
}

func TestNativeUnicodeAndEOFDiagnostics(t *testing.T) {
	handler, _ := toolingHandler(t)
	for _, test := range []struct {
		text       string
		start, end diagnosticPosition
	}{
		{`RETURN "😀" +`, diagnosticPosition{0, 12}, diagnosticPosition{0, 13}},
		{`RETURN "é" +`, diagnosticPosition{0, 11}, diagnosticPosition{0, 12}},
		{"RETURN [1,", diagnosticPosition{0, 10}, diagnosticPosition{0, 10}},
		// Native recovery places this insertion before the trailing whitespace.
		{"RETURN [1,\r\n", diagnosticPosition{0, 10}, diagnosticPosition{0, 10}},
	} {
		body, _ := json.Marshal(map[string]any{"source": map[string]string{"text": test.text}})
		response := toolingRequest(handler, "analyze", string(body))
		var value toolingResponse
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || len(value.Diagnostics) == 0 {
			t.Fatalf("missing diagnostic: %s %v", response.Body, err)
		}

		location := value.Diagnostics[0].Location
		if location.Source != "anonymous" || location.Range == nil || location.Range.Start != test.start || location.Range.End != test.end {
			t.Errorf("source %q: %s", test.text, response.Body)
		}

		if strings.Contains(response.Body.String(), `"code"`) {
			t.Error("invented a diagnostic code")
		}
	}
}
