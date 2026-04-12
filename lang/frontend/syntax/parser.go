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
	"errors"
	"fmt"

	"github.com/purpleidea/mgmt/lang/frontend/diag"
	"github.com/purpleidea/mgmt/lang/frontend/source"
	langtoken "github.com/purpleidea/mgmt/lang/frontend/token"
)

type significantToken struct {
	token         langtoken.Token
	originalIndex int
}

type parseError struct {
	diagnostic diag.Diagnostic
}

func (obj *parseError) Error() string {
	return obj.diagnostic.Message
}

type parser struct {
	file  *source.File
	all   []langtoken.Token
	items []significantToken
	pos   int
}

func parseFileNode(file *source.File, tokens []langtoken.Token) (*FileNode, []diag.Diagnostic) {
	p := newParser(file, tokens)
	root, err := p.parse()
	if err == nil {
		return root, nil
	}

	var parseErr *parseError
	ok := errors.As(err, &parseErr)
	if !ok {
		eof := source.NewSpan(file, file.Len(), file.Len())
		return nil, []diag.Diagnostic{diag.NewError(eof, err.Error())}
	}
	return nil, []diag.Diagnostic{parseErr.diagnostic}
}

func newParser(file *source.File, tokens []langtoken.Token) *parser {
	items := make([]significantToken, 0, len(tokens))
	for i, tok := range tokens {
		switch tok.Kind {
		case langtoken.KindWhitespace, langtoken.KindNewline, langtoken.KindComment:
			continue
		default:
			items = append(items, significantToken{
				token:         tok,
				originalIndex: i,
			})
		}
	}
	return &parser{
		file:  file,
		all:   tokens,
		items: items,
	}
}

func (obj *parser) parse() (*FileNode, error) {
	if len(obj.items) == 0 {
		eof := len(obj.all) - 1
		return &FileNode{
			nodeInfo: obj.info(eof, eof),
			EOFToken: eof,
		}, nil
	}

	var statements []Statement
	for !obj.at(langtoken.KindEOF) {
		stmt, err := obj.parseStatement()
		if err != nil {
			return nil, err
		}
		statements = append(statements, stmt)
	}

	eof := obj.current().originalIndex
	info := obj.info(eof, eof)
	if len(statements) > 0 {
		info = obj.info(statements[0].StartTokenIndex(), eof)
	}
	return &FileNode{
		nodeInfo:   info,
		Statements: statements,
		EOFToken:   eof,
	}, nil
}

func (obj *parser) parseStatement() (Statement, error) {
	switch obj.current().token.Kind {
	case langtoken.KindDollar:
		return obj.parseBindStatement()
	case langtoken.KindPanicIdentifier:
		return obj.parsePanicStatement()
	case langtoken.KindCollectIdentifier:
		return obj.parseCollectStatement()
	case langtoken.KindIf:
		return obj.parseIfStatement()
	case langtoken.KindFor:
		return obj.parseForStatement()
	case langtoken.KindForkv:
		return obj.parseForKVStatement()
	case langtoken.KindFuncIdentifier:
		return obj.parseFunctionStatement()
	case langtoken.KindClassIdentifier:
		return obj.parseClassStatement()
	case langtoken.KindIncludeIdentifier:
		return obj.parseIncludeStatement()
	case langtoken.KindImportIdentifier:
		return obj.parseImportStatement()
	case langtoken.KindCapitalizedIdentifier:
		return obj.parseEdgeStatement()
	case langtoken.KindIdentifier:
		return obj.parseResourceStatement()
	default:
		return nil, obj.errorCurrent("statement")
	}
}

func (obj *parser) parseBindStatement() (Statement, error) {
	name, start, _, err := obj.parseVariableIdentifier()
	if err != nil {
		return nil, err
	}

	var typ TypeExpression
	if !obj.at(langtoken.KindEquals) {
		typ, err = obj.parseTypeExpression()
		if err != nil {
			return nil, err
		}
	}

	if _, err := obj.expect(langtoken.KindEquals, "\"=\""); err != nil {
		return nil, err
	}
	value, err := obj.parseExpression()
	if err != nil {
		return nil, err
	}

	return &BindStatement{
		nodeInfo: obj.info(start, value.EndTokenIndex()),
		Name:     name,
		Type:     typ,
		Value:    value,
	}, nil
}

func (obj *parser) parsePanicStatement() (Statement, error) {
	keyword := obj.consume()
	openParen, closeParen, args, err := obj.parseRequiredCallSection()
	if err != nil {
		return nil, err
	}

	return &PanicStatement{
		nodeInfo:     obj.info(keyword.originalIndex, closeParen),
		KeywordToken: keyword.originalIndex,
		OpenParen:    openParen,
		CloseParen:   closeParen,
		Args:         args,
	}, nil
}

func (obj *parser) parseCollectStatement() (Statement, error) {
	keyword := obj.consume()
	resource, err := obj.parseResourceStatementNode()
	if err != nil {
		return nil, err
	}

	return &CollectStatement{
		nodeInfo:     obj.info(keyword.originalIndex, resource.EndTokenIndex()),
		KeywordToken: keyword.originalIndex,
		Resource:     resource,
	}, nil
}

func (obj *parser) parseIfStatement() (Statement, error) {
	keyword := obj.consume()
	condition, err := obj.parseExpression()
	if err != nil {
		return nil, err
	}
	thenBlock, err := obj.parseBlock()
	if err != nil {
		return nil, err
	}

	stmt := &IfStatement{
		nodeInfo:  obj.info(keyword.originalIndex, thenBlock.EndTokenIndex()),
		IfToken:   keyword.originalIndex,
		ElseToken: -1,
		Condition: condition,
		Then:      thenBlock,
	}

	if obj.at(langtoken.KindElse) {
		elseTok := obj.consume()
		elseBlock, err := obj.parseBlock()
		if err != nil {
			return nil, err
		}
		stmt.nodeInfo = obj.info(keyword.originalIndex, elseBlock.EndTokenIndex())
		stmt.ElseToken = elseTok.originalIndex
		stmt.Else = elseBlock
	}

	return stmt, nil
}

func (obj *parser) parseForStatement() (Statement, error) {
	keyword := obj.consume()
	indexName, valueName, commaToken, inToken, iterable, body, err := obj.parseLoopHeader()
	if err != nil {
		return nil, err
	}

	return &ForStatement{
		nodeInfo:     obj.info(keyword.originalIndex, body.EndTokenIndex()),
		KeywordToken: keyword.originalIndex,
		CommaToken:   commaToken,
		InToken:      inToken,
		IndexName:    indexName,
		ValueName:    valueName,
		Iterable:     iterable,
		Body:         body,
	}, nil
}

func (obj *parser) parseForKVStatement() (Statement, error) {
	keyword := obj.consume()
	keyName, valueName, commaToken, inToken, iterable, body, err := obj.parseLoopHeader()
	if err != nil {
		return nil, err
	}

	return &ForKVStatement{
		nodeInfo:     obj.info(keyword.originalIndex, body.EndTokenIndex()),
		KeywordToken: keyword.originalIndex,
		CommaToken:   commaToken,
		InToken:      inToken,
		KeyName:      keyName,
		ValueName:    valueName,
		Iterable:     iterable,
		Body:         body,
	}, nil
}

func (obj *parser) parseFunctionStatement() (Statement, error) {
	keyword := obj.consume()
	nameTok, err := obj.expect(langtoken.KindIdentifier, "identifier")
	if err != nil {
		return nil, err
	}
	openParen, closeParen, parameters, returnType, body, err := obj.parseFunctionTail()
	if err != nil {
		return nil, err
	}

	return &FunctionStatement{
		nodeInfo:     obj.info(keyword.originalIndex, body.EndTokenIndex()),
		KeywordToken: keyword.originalIndex,
		Name:         nameTok.token.Raw,
		OpenParen:    openParen,
		CloseParen:   closeParen,
		Parameters:   parameters,
		ReturnType:   returnType,
		Body:         body,
	}, nil
}

func (obj *parser) parseClassStatement() (Statement, error) {
	keyword := obj.consume()
	parts, start, _, err := obj.parseColonIdentifier()
	if err != nil {
		return nil, err
	}

	openParen, closeParen, parameters, err := obj.parseOptionalParameterSection()
	if err != nil {
		return nil, err
	}

	body, err := obj.parseBlock()
	if err != nil {
		return nil, err
	}

	return &ClassStatement{
		nodeInfo:     obj.info(start, body.EndTokenIndex()),
		KeywordToken: keyword.originalIndex,
		NameParts:    parts,
		OpenParen:    openParen,
		CloseParen:   closeParen,
		Parameters:   parameters,
		Body:         body,
	}, nil
}

func (obj *parser) parseIncludeStatement() (Statement, error) {
	keyword := obj.consume()
	nameParts, start, _, err := obj.parseDottedIdentifier()
	if err != nil {
		return nil, err
	}

	openParen, closeParen, arguments, err := obj.parseOptionalCallSection()
	if err != nil {
		return nil, err
	}

	asToken := -1
	alias := ""
	end := obj.previousOriginalIndex()
	if obj.at(langtoken.KindAsIdentifier) {
		asTok := obj.consume()
		asToken = asTok.originalIndex
		aliasTok, err := obj.expect(langtoken.KindIdentifier, "identifier")
		if err != nil {
			return nil, err
		}
		alias = aliasTok.token.Raw
		end = aliasTok.originalIndex
	}

	if end < start {
		end = start
	}
	return &IncludeStatement{
		nodeInfo:     obj.info(keyword.originalIndex, end),
		KeywordToken: keyword.originalIndex,
		NameParts:    nameParts,
		OpenParen:    openParen,
		CloseParen:   closeParen,
		Arguments:    arguments,
		AsToken:      asToken,
		Alias:        alias,
	}, nil
}

func (obj *parser) parseImportStatement() (Statement, error) {
	keyword := obj.consume()
	pathTok, err := obj.expect(langtoken.KindString, "string literal")
	if err != nil {
		return nil, err
	}

	asToken := -1
	alias := ""
	aliasAll := false
	end := pathTok.originalIndex
	if obj.at(langtoken.KindAsIdentifier) {
		asTok := obj.consume()
		asToken = asTok.originalIndex
		switch obj.current().token.Kind {
		case langtoken.KindIdentifier:
			aliasTok := obj.consume()
			alias = aliasTok.token.Raw
			end = aliasTok.originalIndex
		case langtoken.KindMultiply:
			starTok := obj.consume()
			alias = "*"
			aliasAll = true
			end = starTok.originalIndex
		default:
			return nil, obj.errorCurrent("identifier or \"*\"")
		}
	}

	return &ImportStatement{
		nodeInfo:     obj.info(keyword.originalIndex, end),
		KeywordToken: keyword.originalIndex,
		PathRaw:      pathTok.token.Raw,
		AsToken:      asToken,
		Alias:        alias,
		AliasAll:     aliasAll,
	}, nil
}

func (obj *parser) parseResourceStatement() (Statement, error) {
	return obj.parseResourceStatementNode()
}

func (obj *parser) parseResourceStatementNode() (*ResourceStatement, error) {
	parts, start, _, err := obj.parseColonIdentifier()
	if err != nil {
		return nil, err
	}
	name, err := obj.parseExpression()
	if err != nil {
		return nil, err
	}
	openBrace, err := obj.expect(langtoken.KindOpenCurly, "\"{\"")
	if err != nil {
		return nil, err
	}

	var entries []ResourceEntry
	for !obj.at(langtoken.KindCloseCurly) {
		if obj.at(langtoken.KindEOF) {
			return nil, obj.errorCurrent("\"}\"")
		}
		entry, err := obj.parseResourceEntry()
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	closeBrace, err := obj.expect(langtoken.KindCloseCurly, "\"}\"")
	if err != nil {
		return nil, err
	}

	return &ResourceStatement{
		nodeInfo:   obj.info(start, closeBrace.originalIndex),
		KindParts:  parts,
		Name:       name,
		OpenBrace:  openBrace.originalIndex,
		CloseBrace: closeBrace.originalIndex,
		Entries:    entries,
	}, nil
}

func (obj *parser) parseEdgeStatement() (Statement, error) {
	first, err := obj.parseEdgeHalf()
	if err != nil {
		return nil, err
	}

	var items []*EdgeChainItem
	sendRecv := first.Field != ""
	currentHalf := first
	for obj.at(langtoken.KindArrow) {
		arrow := obj.consume()
		items = append(items, &EdgeChainItem{
			nodeInfo: obj.info(currentHalf.StartTokenIndex(), arrow.originalIndex),
			Half:     currentHalf,
		})

		next, err := obj.parseEdgeHalf()
		if err != nil {
			return nil, err
		}
		if sendRecv && len(items) > 1 {
			return nil, obj.errorAt(next.Span(), "unexpected additional send/recv edge target")
		}
		if sendRecv != (next.Field != "") {
			return nil, obj.errorAt(next.Span(), "mixed edge kinds in edge statement")
		}
		currentHalf = next
	}

	items = append(items, &EdgeChainItem{
		nodeInfo: obj.info(currentHalf.StartTokenIndex(), currentHalf.EndTokenIndex()),
		Half:     currentHalf,
	})

	if sendRecv && len(items) != 2 {
		return nil, obj.errorAt(first.Span(), "send/recv edges must contain exactly two halves")
	}

	return &EdgeStatement{
		nodeInfo: obj.info(items[0].StartTokenIndex(), items[len(items)-1].EndTokenIndex()),
		SendRecv: sendRecv,
		Items:    items,
	}, nil
}

func (obj *parser) parseBlock() (*Block, error) {
	openBrace, err := obj.expect(langtoken.KindOpenCurly, "\"{\"")
	if err != nil {
		return nil, err
	}

	var statements []Statement
	for !obj.at(langtoken.KindCloseCurly) {
		if obj.at(langtoken.KindEOF) {
			return nil, obj.errorCurrent("\"}\"")
		}
		stmt, err := obj.parseStatement()
		if err != nil {
			return nil, err
		}
		statements = append(statements, stmt)
	}

	closeBrace, err := obj.expect(langtoken.KindCloseCurly, "\"}\"")
	if err != nil {
		return nil, err
	}

	return &Block{
		nodeInfo:   obj.info(openBrace.originalIndex, closeBrace.originalIndex),
		OpenBrace:  openBrace.originalIndex,
		CloseBrace: closeBrace.originalIndex,
		Statements: statements,
	}, nil
}

func (obj *parser) parseExpressionBlock() (*ExpressionBlock, error) {
	openBrace, err := obj.expect(langtoken.KindOpenCurly, "\"{\"")
	if err != nil {
		return nil, err
	}
	value, err := obj.parseExpression()
	if err != nil {
		return nil, err
	}
	closeBrace, err := obj.expect(langtoken.KindCloseCurly, "\"}\"")
	if err != nil {
		return nil, err
	}

	return &ExpressionBlock{
		nodeInfo:   obj.info(openBrace.originalIndex, closeBrace.originalIndex),
		OpenBrace:  openBrace.originalIndex,
		CloseBrace: closeBrace.originalIndex,
		Value:      value,
	}, nil
}

func (obj *parser) parseResourceEntry() (ResourceEntry, error) {
	tok := obj.current()
	if tok.token.Kind == langtoken.KindCapitalizedIdentifier && tok.token.Raw == "Meta" {
		return obj.parseResourceMetaEntry()
	}
	if tok.token.Kind == langtoken.KindIdentifier {
		return obj.parseResourceFieldEntry()
	}
	if tok.token.Kind == langtoken.KindCapitalizedIdentifier {
		return obj.parseResourceEdgeEntry()
	}
	return nil, obj.errorCurrent("resource body entry")
}

func (obj *parser) parseResourceFieldEntry() (ResourceEntry, error) {
	nameTok, err := obj.expect(langtoken.KindIdentifier, "identifier")
	if err != nil {
		return nil, err
	}
	value, alt, end, err := obj.parseResourceValueTail()
	if err != nil {
		return nil, err
	}

	return &ResourceFieldEntry{
		nodeInfo:    obj.info(nameTok.originalIndex, end),
		Name:        nameTok.token.Raw,
		Value:       value,
		Alternative: alt,
	}, nil
}

func (obj *parser) parseResourceEdgeEntry() (ResourceEntry, error) {
	nameTok, err := obj.expect(langtoken.KindCapitalizedIdentifier, "capitalized identifier")
	if err != nil {
		return nil, err
	}
	if _, err := obj.expect(langtoken.KindRocket, "\"=>\""); err != nil {
		return nil, err
	}

	var condition Expression
	var target *EdgeHalf
	if obj.at(langtoken.KindCapitalizedIdentifier) {
		target, err = obj.parseEdgeHalf()
		if err != nil {
			return nil, err
		}
	} else {
		condition, err = obj.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := obj.expect(langtoken.KindElvis, "\"?:\""); err != nil {
			return nil, err
		}
		target, err = obj.parseEdgeHalf()
		if err != nil {
			return nil, err
		}
	}

	comma, err := obj.expect(langtoken.KindComma, "\",\"")
	if err != nil {
		return nil, err
	}

	return &ResourceEdgeEntry{
		nodeInfo:  obj.info(nameTok.originalIndex, comma.originalIndex),
		Name:      nameTok.token.Raw,
		Condition: condition,
		Target:    target,
	}, nil
}

func (obj *parser) parseResourceMetaEntry() (ResourceEntry, error) {
	headTok := obj.consume()
	name := ""
	if obj.at(langtoken.KindColon) {
		obj.consume()
		nameTok, err := obj.expect(langtoken.KindIdentifier, "identifier")
		if err != nil {
			return nil, err
		}
		name = nameTok.token.Raw
	}
	value, alt, end, err := obj.parseResourceValueTail()
	if err != nil {
		return nil, err
	}

	return &ResourceMetaEntry{
		nodeInfo:    obj.info(headTok.originalIndex, end),
		HeadRaw:     headTok.token.Raw,
		Name:        name,
		Value:       value,
		Alternative: alt,
	}, nil
}

func (obj *parser) parseResourceValueTail() (Expression, Expression, int, error) {
	if _, err := obj.expect(langtoken.KindRocket, "\"=>\""); err != nil {
		return nil, nil, 0, err
	}
	value, err := obj.parseExpression()
	if err != nil {
		return nil, nil, 0, err
	}

	var alt Expression
	if obj.at(langtoken.KindElvis) {
		obj.consume()
		alt, err = obj.parseExpression()
		if err != nil {
			return nil, nil, 0, err
		}
	}

	comma, err := obj.expect(langtoken.KindComma, "\",\"")
	if err != nil {
		return nil, nil, 0, err
	}
	return value, alt, comma.originalIndex, nil
}

func (obj *parser) parseEdgeHalf() (*EdgeHalf, error) {
	parts, start, _, err := obj.parseCapitalizedResourceIdentifier()
	if err != nil {
		return nil, err
	}
	if _, err := obj.expect(langtoken.KindOpenBracket, "\"[\""); err != nil {
		return nil, err
	}
	name, err := obj.parseExpression()
	if err != nil {
		return nil, err
	}
	closeBracket, err := obj.expect(langtoken.KindCloseBracket, "\"]\"")
	if err != nil {
		return nil, err
	}

	field := ""
	end := closeBracket.originalIndex
	if obj.at(langtoken.KindDot) {
		obj.consume()
		fieldTok, err := obj.expect(langtoken.KindIdentifier, "identifier")
		if err != nil {
			return nil, err
		}
		field = fieldTok.token.Raw
		end = fieldTok.originalIndex
	}

	return &EdgeHalf{
		nodeInfo:  obj.info(start, end),
		KindParts: parts,
		Name:      name,
		Field:     field,
	}, nil
}

func (obj *parser) parseExpression() (Expression, error) {
	return obj.parseLogicalExpression()
}

func (obj *parser) parseLogicalExpression() (Expression, error) {
	return obj.parseBinaryLeftAssociative(obj.parseComparisonExpression, langtoken.KindAnd, langtoken.KindOr)
}

func (obj *parser) parseComparisonExpression() (Expression, error) {
	return obj.parseBinaryOptional(obj.parseAdditiveExpression, langtoken.KindEQ, langtoken.KindNEQ, langtoken.KindLT, langtoken.KindGT, langtoken.KindLTE, langtoken.KindGTE)
}

func (obj *parser) parseAdditiveExpression() (Expression, error) {
	return obj.parseBinaryLeftAssociative(obj.parseMultiplicativeExpression, langtoken.KindPlus, langtoken.KindMinus)
}

func (obj *parser) parseMultiplicativeExpression() (Expression, error) {
	return obj.parseBinaryLeftAssociative(obj.parseUnaryExpression, langtoken.KindMultiply, langtoken.KindDivide)
}

func (obj *parser) parseUnaryExpression() (Expression, error) {
	if obj.at(langtoken.KindNot) {
		op := obj.consume()
		operand, err := obj.parseUnaryExpression()
		if err != nil {
			return nil, err
		}
		return &UnaryExpression{
			nodeInfo:      obj.info(op.originalIndex, operand.EndTokenIndex()),
			OperatorToken: op.originalIndex,
			OperatorRaw:   op.token.Raw,
			Operand:       operand,
		}, nil
	}
	return obj.parseMembershipExpression()
}

func (obj *parser) parseMembershipExpression() (Expression, error) {
	return obj.parseBinaryOptional(obj.parsePostfixExpression, langtoken.KindIn)
}

func (obj *parser) parsePostfixExpression() (Expression, error) {
	left, err := obj.parsePrimaryExpression()
	if err != nil {
		return nil, err
	}
	for {
		switch obj.current().token.Kind {
		case langtoken.KindOpenBracket:
			openBracket := obj.consume()
			index, err := obj.parseExpression()
			if err != nil {
				return nil, err
			}
			closeBracket, err := obj.expect(langtoken.KindCloseBracket, "\"]\"")
			if err != nil {
				return nil, err
			}

			defaultTok, defaultValue, end, err := obj.parseOptionalDefaultValue(closeBracket.originalIndex)
			if err != nil {
				return nil, err
			}

			left = &IndexExpression{
				nodeInfo:     obj.info(left.StartTokenIndex(), end),
				Target:       left,
				OpenBracket:  openBracket.originalIndex,
				CloseBracket: closeBracket.originalIndex,
				Index:        index,
				DefaultToken: defaultTok,
				DefaultValue: defaultValue,
			}
		case langtoken.KindArrow:
			arrow := obj.consume()
			fieldTok, err := obj.expect(langtoken.KindIdentifier, "identifier")
			if err != nil {
				return nil, err
			}

			defaultTok, defaultValue, end, err := obj.parseOptionalDefaultValue(fieldTok.originalIndex)
			if err != nil {
				return nil, err
			}

			left = &FieldExpression{
				nodeInfo:     obj.info(left.StartTokenIndex(), end),
				Target:       left,
				ArrowToken:   arrow.originalIndex,
				Field:        fieldTok.token.Raw,
				DefaultToken: defaultTok,
				DefaultValue: defaultValue,
			}
		default:
			return left, nil
		}
	}
}

func (obj *parser) parsePrimaryExpression() (Expression, error) {
	switch obj.current().token.Kind {
	case langtoken.KindBool:
		tok := obj.consume()
		return &BoolLiteral{
			nodeInfo: obj.info(tok.originalIndex, tok.originalIndex),
			Raw:      tok.token.Raw,
		}, nil
	case langtoken.KindString:
		tok := obj.consume()
		return &StringLiteral{
			nodeInfo: obj.info(tok.originalIndex, tok.originalIndex),
			Raw:      tok.token.Raw,
		}, nil
	case langtoken.KindInteger:
		tok := obj.consume()
		return &IntegerLiteral{
			nodeInfo: obj.info(tok.originalIndex, tok.originalIndex),
			Raw:      tok.token.Raw,
		}, nil
	case langtoken.KindFloat:
		tok := obj.consume()
		return &FloatLiteral{
			nodeInfo: obj.info(tok.originalIndex, tok.originalIndex),
			Raw:      tok.token.Raw,
		}, nil
	case langtoken.KindOpenBracket:
		return obj.parseListLiteral()
	case langtoken.KindOpenCurly:
		return obj.parseMapLiteral()
	case langtoken.KindStructIdentifier:
		return obj.parseStructLiteral()
	case langtoken.KindIdentifier, langtoken.KindMapIdentifier, langtoken.KindCollectIdentifier:
		return obj.parseNamedCallExpression()
	case langtoken.KindDollar:
		return obj.parseVariablePrimary()
	case langtoken.KindFuncIdentifier:
		return obj.parseFunctionPrimary()
	case langtoken.KindIf:
		return obj.parseIfExpression()
	case langtoken.KindOpenParen:
		return obj.parseParenExpression()
	default:
		return nil, obj.errorCurrent("expression")
	}
}

func (obj *parser) parseListLiteral() (Expression, error) {
	openBracket := obj.consume()
	var elements []*ListElement
	for !obj.at(langtoken.KindCloseBracket) {
		if obj.at(langtoken.KindEOF) {
			return nil, obj.errorCurrent("\"]\"")
		}
		value, err := obj.parseExpression()
		if err != nil {
			return nil, err
		}
		comma, err := obj.expect(langtoken.KindComma, "\",\"")
		if err != nil {
			return nil, err
		}
		elements = append(elements, &ListElement{
			nodeInfo: obj.info(value.StartTokenIndex(), comma.originalIndex),
			Value:    value,
		})
	}
	closeBracket := obj.consume()
	return &ListLiteral{
		nodeInfo:     obj.info(openBracket.originalIndex, closeBracket.originalIndex),
		OpenBracket:  openBracket.originalIndex,
		CloseBracket: closeBracket.originalIndex,
		Elements:     elements,
	}, nil
}

func (obj *parser) parseMapLiteral() (Expression, error) {
	openBrace := obj.consume()
	var entries []*MapEntry
	for !obj.at(langtoken.KindCloseCurly) {
		if obj.at(langtoken.KindEOF) {
			return nil, obj.errorCurrent("\"}\"")
		}
		key, err := obj.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := obj.expect(langtoken.KindRocket, "\"=>\""); err != nil {
			return nil, err
		}
		value, err := obj.parseExpression()
		if err != nil {
			return nil, err
		}
		comma, err := obj.expect(langtoken.KindComma, "\",\"")
		if err != nil {
			return nil, err
		}
		entries = append(entries, &MapEntry{
			nodeInfo: obj.info(key.StartTokenIndex(), comma.originalIndex),
			Key:      key,
			Value:    value,
		})
	}
	closeBrace := obj.consume()
	return &MapLiteral{
		nodeInfo:   obj.info(openBrace.originalIndex, closeBrace.originalIndex),
		OpenBrace:  openBrace.originalIndex,
		CloseBrace: closeBrace.originalIndex,
		Entries:    entries,
	}, nil
}

func (obj *parser) parseStructLiteral() (Expression, error) {
	keyword := obj.consume()
	openBrace, err := obj.expect(langtoken.KindOpenCurly, "\"{\"")
	if err != nil {
		return nil, err
	}
	var fields []*StructLiteralField
	for !obj.at(langtoken.KindCloseCurly) {
		if obj.at(langtoken.KindEOF) {
			return nil, obj.errorCurrent("\"}\"")
		}
		nameTok, err := obj.expect(langtoken.KindIdentifier, "identifier")
		if err != nil {
			return nil, err
		}
		if _, err := obj.expect(langtoken.KindRocket, "\"=>\""); err != nil {
			return nil, err
		}
		value, err := obj.parseExpression()
		if err != nil {
			return nil, err
		}
		comma, err := obj.expect(langtoken.KindComma, "\",\"")
		if err != nil {
			return nil, err
		}
		fields = append(fields, &StructLiteralField{
			nodeInfo: obj.info(nameTok.originalIndex, comma.originalIndex),
			Name:     nameTok.token.Raw,
			Value:    value,
		})
	}
	closeBrace := obj.consume()
	return &StructLiteral{
		nodeInfo:     obj.info(keyword.originalIndex, closeBrace.originalIndex),
		KeywordToken: keyword.originalIndex,
		OpenBrace:    openBrace.originalIndex,
		CloseBrace:   closeBrace.originalIndex,
		Fields:       fields,
	}, nil
}

func (obj *parser) parseNamedCallExpression() (Expression, error) {
	nameParts, start, _, err := obj.parseDottedIdentifier()
	if err != nil {
		return nil, err
	}
	openParen, closeParen, args, err := obj.parseRequiredCallSection()
	if err != nil {
		return nil, err
	}
	return &NamedCallExpression{
		nodeInfo:   obj.info(start, closeParen),
		NameParts:  nameParts,
		OpenParen:  openParen,
		CloseParen: closeParen,
		Arguments:  args,
	}, nil
}

func (obj *parser) parseVariablePrimary() (Expression, error) {
	parts, start, _, err := obj.parseDottedVariableIdentifier()
	if err != nil {
		return nil, err
	}
	if !obj.at(langtoken.KindOpenParen) {
		end := obj.previousOriginalIndex()
		return &VariableExpression{
			nodeInfo:  obj.info(start, end),
			NameParts: parts,
		}, nil
	}

	openParen, closeParen, args, err := obj.parseRequiredCallSection()
	if err != nil {
		return nil, err
	}
	return &VariableCallExpression{
		nodeInfo:   obj.info(start, closeParen),
		NameParts:  parts,
		OpenParen:  openParen,
		CloseParen: closeParen,
		Arguments:  args,
	}, nil
}

func (obj *parser) parseFunctionPrimary() (Expression, error) {
	fn, err := obj.parseFunctionExpression()
	if err != nil {
		return nil, err
	}
	if !obj.at(langtoken.KindOpenParen) {
		return fn, nil
	}

	openParen, closeParen, args, err := obj.parseRequiredCallSection()
	if err != nil {
		return nil, err
	}
	return &AnonymousCallExpression{
		nodeInfo:   obj.info(fn.StartTokenIndex(), closeParen),
		Callee:     fn,
		OpenParen:  openParen,
		CloseParen: closeParen,
		Arguments:  args,
	}, nil
}

func (obj *parser) parseFunctionExpression() (*FunctionExpression, error) {
	keyword := obj.consume()
	openParen, closeParen, parameters, returnType, body, err := obj.parseFunctionTail()
	if err != nil {
		return nil, err
	}
	return &FunctionExpression{
		nodeInfo:     obj.info(keyword.originalIndex, body.EndTokenIndex()),
		KeywordToken: keyword.originalIndex,
		OpenParen:    openParen,
		CloseParen:   closeParen,
		Parameters:   parameters,
		ReturnType:   returnType,
		Body:         body,
	}, nil
}

func (obj *parser) parseIfExpression() (Expression, error) {
	keyword := obj.consume()
	condition, err := obj.parseExpression()
	if err != nil {
		return nil, err
	}
	thenBlock, err := obj.parseExpressionBlock()
	if err != nil {
		return nil, err
	}
	elseTok, err := obj.expect(langtoken.KindElse, "\"else\"")
	if err != nil {
		return nil, err
	}
	elseBlock, err := obj.parseExpressionBlock()
	if err != nil {
		return nil, err
	}
	return &IfExpression{
		nodeInfo:  obj.info(keyword.originalIndex, elseBlock.EndTokenIndex()),
		IfToken:   keyword.originalIndex,
		ElseToken: elseTok.originalIndex,
		Condition: condition,
		Then:      thenBlock,
		Else:      elseBlock,
	}, nil
}

func (obj *parser) parseParenExpression() (Expression, error) {
	openParen := obj.consume()
	inner, err := obj.parseExpression()
	if err != nil {
		return nil, err
	}
	closeParen, err := obj.expect(langtoken.KindCloseParen, "\")\"")
	if err != nil {
		return nil, err
	}
	return &ParenExpression{
		nodeInfo:   obj.info(openParen.originalIndex, closeParen.originalIndex),
		OpenParen:  openParen.originalIndex,
		CloseParen: closeParen.originalIndex,
		Inner:      inner,
	}, nil
}

func (obj *parser) parseTypeExpression() (TypeExpression, error) {
	switch obj.current().token.Kind {
	case langtoken.KindBoolIdentifier, langtoken.KindStrIdentifier, langtoken.KindIntIdentifier, langtoken.KindFloatIdentifier, langtoken.KindVariantIdentifier:
		tok := obj.consume()
		return &PrimitiveType{
			nodeInfo: obj.info(tok.originalIndex, tok.originalIndex),
			Name:     tok.token.Raw,
		}, nil
	case langtoken.KindOpenBracket:
		openBracket := obj.consume()
		closeBracket, err := obj.expect(langtoken.KindCloseBracket, "\"]\"")
		if err != nil {
			return nil, err
		}
		element, err := obj.parseTypeExpression()
		if err != nil {
			return nil, err
		}
		return &ListType{
			nodeInfo:     obj.info(openBracket.originalIndex, element.EndTokenIndex()),
			OpenBracket:  openBracket.originalIndex,
			CloseBracket: closeBracket.originalIndex,
			Element:      element,
		}, nil
	case langtoken.KindMapIdentifier:
		return obj.parseMapType()
	case langtoken.KindStructIdentifier:
		return obj.parseStructType()
	case langtoken.KindFuncIdentifier:
		return obj.parseFunctionType()
	default:
		return nil, obj.errorCurrent("type expression")
	}
}

func (obj *parser) parseMapType() (TypeExpression, error) {
	keyword := obj.consume()
	openBrace, err := obj.expect(langtoken.KindOpenCurly, "\"{\"")
	if err != nil {
		return nil, err
	}
	key, err := obj.parseTypeExpression()
	if err != nil {
		return nil, err
	}
	if _, err := obj.expect(langtoken.KindColon, "\":\""); err != nil {
		return nil, err
	}
	value, err := obj.parseTypeExpression()
	if err != nil {
		return nil, err
	}
	closeBrace, err := obj.expect(langtoken.KindCloseCurly, "\"}\"")
	if err != nil {
		return nil, err
	}
	return &MapType{
		nodeInfo:     obj.info(keyword.originalIndex, closeBrace.originalIndex),
		KeywordToken: keyword.originalIndex,
		OpenBrace:    openBrace.originalIndex,
		CloseBrace:   closeBrace.originalIndex,
		Key:          key,
		Value:        value,
	}, nil
}

func (obj *parser) parseStructType() (TypeExpression, error) {
	keyword := obj.consume()
	openBrace, err := obj.expect(langtoken.KindOpenCurly, "\"{\"")
	if err != nil {
		return nil, err
	}
	var fields []*StructTypeField
	if !obj.at(langtoken.KindCloseCurly) {
		for {
			nameTok, err := obj.expect(langtoken.KindIdentifier, "identifier")
			if err != nil {
				return nil, err
			}
			typ, err := obj.parseTypeExpression()
			if err != nil {
				return nil, err
			}
			end := typ.EndTokenIndex()
			consumedSemi := false
			if obj.at(langtoken.KindSemicolon) {
				semi := obj.consume()
				end = semi.originalIndex
				consumedSemi = true
				if obj.at(langtoken.KindCloseCurly) {
					return nil, obj.errorCurrent("struct type field")
				}
			}
			fields = append(fields, &StructTypeField{
				nodeInfo: obj.info(nameTok.originalIndex, end),
				Name:     nameTok.token.Raw,
				Type:     typ,
			})
			if !consumedSemi {
				break
			}
		}
	}
	closeBrace, err := obj.expect(langtoken.KindCloseCurly, "\"}\"")
	if err != nil {
		return nil, err
	}
	return &StructType{
		nodeInfo:     obj.info(keyword.originalIndex, closeBrace.originalIndex),
		KeywordToken: keyword.originalIndex,
		OpenBrace:    openBrace.originalIndex,
		CloseBrace:   closeBrace.originalIndex,
		Fields:       fields,
	}, nil
}

func (obj *parser) parseFunctionType() (TypeExpression, error) {
	keyword := obj.consume()
	openParen, closeParen, args, err := obj.parseRequiredFunctionTypeArgumentSection()
	if err != nil {
		return nil, err
	}
	ret, err := obj.parseTypeExpression()
	if err != nil {
		return nil, err
	}
	return &FunctionType{
		nodeInfo:     obj.info(keyword.originalIndex, ret.EndTokenIndex()),
		KeywordToken: keyword.originalIndex,
		OpenParen:    openParen,
		CloseParen:   closeParen,
		Arguments:    args,
		Return:       ret,
	}, nil
}

func (obj *parser) parseLoopHeader() (string, string, int, int, Expression, *Block, error) {
	firstName, _, _, err := obj.parseVariableIdentifier()
	if err != nil {
		return "", "", 0, 0, nil, nil, err
	}
	commaTok, err := obj.expect(langtoken.KindComma, "\",\"")
	if err != nil {
		return "", "", 0, 0, nil, nil, err
	}
	secondName, _, _, err := obj.parseVariableIdentifier()
	if err != nil {
		return "", "", 0, 0, nil, nil, err
	}
	inTok, err := obj.expect(langtoken.KindIn, "\"in\"")
	if err != nil {
		return "", "", 0, 0, nil, nil, err
	}
	iterable, err := obj.parseExpression()
	if err != nil {
		return "", "", 0, 0, nil, nil, err
	}
	body, err := obj.parseBlock()
	if err != nil {
		return "", "", 0, 0, nil, nil, err
	}
	return firstName, secondName, commaTok.originalIndex, inTok.originalIndex, iterable, body, nil
}

func (obj *parser) parseFunctionTail() (int, int, []*Parameter, TypeExpression, *ExpressionBlock, error) {
	openParen, closeParen, parameters, err := obj.parseRequiredParameterSection()
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	var returnType TypeExpression
	if !obj.at(langtoken.KindOpenCurly) {
		returnType, err = obj.parseTypeExpression()
		if err != nil {
			return 0, 0, nil, nil, nil, err
		}
	}

	body, err := obj.parseExpressionBlock()
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}
	return openParen, closeParen, parameters, returnType, body, nil
}

func (obj *parser) parseRequiredParameterSection() (int, int, []*Parameter, error) {
	openParenTok, err := obj.expect(langtoken.KindOpenParen, "\"(\"")
	if err != nil {
		return 0, 0, nil, err
	}
	parameters, err := obj.parseParameterList()
	if err != nil {
		return 0, 0, nil, err
	}
	closeParenTok, err := obj.expect(langtoken.KindCloseParen, "\")\"")
	if err != nil {
		return 0, 0, nil, err
	}
	return openParenTok.originalIndex, closeParenTok.originalIndex, parameters, nil
}

func (obj *parser) parseOptionalParameterSection() (int, int, []*Parameter, error) {
	openParen := -1
	closeParen := -1
	var parameters []*Parameter
	if !obj.at(langtoken.KindOpenParen) {
		return openParen, closeParen, parameters, nil
	}

	openParen, closeParen, parameters, err := obj.parseRequiredParameterSection()
	if err != nil {
		return 0, 0, nil, err
	}
	return openParen, closeParen, parameters, nil
}

func (obj *parser) parseRequiredCallSection() (int, int, []*CallArgument, error) {
	openParenTok, err := obj.expect(langtoken.KindOpenParen, "\"(\"")
	if err != nil {
		return 0, 0, nil, err
	}
	args, err := obj.parseCallArguments()
	if err != nil {
		return 0, 0, nil, err
	}
	closeParenTok, err := obj.expect(langtoken.KindCloseParen, "\")\"")
	if err != nil {
		return 0, 0, nil, err
	}
	return openParenTok.originalIndex, closeParenTok.originalIndex, args, nil
}

func (obj *parser) parseOptionalCallSection() (int, int, []*CallArgument, error) {
	openParen := -1
	closeParen := -1
	var args []*CallArgument
	if !obj.at(langtoken.KindOpenParen) {
		return openParen, closeParen, args, nil
	}

	openParen, closeParen, args, err := obj.parseRequiredCallSection()
	if err != nil {
		return 0, 0, nil, err
	}
	return openParen, closeParen, args, nil
}

func (obj *parser) parseRequiredFunctionTypeArgumentSection() (int, int, []*FunctionTypeArgument, error) {
	openParenTok, err := obj.expect(langtoken.KindOpenParen, "\"(\"")
	if err != nil {
		return 0, 0, nil, err
	}
	args, err := obj.parseFunctionTypeArguments()
	if err != nil {
		return 0, 0, nil, err
	}
	closeParenTok, err := obj.expect(langtoken.KindCloseParen, "\")\"")
	if err != nil {
		return 0, 0, nil, err
	}
	return openParenTok.originalIndex, closeParenTok.originalIndex, args, nil
}

func (obj *parser) parseParameterList() ([]*Parameter, error) {
	var parameters []*Parameter
	if obj.at(langtoken.KindCloseParen) {
		return parameters, nil
	}

	for {
		name, start, _, err := obj.parseVariableIdentifier()
		if err != nil {
			return nil, err
		}

		var typ TypeExpression
		if !obj.at(langtoken.KindComma) && !obj.at(langtoken.KindCloseParen) {
			typ, err = obj.parseTypeExpression()
			if err != nil {
				return nil, err
			}
		}
		end := obj.previousOriginalIndex()
		consumedComma := false
		if obj.at(langtoken.KindComma) {
			comma := obj.consume()
			end = comma.originalIndex
			consumedComma = true
			if obj.at(langtoken.KindCloseParen) {
				return nil, obj.errorCurrent("parameter")
			}
		}

		parameters = append(parameters, &Parameter{
			nodeInfo: obj.info(start, end),
			Name:     name,
			Type:     typ,
		})
		if !consumedComma {
			return parameters, nil
		}
	}
}

func (obj *parser) parseCallArguments() ([]*CallArgument, error) {
	var args []*CallArgument
	if obj.at(langtoken.KindCloseParen) {
		return args, nil
	}

	for {
		value, err := obj.parseExpression()
		if err != nil {
			return nil, err
		}
		end := value.EndTokenIndex()
		consumedComma := false
		if obj.at(langtoken.KindComma) {
			comma := obj.consume()
			end = comma.originalIndex
			consumedComma = true
			if obj.at(langtoken.KindCloseParen) {
				return nil, obj.errorCurrent("call argument")
			}
		}
		args = append(args, &CallArgument{
			nodeInfo: obj.info(value.StartTokenIndex(), end),
			Value:    value,
		})
		if !consumedComma {
			return args, nil
		}
	}
}

func (obj *parser) parseFunctionTypeArguments() ([]*FunctionTypeArgument, error) {
	var args []*FunctionTypeArgument
	if obj.at(langtoken.KindCloseParen) {
		return args, nil
	}

	for {
		start := obj.current().originalIndex
		name := ""
		hasName := false
		if obj.at(langtoken.KindDollar) {
			var err error
			name, start, _, err = obj.parseVariableIdentifier()
			if err != nil {
				return nil, err
			}
			hasName = true
		}

		typ, err := obj.parseTypeExpression()
		if err != nil {
			return nil, err
		}
		end := typ.EndTokenIndex()
		consumedComma := false
		if obj.at(langtoken.KindComma) {
			comma := obj.consume()
			end = comma.originalIndex
			consumedComma = true
			if obj.at(langtoken.KindCloseParen) {
				return nil, obj.errorCurrent("function type argument")
			}
		}
		args = append(args, &FunctionTypeArgument{
			nodeInfo: obj.info(start, end),
			Name:     name,
			HasName:  hasName,
			Type:     typ,
		})
		if !consumedComma {
			return args, nil
		}
	}
}

func (obj *parser) parseVariableIdentifier() (string, int, int, error) {
	dollar, err := obj.expect(langtoken.KindDollar, "\"$\"")
	if err != nil {
		return "", 0, 0, err
	}
	name, end, err := obj.parseUndottedIdentifier()
	if err != nil {
		return "", 0, 0, err
	}
	return name, dollar.originalIndex, end, nil
}

func (obj *parser) parseDottedVariableIdentifier() ([]string, int, int, error) {
	dollar, err := obj.expect(langtoken.KindDollar, "\"$\"")
	if err != nil {
		return nil, 0, 0, err
	}
	parts, _, end, err := obj.parseDottedIdentifier()
	if err != nil {
		return nil, 0, 0, err
	}
	return parts, dollar.originalIndex, end, nil
}

func (obj *parser) parseUndottedIdentifier() (string, int, error) {
	tok := obj.current()
	if !isUndottedIdentifierKind(tok.token.Kind) {
		return "", 0, obj.errorCurrent("identifier")
	}
	obj.consume()
	return tok.token.Raw, tok.originalIndex, nil
}

func (obj *parser) parseDottedIdentifier() ([]string, int, int, error) {
	first, start, err := obj.parseUndottedIdentifier()
	if err != nil {
		return nil, 0, 0, err
	}
	parts := []string{first}
	end := start
	for obj.at(langtoken.KindDot) {
		obj.consume()
		next, nextEnd, err := obj.parseUndottedIdentifier()
		if err != nil {
			return nil, 0, 0, err
		}
		parts = append(parts, next)
		end = nextEnd
	}
	return parts, start, end, nil
}

func (obj *parser) parseColonIdentifier() ([]string, int, int, error) {
	return obj.parseColonSeparatedIdentifier(langtoken.KindIdentifier, "identifier")
}

func (obj *parser) parseCapitalizedResourceIdentifier() ([]string, int, int, error) {
	return obj.parseColonSeparatedIdentifier(langtoken.KindCapitalizedIdentifier, "capitalized identifier")
}

func (obj *parser) parseColonSeparatedIdentifier(kind langtoken.Kind, want string) ([]string, int, int, error) {
	tok, err := obj.expect(kind, want)
	if err != nil {
		return nil, 0, 0, err
	}
	parts := []string{tok.token.Raw}
	start := tok.originalIndex
	end := tok.originalIndex
	for obj.at(langtoken.KindColon) {
		obj.consume()
		next, err := obj.expect(kind, want)
		if err != nil {
			return nil, 0, 0, err
		}
		parts = append(parts, next.token.Raw)
		end = next.originalIndex
	}
	return parts, start, end, nil
}

func (obj *parser) current() significantToken {
	if obj.pos >= len(obj.items) {
		if len(obj.items) == 0 {
			return significantToken{}
		}
		return obj.items[len(obj.items)-1]
	}
	return obj.items[obj.pos]
}

func (obj *parser) at(kind langtoken.Kind) bool {
	return obj.current().token.Kind == kind
}

func (obj *parser) atAny(kinds ...langtoken.Kind) bool {
	for _, kind := range kinds {
		if obj.at(kind) {
			return true
		}
	}
	return false
}

func (obj *parser) consume() significantToken {
	tok := obj.current()
	if obj.pos < len(obj.items) {
		obj.pos++
	}
	return tok
}

func (obj *parser) expect(kind langtoken.Kind, want string) (significantToken, error) {
	if obj.at(kind) {
		return obj.consume(), nil
	}
	return significantToken{}, obj.errorCurrent(want)
}

func (obj *parser) previousOriginalIndex() int {
	if obj.pos == 0 {
		return obj.current().originalIndex
	}
	return obj.items[obj.pos-1].originalIndex
}

func (obj *parser) parseOptionalDefaultValue(end int) (int, Expression, int, error) {
	defaultTok := -1
	var defaultValue Expression
	if !obj.at(langtoken.KindDefault) {
		return defaultTok, defaultValue, end, nil
	}

	op := obj.consume()
	defaultTok = op.originalIndex
	value, err := obj.parseExpression()
	if err != nil {
		return 0, nil, 0, err
	}
	return defaultTok, value, value.EndTokenIndex(), nil
}

func (obj *parser) binaryExpression(left Expression, op significantToken, right Expression) Expression {
	return &BinaryExpression{
		nodeInfo:      obj.info(left.StartTokenIndex(), right.EndTokenIndex()),
		Left:          left,
		OperatorToken: op.originalIndex,
		OperatorRaw:   op.token.Raw,
		Right:         right,
	}
}

// parseBinaryLeftAssociative parses next (op next)* and folds the results from
// left to right. It is used for precedence levels where repeated chaining is
// valid, such as +, -, *, /, and/or.
func (obj *parser) parseBinaryLeftAssociative(next func() (Expression, error), kinds ...langtoken.Kind) (Expression, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for obj.atAny(kinds...) {
		op := obj.consume()
		right, err := next()
		if err != nil {
			return nil, err
		}
		left = obj.binaryExpression(left, op, right)
	}
	return left, nil
}

// parseBinaryOptional parses next [op next] for precedence levels where at
// most one operator is accepted at this stage, such as comparisons and in.
func (obj *parser) parseBinaryOptional(next func() (Expression, error), kinds ...langtoken.Kind) (Expression, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	if !obj.atAny(kinds...) {
		return left, nil
	}
	op := obj.consume()
	right, err := next()
	if err != nil {
		return nil, err
	}
	return obj.binaryExpression(left, op, right), nil
}

func (obj *parser) info(start, end int) nodeInfo {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if len(obj.all) == 0 {
		return nodeInfo{
			span:            source.NewSpan(obj.file, 0, 0),
			startTokenIndex: 0,
			endTokenIndex:   0,
		}
	}
	if start >= len(obj.all) {
		start = len(obj.all) - 1
	}
	if end >= len(obj.all) {
		end = len(obj.all) - 1
	}
	return nodeInfo{
		span:            source.NewSpan(obj.file, obj.all[start].Span.Start, obj.all[end].Span.End),
		startTokenIndex: start,
		endTokenIndex:   end,
	}
}

func (obj *parser) errorCurrent(want string) error {
	return obj.errorAt(obj.current().token.Span, fmt.Sprintf("expected %s, found %s", want, tokenDescription(obj.current().token)))
}

func (obj *parser) errorAt(span source.Span, message string) error {
	return &parseError{
		diagnostic: diag.NewError(span, message),
	}
}

func tokenDescription(tok langtoken.Token) string {
	switch tok.Kind {
	case langtoken.KindEOF:
		return "end of file"
	case langtoken.KindIdentifier,
		langtoken.KindCapitalizedIdentifier,
		langtoken.KindBoolIdentifier,
		langtoken.KindStrIdentifier,
		langtoken.KindIntIdentifier,
		langtoken.KindFloatIdentifier,
		langtoken.KindMapIdentifier,
		langtoken.KindStructIdentifier,
		langtoken.KindVariantIdentifier,
		langtoken.KindFuncIdentifier,
		langtoken.KindClassIdentifier,
		langtoken.KindIncludeIdentifier,
		langtoken.KindImportIdentifier,
		langtoken.KindAsIdentifier,
		langtoken.KindCollectIdentifier,
		langtoken.KindPanicIdentifier,
		langtoken.KindIf,
		langtoken.KindElse,
		langtoken.KindFor,
		langtoken.KindForkv,
		langtoken.KindIn,
		langtoken.KindAnd,
		langtoken.KindOr,
		langtoken.KindNot:
		return fmt.Sprintf("%q", tok.Raw)
	default:
		if tok.Raw != "" {
			return fmt.Sprintf("%q", tok.Raw)
		}
		return tok.Kind.String()
	}
}

func isUndottedIdentifierKind(kind langtoken.Kind) bool {
	switch kind {
	case langtoken.KindIdentifier, langtoken.KindMapIdentifier, langtoken.KindCollectIdentifier:
		return true
	default:
		return false
	}
}
