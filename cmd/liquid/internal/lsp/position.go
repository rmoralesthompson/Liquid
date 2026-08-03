package lsp

import (
	"sort"
	"unicode/utf8"
)

// lineStarts indexes the byte offset of each line start in text.
func lineStarts(text string) []int {
	starts := []int{0}
	for i := range len(text) {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// offsetOf converts a compiler position (1-based line, 1-based byte column)
// to a byte offset, clamped to the document.
func (d *document) offsetOf(line, col int) int {
	if line < 1 || line > len(d.lines) {
		return len(d.text)
	}
	off := d.lines[line-1] + col - 1
	return min(off, len(d.text))
}

// positionAt converts a byte offset to an LSP position (UTF-16 characters).
func (d *document) positionAt(off int) Position {
	off = min(max(off, 0), len(d.text))
	line := sort.Search(len(d.lines), func(i int) bool { return d.lines[i] > off }) - 1
	return Position{Line: line, Character: utf16Len(d.text[d.lines[line]:off])}
}

// offsetAt converts an LSP position to a byte offset, clamped to the line.
func (d *document) offsetAt(p Position) int {
	if p.Line < 0 || p.Line >= len(d.lines) {
		return len(d.text)
	}
	off := d.lines[p.Line]
	end := len(d.text)
	if p.Line+1 < len(d.lines) {
		end = d.lines[p.Line+1]
	}
	units := 0
	for off < end && units < p.Character {
		r, size := utf8.DecodeRuneInString(d.text[off:end])
		if r == '\n' {
			break
		}
		units += utf16RuneLen(r)
		off += size
	}
	return off
}

// span is a byte-offset interval in the document; contains is inclusive of
// the end so a cursor sitting just after a token still resolves it.
type span struct{ start, end int }

func (s span) contains(off int) bool { return off >= s.start && off <= s.end }

// rangeOf converts a byte span to an LSP range.
func (d *document) rangeOf(s span) Range {
	return Range{Start: d.positionAt(s.start), End: d.positionAt(s.end)}
}

// tokenLenAt is the byte length of the identifier-shaped token starting at
// off, so a diagnostic can span what it points at instead of collapsing to
// a zero-width range.
func (d *document) tokenLenAt(off int) int {
	end := off
	for end < len(d.text) && isTokenByte(d.text[end]) {
		end++
	}
	return end - off
}

// utf16RuneLen is the UTF-16 code unit count of one rune.
func utf16RuneLen(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

// utf16Len is the UTF-16 code unit count of a string.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16RuneLen(r)
	}
	return n
}
