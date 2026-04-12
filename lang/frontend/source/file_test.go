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

package source

import "testing"

func TestFileOffsetAndPositionRoundTrip(t *testing.T) {
	file := NewFile("offsets.mcl", []byte("ab\n\xce\xbbx\n"))

	tests := []struct {
		line   int
		column int
		offset int
	}{
		{line: 0, column: 0, offset: 0},
		{line: 0, column: 2, offset: 2},
		{line: 1, column: 0, offset: 3},
		{line: 1, column: 1, offset: 5},
		{line: 1, column: 2, offset: 6},
		{line: 2, column: 0, offset: 7},
	}

	for _, tt := range tests {
		if got := file.Offset(tt.line, tt.column); got != tt.offset {
			t.Fatalf("offset mismatch for %d:%d: got %d, want %d", tt.line, tt.column, got, tt.offset)
		}

		pos := file.Position(tt.offset)
		if pos.Line != tt.line || pos.Column != tt.column {
			t.Fatalf("position mismatch for offset %d: got %d:%d, want %d:%d", tt.offset, pos.Line, pos.Column, tt.line, tt.column)
		}
	}
}

func TestFileSpanFromLinesAndColumns(t *testing.T) {
	file := NewFile("span.mcl", []byte("first\nsecond\n"))
	span := file.Span(1, 1, 1, 4)

	if got, want := span.Start, 7; got != want {
		t.Fatalf("unexpected span start: got %d, want %d", got, want)
	}
	if got, want := span.End, 10; got != want {
		t.Fatalf("unexpected span end: got %d, want %d", got, want)
	}
	if got, want := string(file.Bytes()[span.Start:span.End]), "eco"; got != want {
		t.Fatalf("unexpected span contents: got %q, want %q", got, want)
	}
}
