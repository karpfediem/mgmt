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

// Package token contains the syntax-preserving scanner output.
package token

// Kind is the token kind produced by the lossless scanner.
type Kind int

const (
	KindError Kind = iota
	KindEOF
	KindWhitespace
	KindNewline
	KindComment

	KindOpenCurly
	KindCloseCurly
	KindOpenParen
	KindCloseParen
	KindOpenBracket
	KindCloseBracket

	KindIf
	KindElse
	KindFor
	KindForkv
	KindBool
	KindString
	KindInteger
	KindFloat
	KindEquals
	KindDollar
	KindComma
	KindColon
	KindSemicolon
	KindElvis
	KindDefault
	KindRocket
	KindArrow
	KindDot

	KindBoolIdentifier
	KindStrIdentifier
	KindIntIdentifier
	KindFloatIdentifier
	KindMapIdentifier
	KindStructIdentifier
	KindVariantIdentifier
	KindIdentifier
	KindCapitalizedIdentifier
	KindFuncIdentifier
	KindClassIdentifier
	KindIncludeIdentifier
	KindImportIdentifier
	KindAsIdentifier
	KindCollectIdentifier
	KindPanicIdentifier

	KindAnd
	KindOr
	KindNot
	KindIn

	KindLT
	KindGT
	KindLTE
	KindGTE
	KindEQ
	KindNEQ
	KindPlus
	KindMinus
	KindMultiply
	KindDivide
)

var kindNames = [...]string{
	"error",
	"eof",
	"whitespace",
	"newline",
	"comment",
	"open_curly",
	"close_curly",
	"open_paren",
	"close_paren",
	"open_bracket",
	"close_bracket",
	"if",
	"else",
	"for",
	"forkv",
	"bool",
	"string",
	"integer",
	"float",
	"equals",
	"dollar",
	"comma",
	"colon",
	"semicolon",
	"elvis",
	"default",
	"rocket",
	"arrow",
	"dot",
	"bool_identifier",
	"str_identifier",
	"int_identifier",
	"float_identifier",
	"map_identifier",
	"struct_identifier",
	"variant_identifier",
	"identifier",
	"capitalized_identifier",
	"func_identifier",
	"class_identifier",
	"include_identifier",
	"import_identifier",
	"as_identifier",
	"collect_identifier",
	"panic_identifier",
	"and",
	"or",
	"not",
	"in",
	"lt",
	"gt",
	"lte",
	"gte",
	"eq",
	"neq",
	"plus",
	"minus",
	"multiply",
	"divide",
}

// String returns a debug-friendly token kind name.
func (obj Kind) String() string {
	if int(obj) < 0 || int(obj) >= len(kindNames) {
		return "unknown"
	}
	return kindNames[obj]
}

// IsTrivia reports whether this token is syntax trivia.
func (obj Kind) IsTrivia() bool {
	return obj == KindWhitespace || obj == KindNewline || obj == KindComment
}
