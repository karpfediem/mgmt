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

// Package diag contains reusable diagnostics emitted by frontend and semantic
// passes in the new frontend/tooling path.
package diag

import "github.com/purpleidea/mgmt/lang/frontend/source"

// Severity identifies the diagnostic level emitted by a pass.
type Severity string

const (
	SeverityError Severity = "error"
)

// Related annotates a diagnostic with an additional source location.
type Related struct {
	Span    source.Span
	Message string
}

// Diagnostic is a pass-owned message tied to source provenance.
type Diagnostic struct {
	Severity Severity
	Message  string
	Span     source.Span
	Related  []Related
}

// NewDiagnostic constructs a diagnostic with the given severity and message.
func NewDiagnostic(severity Severity, span source.Span, message string) Diagnostic {
	return Diagnostic{
		Severity: severity,
		Message:  message,
		Span:     span,
	}
}

// NewError constructs an error diagnostic.
func NewError(span source.Span, message string) Diagnostic {
	return NewDiagnostic(SeverityError, span, message)
}

// WithRelated returns a copy of this diagnostic with one related span added.
func (obj Diagnostic) WithRelated(span source.Span, message string) Diagnostic {
	obj.Related = append(obj.Related, Related{
		Span:    span,
		Message: message,
	})
	return obj
}
