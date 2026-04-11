// Mgmt
// Copyright (C) James Shubin and the project contributors
// Written by James Shubin <james@shubin.ca> and the project contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// Additional permission under GNU GPL version 3 section 7
//
// If you modify this program, or any covered work, by linking or combining it
// with embedded mcl code and modules (and that the embedded mcl code and
// modules which link with this program, contain a copy of their source code in
// the authoritative form) containing parts covered by the terms of any other
// license, the licensors of this program grant you additional permission to
// convey the resulting work. Furthermore, the licensors of this program grant
// the original author, James Shubin, additional permission to update this
// additional permission if he deems it necessary to achieve the goals of this
// additional permission.

package token

import (
	"testing"

	langsource "github.com/purpleidea/mgmt/lang/frontend/source"
)

func TestScanRoundTrip(t *testing.T) {
	source := "# header\n\tnoop \"n1\" { # inline\n\t\tint64 => 42,\n\t}\n"
	file := langsource.NewFile("roundtrip.mcl", []byte(source))

	tokens := Scan(file)
	if got := JoinRaw(tokens); got != source {
		t.Fatalf("round-trip mismatch\nwant: %q\ngot:  %q", source, got)
	}
	if got := tokens[len(tokens)-1].Kind; got != KindEOF {
		t.Fatalf("expected final token to be eof, got: %s", got)
	}
}

func TestScanPreservesCommentsWhitespaceAndNewlines(t *testing.T) {
	source := " # lead\nnoop \"n1\" { # inline\n}\n"
	file := langsource.NewFile("trivia.mcl", []byte(source))

	tokens := Scan(file)
	kinds := []Kind{}
	raws := []string{}
	for _, tok := range tokens {
		kinds = append(kinds, tok.Kind)
		raws = append(raws, tok.Raw)
	}

	wantKinds := []Kind{
		KindWhitespace,
		KindComment,
		KindNewline,
		KindIdentifier,
		KindWhitespace,
		KindString,
		KindWhitespace,
		KindOpenCurly,
		KindWhitespace,
		KindComment,
		KindNewline,
		KindCloseCurly,
		KindNewline,
		KindEOF,
	}
	wantRaws := []string{
		" ",
		"# lead",
		"\n",
		"noop",
		" ",
		"\"n1\"",
		" ",
		"{",
		" ",
		"# inline",
		"\n",
		"}",
		"\n",
		"",
	}

	if len(kinds) != len(wantKinds) {
		t.Fatalf("unexpected token count: got %d, want %d", len(kinds), len(wantKinds))
	}
	for i := range wantKinds {
		if kinds[i] != wantKinds[i] || raws[i] != wantRaws[i] {
			t.Fatalf("token %d mismatch: got (%s, %q), want (%s, %q)", i, kinds[i], raws[i], wantKinds[i], wantRaws[i])
		}
	}
}

func TestScanSpans(t *testing.T) {
	source := "#x\nab 12\n"
	file := langsource.NewFile("span.mcl", []byte(source))
	tokens := Scan(file)

	type expectation struct {
		kind                   Kind
		raw                    string
		startOffset, endOffset int
		startLine, startCol    int
		endLine, endCol        int
	}

	expectations := []expectation{
		{kind: KindComment, raw: "#x", startOffset: 0, endOffset: 2, startLine: 0, startCol: 0, endLine: 0, endCol: 2},
		{kind: KindNewline, raw: "\n", startOffset: 2, endOffset: 3, startLine: 0, startCol: 2, endLine: 1, endCol: 0},
		{kind: KindIdentifier, raw: "ab", startOffset: 3, endOffset: 5, startLine: 1, startCol: 0, endLine: 1, endCol: 2},
		{kind: KindWhitespace, raw: " ", startOffset: 5, endOffset: 6, startLine: 1, startCol: 2, endLine: 1, endCol: 3},
		{kind: KindInteger, raw: "12", startOffset: 6, endOffset: 8, startLine: 1, startCol: 3, endLine: 1, endCol: 5},
		{kind: KindNewline, raw: "\n", startOffset: 8, endOffset: 9, startLine: 1, startCol: 5, endLine: 2, endCol: 0},
		{kind: KindEOF, raw: "", startOffset: 9, endOffset: 9, startLine: 2, startCol: 0, endLine: 2, endCol: 0},
	}

	if len(tokens) != len(expectations) {
		t.Fatalf("unexpected token count: got %d, want %d", len(tokens), len(expectations))
	}

	for i, exp := range expectations {
		tok := tokens[i]
		start := tok.Span.StartPosition()
		end := tok.Span.EndPosition()
		if tok.Kind != exp.kind || tok.Raw != exp.raw || tok.Span.Start != exp.startOffset || tok.Span.End != exp.endOffset ||
			start.Line != exp.startLine || start.Column != exp.startCol || end.Line != exp.endLine || end.Column != exp.endCol {
			t.Fatalf("token %d mismatch: got kind=%s raw=%q offsets=%d:%d pos=%d:%d-%d:%d, want kind=%s raw=%q offsets=%d:%d pos=%d:%d-%d:%d",
				i, tok.Kind, tok.Raw, tok.Span.Start, tok.Span.End, start.Line, start.Column, end.Line, end.Column,
				exp.kind, exp.raw, exp.startOffset, exp.endOffset, exp.startLine, exp.startCol, exp.endLine, exp.endCol)
		}
	}
}

func TestScanUnterminatedStringIsLosslessErrorToken(t *testing.T) {
	source := "noop \"n1\nnext\n"
	file := langsource.NewFile("bad-string.mcl", []byte(source))
	tokens := Scan(file)

	if got := JoinRaw(tokens); got != source {
		t.Fatalf("round-trip mismatch for lexer error\nwant: %q\ngot:  %q", source, got)
	}

	var found bool
	for _, tok := range tokens {
		if tok.Kind != KindError {
			continue
		}
		found = true
		if got, want := tok.Raw, "\"n1\nnext\n"; got != want {
			t.Fatalf("unexpected lexer error token raw: got %q, want %q", got, want)
		}
		break
	}
	if !found {
		t.Fatalf("expected one lexer error token in %v", tokens)
	}
}

func TestScanMultilineStringIsValidToken(t *testing.T) {
	source := "$msg = \"hello\nworld\"\n"
	file := langsource.NewFile("multiline-string.mcl", []byte(source))
	tokens := Scan(file)

	if got := JoinRaw(tokens); got != source {
		t.Fatalf("round-trip mismatch for multiline string\nwant: %q\ngot:  %q", source, got)
	}

	foundString := false
	for _, tok := range tokens {
		if tok.Kind == KindError {
			t.Fatalf("unexpected error token in multiline string scan: %+v", tok)
		}
		if tok.Kind == KindString {
			foundString = true
			if got, want := tok.Raw, "\"hello\nworld\""; got != want {
				t.Fatalf("unexpected multiline string raw: got %q, want %q", got, want)
			}
		}
	}
	if !foundString {
		t.Fatalf("expected one multiline string token in %v", tokens)
	}
}

func TestScanCarriageReturnIsErrorToken(t *testing.T) {
	source := "noop \"n1\" {}\r\n"
	file := langsource.NewFile("crlf.mcl", []byte(source))
	tokens := Scan(file)

	if got := JoinRaw(tokens); got != source {
		t.Fatalf("round-trip mismatch for carriage return\nwant: %q\ngot:  %q", source, got)
	}

	found := false
	for _, tok := range tokens {
		if tok.Kind != KindError {
			continue
		}
		found = true
		if got, want := tok.Raw, "\r"; got != want {
			t.Fatalf("unexpected carriage return token raw: got %q, want %q", got, want)
		}
		break
	}
	if !found {
		t.Fatalf("expected one error token for carriage return in %v", tokens)
	}
}
