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

import (
	"sort"
	"unicode/utf8"
)

// Position is a source location derived from a byte offset.
type Position struct {
	Offset int
	Line   int
	Column int
}

// File is an immutable source snapshot used to derive spans and positions.
type File struct {
	name        string
	source      []byte
	lineOffsets []int
}

// NewFile returns a source snapshot with precomputed line starts.
func NewFile(name string, source []byte) *File {
	lineOffsets := []int{0}
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case '\n':
			lineOffsets = append(lineOffsets, i+1)
		case '\r':
			if i+1 < len(source) && source[i+1] == '\n' {
				lineOffsets = append(lineOffsets, i+2)
				i++
				continue
			}
			lineOffsets = append(lineOffsets, i+1)
		}
	}

	return &File{
		name:        name,
		source:      source,
		lineOffsets: lineOffsets,
	}
}

// Name returns the file name associated with this snapshot.
func (obj *File) Name() string {
	if obj == nil {
		return ""
	}
	return obj.name
}

// Bytes returns the original source bytes for this snapshot.
func (obj *File) Bytes() []byte {
	if obj == nil {
		return nil
	}
	return obj.source
}

// Len returns the total source length in bytes.
func (obj *File) Len() int {
	if obj == nil {
		return 0
	}
	return len(obj.source)
}

// Position converts a byte offset into a line/column pair. Offsets are clamped
// to the current file length.
func (obj *File) Position(offset int) Position {
	if obj == nil {
		return Position{Offset: offset}
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(obj.source) {
		offset = len(obj.source)
	}

	line := sort.Search(len(obj.lineOffsets), func(i int) bool {
		return obj.lineOffsets[i] > offset
	}) - 1
	if line < 0 {
		line = 0
	}

	start := obj.lineOffsets[line]
	column := 0
	for i := start; i < offset; {
		r, size := utf8.DecodeRune(obj.source[i:offset])
		if r == utf8.RuneError && size == 0 {
			break
		}
		column++
		i += size
	}

	return Position{
		Offset: offset,
		Line:   line,
		Column: column,
	}
}

// Offset converts a zero-based line/column pair back into a byte offset.
// Columns are counted in runes to match Position.
func (obj *File) Offset(line, column int) int {
	if obj == nil {
		return 0
	}
	if line < 0 {
		line = 0
	}
	if column < 0 {
		column = 0
	}
	if line >= len(obj.lineOffsets) {
		return len(obj.source)
	}

	start := obj.lineOffsets[line]
	lineEnd := len(obj.source)
	if line+1 < len(obj.lineOffsets) {
		lineEnd = obj.lineOffsets[line+1]
	}

	offset := start
	for i := 0; i < column && offset < lineEnd; i++ {
		r, size := utf8.DecodeRune(obj.source[offset:lineEnd])
		if r == utf8.RuneError && size == 0 {
			break
		}
		offset += size
	}
	if offset > len(obj.source) {
		offset = len(obj.source)
	}
	return offset
}

// Span converts zero-based line/column pairs into a validated half-open span.
func (obj *File) Span(startLine, startColumn, endLine, endColumn int) Span {
	return NewSpan(
		obj,
		obj.Offset(startLine, startColumn),
		obj.Offset(endLine, endColumn),
	)
}
