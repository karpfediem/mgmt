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
	"strconv"
	"strings"

	"github.com/purpleidea/mgmt/lang/frontend/diag"
	"github.com/purpleidea/mgmt/lang/frontend/source"
	langtoken "github.com/purpleidea/mgmt/lang/frontend/token"
)

type delimiter struct {
	open   langtoken.Token
	close  langtoken.Kind
	closer string
}

func collectDiagnostics(tokens []langtoken.Token) []diag.Diagnostic {
	diagnostics := []diag.Diagnostic{}

	for _, tok := range tokens {
		if tok.Kind != langtoken.KindError {
			continue
		}
		diagnostics = append(diagnostics, diag.NewError(tok.Span, lexerDiagnosticMessage(tok)))
	}

	stack := []delimiter{}
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

func collectSymbols(tokens []langtoken.Token) []Symbol {
	items := syntaxTokens(tokens)
	symbols := []Symbol{}

	for i := 0; i < len(items); {
		if items[i].Kind == langtoken.KindNewline {
			i++
			continue
		}

		end := findStatementEnd(items, i)
		if end <= i {
			end = i + 1
		}

		if sym, ok := parseSymbol(items[i:end]); ok {
			symbols = append(symbols, sym)
		}

		i = end
		for i < len(items) && items[i].Kind == langtoken.KindNewline {
			i++
		}
	}

	return symbols
}

func syntaxTokens(tokens []langtoken.Token) []langtoken.Token {
	items := make([]langtoken.Token, 0, len(tokens))
	for _, tok := range tokens {
		switch tok.Kind {
		case langtoken.KindWhitespace, langtoken.KindComment, langtoken.KindEOF:
			continue
		default:
			items = append(items, tok)
		}
	}
	return items
}

func findStatementEnd(tokens []langtoken.Token, start int) int {
	curly := 0
	parens := 0
	brackets := 0

	for i := start; i < len(tokens); i++ {
		switch tokens[i].Kind {
		case langtoken.KindOpenCurly:
			curly++
		case langtoken.KindCloseCurly:
			if curly > 0 {
				curly--
			}
		case langtoken.KindOpenParen:
			parens++
		case langtoken.KindCloseParen:
			if parens > 0 {
				parens--
			}
		case langtoken.KindOpenBracket:
			brackets++
		case langtoken.KindCloseBracket:
			if brackets > 0 {
				brackets--
			}
		case langtoken.KindSemicolon:
			if curly == 0 && parens == 0 && brackets == 0 {
				return i + 1
			}
		case langtoken.KindNewline:
			if curly == 0 && parens == 0 && brackets == 0 {
				return i
			}
		}
	}

	return len(tokens)
}

func parseSymbol(tokens []langtoken.Token) (Symbol, bool) {
	if len(tokens) == 0 {
		return Symbol{}, false
	}

	switch tokens[0].Kind {
	case langtoken.KindImportIdentifier:
		if tok, ok := aliasOrNextToken(tokens[1:]); ok {
			return newSymbol(SymbolKindImport, label(tok.Raw), tokenSpan(tokens), tok.Span), true
		}
	case langtoken.KindIncludeIdentifier:
		if tok, ok := includeSymbolTarget(tokens[1:]); ok {
			return newSymbol(SymbolKindInclude, tok.name, tokenSpan(tokens), tok.span), true
		}
	case langtoken.KindFuncIdentifier:
		if tok, ok := nextIdentifier(tokens[1:]); ok {
			return newSymbol(SymbolKindFunction, tok.Raw, tokenSpan(tokens), tok.Span), true
		}
	case langtoken.KindClassIdentifier:
		if tok, ok := qualifiedTarget(tokens[1:], langtoken.KindColon, langtoken.KindOpenParen, langtoken.KindOpenCurly); ok {
			return newSymbol(SymbolKindClass, tok.name, tokenSpan(tokens), tok.span), true
		}
	case langtoken.KindCollectIdentifier:
		if header, ok := resourceHeader(tokens[1:]); ok {
			return newSymbol(SymbolKindResource, header.name, tokenSpan(tokens), header.span), true
		}
	case langtoken.KindDollar:
		if len(tokens) >= 3 && isIdentifierLike(tokens[1].Kind) && hasTopLevelEquals(tokens[2:]) {
			return newSymbol(SymbolKindVariable, tokens[1].Raw, tokenSpan(tokens), tokens[1].Span), true
		}
	default:
		if !isIdentifierLike(tokens[0].Kind) {
			return Symbol{}, false
		}
		if header, ok := resourceHeader(tokens); ok {
			return newSymbol(SymbolKindResource, header.name, tokenSpan(tokens), header.span), true
		}
	}

	return Symbol{}, false
}

func newSymbol(kind SymbolKind, name string, span source.Span, selection source.Span) Symbol {
	return Symbol{
		Name:          name,
		Kind:          kind,
		Span:          span,
		SelectionSpan: selection,
	}
}

func tokenSpan(tokens []langtoken.Token) source.Span {
	if len(tokens) == 0 {
		return source.Span{}
	}
	first := tokens[0]
	last := tokens[len(tokens)-1]
	return source.NewSpan(first.Span.File, first.Span.Start, last.Span.End)
}

func nextToken(tokens []langtoken.Token) (langtoken.Token, bool) {
	for _, tok := range tokens {
		if tok.Kind == langtoken.KindNewline {
			break
		}
		return tok, true
	}
	return langtoken.Token{}, false
}

func nextIdentifier(tokens []langtoken.Token) (langtoken.Token, bool) {
	for _, tok := range tokens {
		if tok.Kind == langtoken.KindNewline {
			break
		}
		if isIdentifierLike(tok.Kind) {
			return tok, true
		}
	}
	return langtoken.Token{}, false
}

func aliasOrNextToken(tokens []langtoken.Token) (langtoken.Token, bool) {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i].Kind == langtoken.KindAsIdentifier && tokens[i+1].Kind != langtoken.KindNewline {
			return tokens[i+1], true
		}
		if tokens[i].Kind == langtoken.KindNewline {
			break
		}
	}
	return nextToken(tokens)
}

func isIdentifierLike(kind langtoken.Kind) bool {
	switch kind {
	case langtoken.KindIdentifier,
		langtoken.KindCapitalizedIdentifier,
		langtoken.KindBoolIdentifier,
		langtoken.KindStrIdentifier,
		langtoken.KindIntIdentifier,
		langtoken.KindFloatIdentifier,
		langtoken.KindMapIdentifier,
		langtoken.KindStructIdentifier,
		langtoken.KindVariantIdentifier:
		return true
	default:
		return false
	}
}

func label(raw string) string {
	if value, err := strconv.Unquote(raw); err == nil {
		return value
	}
	return raw
}

type symbolTarget struct {
	name string
	span source.Span
}

func hasTopLevelEquals(tokens []langtoken.Token) bool {
	parens := 0
	brackets := 0
	curlies := 0
	for _, tok := range tokens {
		switch tok.Kind {
		case langtoken.KindOpenParen:
			parens++
		case langtoken.KindCloseParen:
			if parens > 0 {
				parens--
			}
		case langtoken.KindOpenBracket:
			brackets++
		case langtoken.KindCloseBracket:
			if brackets > 0 {
				brackets--
			}
		case langtoken.KindOpenCurly:
			curlies++
		case langtoken.KindCloseCurly:
			if curlies > 0 {
				curlies--
			}
		case langtoken.KindEquals:
			if parens == 0 && brackets == 0 && curlies == 0 {
				return true
			}
		}
	}
	return false
}

func includeSymbolTarget(tokens []langtoken.Token) (symbolTarget, bool) {
	if tok, ok := aliasTarget(tokens); ok {
		return tok, true
	}
	return qualifiedTarget(tokens, langtoken.KindDot, langtoken.KindOpenParen, langtoken.KindNewline)
}

func aliasTarget(tokens []langtoken.Token) (symbolTarget, bool) {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i].Kind == langtoken.KindNewline {
			break
		}
		if tokens[i].Kind == langtoken.KindAsIdentifier && tokens[i+1].Kind != langtoken.KindNewline {
			return symbolTarget{
				name: label(tokens[i+1].Raw),
				span: tokens[i+1].Span,
			}, true
		}
	}
	return symbolTarget{}, false
}

func qualifiedTarget(tokens []langtoken.Token, sep langtoken.Kind, terminators ...langtoken.Kind) (symbolTarget, bool) {
	if len(tokens) == 0 {
		return symbolTarget{}, false
	}

	end := 0
	expectIdent := true
	for end < len(tokens) {
		tok := tokens[end]
		if tok.Kind == langtoken.KindNewline || kindIn(tok.Kind, terminators...) {
			break
		}
		if expectIdent {
			if !isIdentifierLike(tok.Kind) {
				break
			}
			expectIdent = false
			end++
			continue
		}
		if tok.Kind != sep {
			break
		}
		expectIdent = true
		end++
	}

	if end == 0 || expectIdent {
		return symbolTarget{}, false
	}
	span := tokenSpan(tokens[:end])
	return symbolTarget{
		name: strings.TrimSpace(sourceSlice(span)),
		span: span,
	}, true
}

func resourceHeader(tokens []langtoken.Token) (symbolTarget, bool) {
	if len(tokens) == 0 {
		return symbolTarget{}, false
	}

	parens := 0
	brackets := 0
	for i, tok := range tokens {
		switch tok.Kind {
		case langtoken.KindOpenParen:
			parens++
		case langtoken.KindCloseParen:
			if parens > 0 {
				parens--
			}
		case langtoken.KindOpenBracket:
			brackets++
		case langtoken.KindCloseBracket:
			if brackets > 0 {
				brackets--
			}
		case langtoken.KindOpenCurly:
			if parens == 0 && brackets == 0 && i > 0 {
				span := source.NewSpan(tokens[0].Span.File, tokens[0].Span.Start, tokens[i-1].Span.End)
				return symbolTarget{
					name: strings.TrimSpace(sourceSlice(span)),
					span: span,
				}, true
			}
		}
	}
	return symbolTarget{}, false
}

func sourceSlice(span source.Span) string {
	if span.File == nil {
		return ""
	}
	buf := span.File.Bytes()
	if span.Start < 0 || span.End > len(buf) || span.Start > span.End {
		return ""
	}
	return string(buf[span.Start:span.End])
}

func kindIn(kind langtoken.Kind, kinds ...langtoken.Kind) bool {
	for _, candidate := range kinds {
		if kind == candidate {
			return true
		}
	}
	return false
}
