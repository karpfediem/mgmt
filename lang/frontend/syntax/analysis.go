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

package syntax

import (
	"fmt"

	"github.com/purpleidea/mgmt/lang/frontend/diag"
	langtoken "github.com/purpleidea/mgmt/lang/frontend/token"
)

type delimiter struct {
	open   langtoken.Token
	close  langtoken.Kind
	closer string
}

func collectDiagnostics(tokens []langtoken.Token) []diag.Diagnostic {
	var diagnostics []diag.Diagnostic

	for _, tok := range tokens {
		if tok.Kind != langtoken.KindError {
			continue
		}
		diagnostics = append(diagnostics, diag.NewError(tok.Span, lexerDiagnosticMessage(tok)))
	}

	var stack []delimiter
	for _, tok := range tokens {
		switch tok.Kind {
		case langtoken.KindOpenCurly:
			stack = append(stack, delimiter{open: tok, close: langtoken.KindCloseCurly, closer: "}"})
		case langtoken.KindOpenParen:
			stack = append(stack, delimiter{open: tok, close: langtoken.KindCloseParen, closer: ")"})
		case langtoken.KindOpenBracket:
			stack = append(stack, delimiter{open: tok, close: langtoken.KindCloseBracket, closer: "]"})
		case langtoken.KindCloseCurly, langtoken.KindCloseParen, langtoken.KindCloseBracket:
			if len(stack) == 0 {
				diagnostics = append(diagnostics, diag.NewError(tok.Span, fmt.Sprintf("unexpected closing delimiter %q", tok.Raw)))
				continue
			}

			top := stack[len(stack)-1]
			if top.close == tok.Kind {
				stack = stack[:len(stack)-1]
				continue
			}

			diagnostics = append(diagnostics, diag.NewError(tok.Span, fmt.Sprintf("mismatched closing delimiter %q, expected %q", tok.Raw, top.closer)).WithRelated(top.open.Span, "opening delimiter"))
			stack = stack[:len(stack)-1]
		default:
		}
	}

	for i := len(stack) - 1; i >= 0; i-- {
		top := stack[i]
		diagnostics = append(diagnostics, diag.NewError(top.open.Span, fmt.Sprintf("unclosed delimiter %q", top.open.Raw)))
	}

	return diagnostics
}

func lexerDiagnosticMessage(tok langtoken.Token) string {
	if len(tok.Raw) > 0 && tok.Raw[0] == '"' {
		return "unterminated string literal"
	}
	if tok.Raw == "\r" {
		return "unrecognized carriage return"
	}
	return fmt.Sprintf("unexpected character %q", tok.Raw)
}
