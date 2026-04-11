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

// Span is a half-open byte range into a source snapshot.
type Span struct {
	File  *File
	Start int
	End   int
}

// NewSpan constructs a validated half-open span.
func NewSpan(file *File, start, end int) Span {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if file != nil && end > file.Len() {
		end = file.Len()
	}

	return Span{
		File:  file,
		Start: start,
		End:   end,
	}
}

// StartPosition returns the derived start position for this span.
func (obj Span) StartPosition() Position {
	if obj.File == nil {
		return Position{Offset: obj.Start}
	}
	return obj.File.Position(obj.Start)
}

// EndPosition returns the derived end position for this span.
func (obj Span) EndPosition() Position {
	if obj.File == nil {
		return Position{Offset: obj.End}
	}
	return obj.File.Position(obj.End)
}

// Len returns the length of this span in bytes.
func (obj Span) Len() int {
	return obj.End - obj.Start
}
