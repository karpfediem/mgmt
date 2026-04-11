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
	"bytes"
	"unicode/utf8"

	"github.com/purpleidea/mgmt/lang/frontend/source"
)

// Scanner tokenizes a single source snapshot into a lossless token stream.
type Scanner struct {
	file   *source.File
	offset int
}

// NewScanner returns a scanner for the supplied source snapshot.
func NewScanner(file *source.File) *Scanner {
	if file == nil {
		file = source.NewFile("", nil)
	}
	return &Scanner{
		file: file,
	}
}

// Scan returns all tokens, including trivia, errors, and a final EOF token.
func Scan(file *source.File) []Token {
	return NewScanner(file).Scan()
}

// Scan tokenizes the current source snapshot.
func (obj *Scanner) Scan() []Token {
	tokens := []Token{}
	for obj.offset < len(obj.file.Bytes()) {
		tokens = append(tokens, obj.scanOne())
	}
	eof := source.NewSpan(obj.file, len(obj.file.Bytes()), len(obj.file.Bytes()))
	tokens = append(tokens, Token{
		Kind: KindEOF,
		Raw:  "",
		Span: eof,
	})
	return tokens
}

func (obj *Scanner) scanOne() Token {
	sourceBytes := obj.file.Bytes()
	switch b := sourceBytes[obj.offset]; {
	case b == ' ' || b == '\t':
		return obj.scanWhitespace()
	case b == '\n':
		return obj.scanNewline()
	case b == '#':
		return obj.scanComment()
	case b == '"':
		return obj.scanString()
	case b == '-' && obj.hasDigit(obj.offset+1):
		return obj.scanNumber()
	case isDigit(b):
		return obj.scanNumber()
	case isLower(b):
		return obj.scanLowerIdentifier()
	case isUpper(b):
		return obj.scanUpperIdentifier()
	}

	if tok, ok := obj.scanOperatorOrPunct(); ok {
		return tok
	}

	return obj.scanError()
}

func (obj *Scanner) scanWhitespace() Token {
	sourceBytes := obj.file.Bytes()
	start := obj.offset
	for obj.offset < len(sourceBytes) {
		b := sourceBytes[obj.offset]
		if b != ' ' && b != '\t' {
			break
		}
		obj.offset++
	}
	return obj.emit(KindWhitespace, start, obj.offset)
}

func (obj *Scanner) scanNewline() Token {
	sourceBytes := obj.file.Bytes()
	start := obj.offset
	for obj.offset < len(sourceBytes) && sourceBytes[obj.offset] == '\n' {
		obj.offset++
	}
	return obj.emit(KindNewline, start, obj.offset)
}

func (obj *Scanner) scanComment() Token {
	sourceBytes := obj.file.Bytes()
	start := obj.offset
	for obj.offset < len(sourceBytes) {
		b := sourceBytes[obj.offset]
		if b == '\n' || b == '\r' {
			break
		}
		obj.offset++
	}
	return obj.emit(KindComment, start, obj.offset)
}

func (obj *Scanner) scanString() Token {
	sourceBytes := obj.file.Bytes()
	start := obj.offset
	obj.offset++

	for obj.offset < len(sourceBytes) {
		switch sourceBytes[obj.offset] {
		case '\\':
			obj.offset++
			if obj.offset < len(sourceBytes) {
				obj.offset++
			}
		case '"':
			obj.offset++
			return obj.emit(KindString, start, obj.offset)
		default:
			obj.offset++
		}
	}

	return obj.emit(KindError, start, obj.offset)
}

func (obj *Scanner) scanNumber() Token {
	sourceBytes := obj.file.Bytes()
	start := obj.offset
	if sourceBytes[obj.offset] == '-' {
		obj.offset++
	}

	for obj.offset < len(sourceBytes) && isDigit(sourceBytes[obj.offset]) {
		obj.offset++
	}

	kind := KindInteger
	if obj.offset+1 < len(sourceBytes) && sourceBytes[obj.offset] == '.' && isDigit(sourceBytes[obj.offset+1]) {
		kind = KindFloat
		obj.offset++
		for obj.offset < len(sourceBytes) && isDigit(sourceBytes[obj.offset]) {
			obj.offset++
		}
	}

	return obj.emit(kind, start, obj.offset)
}

func (obj *Scanner) scanLowerIdentifier() Token {
	sourceBytes := obj.file.Bytes()
	start := obj.offset
	obj.offset++
	validEnd := obj.offset

	for obj.offset < len(sourceBytes) {
		b := sourceBytes[obj.offset]
		if !isLower(b) && !isDigit(b) && b != '_' {
			break
		}
		obj.offset++
		if isLower(b) || isDigit(b) {
			validEnd = obj.offset
		}
	}

	obj.offset = validEnd
	return obj.emit(classifyLowerKeyword(string(sourceBytes[start:obj.offset])), start, obj.offset)
}

func (obj *Scanner) scanUpperIdentifier() Token {
	sourceBytes := obj.file.Bytes()
	start := obj.offset
	obj.offset++
	validEnd := obj.offset

	for obj.offset < len(sourceBytes) {
		b := sourceBytes[obj.offset]
		if !isLower(b) && !isDigit(b) && b != '_' {
			break
		}
		obj.offset++
		if isLower(b) || isDigit(b) {
			validEnd = obj.offset
		}
	}

	obj.offset = validEnd
	return obj.emit(KindCapitalizedIdentifier, start, obj.offset)
}

func (obj *Scanner) scanOperatorOrPunct() (Token, bool) {
	sourceBytes := obj.file.Bytes()
	start := obj.offset

	switch {
	case obj.match("?:"):
		obj.offset += 2
		return obj.emit(KindElvis, start, obj.offset), true
	case obj.match("||"):
		obj.offset += 2
		return obj.emit(KindDefault, start, obj.offset), true
	case obj.match("=>"):
		obj.offset += 2
		return obj.emit(KindRocket, start, obj.offset), true
	case obj.match("->"):
		obj.offset += 2
		return obj.emit(KindArrow, start, obj.offset), true
	case obj.match("=="):
		obj.offset += 2
		return obj.emit(KindEQ, start, obj.offset), true
	case obj.match("!="):
		obj.offset += 2
		return obj.emit(KindNEQ, start, obj.offset), true
	case obj.match("<="):
		obj.offset += 2
		return obj.emit(KindLTE, start, obj.offset), true
	case obj.match(">="):
		obj.offset += 2
		return obj.emit(KindGTE, start, obj.offset), true
	}

	switch sourceBytes[obj.offset] {
	case '{':
		obj.offset++
		return obj.emit(KindOpenCurly, start, obj.offset), true
	case '}':
		obj.offset++
		return obj.emit(KindCloseCurly, start, obj.offset), true
	case '(':
		obj.offset++
		return obj.emit(KindOpenParen, start, obj.offset), true
	case ')':
		obj.offset++
		return obj.emit(KindCloseParen, start, obj.offset), true
	case '[':
		obj.offset++
		return obj.emit(KindOpenBracket, start, obj.offset), true
	case ']':
		obj.offset++
		return obj.emit(KindCloseBracket, start, obj.offset), true
	case ',':
		obj.offset++
		return obj.emit(KindComma, start, obj.offset), true
	case ':':
		obj.offset++
		return obj.emit(KindColon, start, obj.offset), true
	case ';':
		obj.offset++
		return obj.emit(KindSemicolon, start, obj.offset), true
	case '=':
		obj.offset++
		return obj.emit(KindEquals, start, obj.offset), true
	case '+':
		obj.offset++
		return obj.emit(KindPlus, start, obj.offset), true
	case '-':
		obj.offset++
		return obj.emit(KindMinus, start, obj.offset), true
	case '*':
		obj.offset++
		return obj.emit(KindMultiply, start, obj.offset), true
	case '/':
		obj.offset++
		return obj.emit(KindDivide, start, obj.offset), true
	case '<':
		obj.offset++
		return obj.emit(KindLT, start, obj.offset), true
	case '>':
		obj.offset++
		return obj.emit(KindGT, start, obj.offset), true
	case '.':
		obj.offset++
		return obj.emit(KindDot, start, obj.offset), true
	case '$':
		obj.offset++
		return obj.emit(KindDollar, start, obj.offset), true
	}

	return Token{}, false
}

func (obj *Scanner) scanError() Token {
	sourceBytes := obj.file.Bytes()
	start := obj.offset
	_, size := utf8.DecodeRune(sourceBytes[obj.offset:])
	if size == 0 {
		size = 1
	}
	obj.offset += size
	return obj.emit(KindError, start, obj.offset)
}

func (obj *Scanner) emit(kind Kind, start, end int) Token {
	sourceBytes := obj.file.Bytes()
	return Token{
		Kind: kind,
		Raw:  string(sourceBytes[start:end]),
		Span: source.NewSpan(obj.file, start, end),
	}
}

func (obj *Scanner) match(prefix string) bool {
	return bytes.HasPrefix(obj.file.Bytes()[obj.offset:], []byte(prefix))
}

func (obj *Scanner) hasDigit(index int) bool {
	sourceBytes := obj.file.Bytes()
	return index < len(sourceBytes) && isDigit(sourceBytes[index])
}

func classifyLowerKeyword(lexeme string) Kind {
	switch lexeme {
	case "if":
		return KindIf
	case "else":
		return KindElse
	case "for":
		return KindFor
	case "forkv":
		return KindForkv
	case "bool":
		return KindBoolIdentifier
	case "str":
		return KindStrIdentifier
	case "int":
		return KindIntIdentifier
	case "float":
		return KindFloatIdentifier
	case "map":
		return KindMapIdentifier
	case "struct":
		return KindStructIdentifier
	case "variant":
		return KindVariantIdentifier
	case "func":
		return KindFuncIdentifier
	case "class":
		return KindClassIdentifier
	case "include":
		return KindIncludeIdentifier
	case "import":
		return KindImportIdentifier
	case "as":
		return KindAsIdentifier
	case "true", "false":
		return KindBool
	case "panic":
		return KindPanicIdentifier
	case "collect":
		return KindCollectIdentifier
	case "and":
		return KindAnd
	case "or":
		return KindOr
	case "not":
		return KindNot
	case "in":
		return KindIn
	default:
		return KindIdentifier
	}
}

func isLower(b byte) bool {
	return 'a' <= b && b <= 'z'
}

func isUpper(b byte) bool {
	return 'A' <= b && b <= 'Z'
}

func isDigit(b byte) bool {
	return '0' <= b && b <= '9'
}
