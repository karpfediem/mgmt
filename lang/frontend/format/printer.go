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

package format

import (
	"strings"

	"github.com/purpleidea/mgmt/lang/frontend/syntax"
	langtoken "github.com/purpleidea/mgmt/lang/frontend/token"
)

const (
	precedenceLogical  = 10
	precedenceCompare  = 20
	precedenceAdditive = 30
	precedenceMultiply = 40
	precedenceUnary    = 50
	precedencePostfix  = 60
	precedencePrimary  = 70
)

type gapComment struct {
	raw         string
	blankBefore bool
}

type gapInfo struct {
	trailing        string
	trailingPrefix  string
	leading         []gapComment
	blankBeforeNext bool
}

func (obj gapInfo) hasComments() bool {
	return obj.trailing != "" || len(obj.leading) > 0
}

func (obj gapInfo) trailingAsLeading() gapInfo {
	if obj.trailing == "" {
		return obj
	}
	obj.leading = append([]gapComment{{
		raw:         obj.trailing,
		blankBefore: false,
	}}, obj.leading...)
	obj.trailing = ""
	obj.trailingPrefix = ""
	return obj
}

type printer struct {
	doc         *syntax.Document
	builder     strings.Builder
	indent      int
	startOfLine bool
}

type tokenRange interface {
	StartTokenIndex() int
	EndTokenIndex() int
}

func newPrinter(doc *syntax.Document) *printer {
	return &printer{
		doc:         doc,
		startOfLine: true,
	}
}

func hasCommentsInDelimitedSequence[T tokenRange](obj *printer, items []T, open, close int) bool {
	if len(items) == 0 {
		return obj.hasCommentsBetween(open, close)
	}
	if obj.hasCommentsBetween(open, items[0].StartTokenIndex()) {
		return true
	}
	for i, item := range items {
		next := close
		if i+1 < len(items) {
			next = items[i+1].StartTokenIndex()
		}
		if obj.hasCommentsBetween(item.EndTokenIndex(), next) {
			return true
		}
	}
	return false
}

func writeInlineDelimitedSequence[T any](obj *printer, openText, closeText, separator, trailingSeparator string, items []T, writeItem func(T)) {
	obj.writeString(openText)
	for i, item := range items {
		if i > 0 {
			obj.writeString(separator)
		}
		writeItem(item)
	}
	if len(items) > 0 && trailingSeparator != "" {
		obj.writeString(trailingSeparator)
	}
	obj.writeString(closeText)
}

func writeMultilineDelimitedSequence[T tokenRange](obj *printer, openText, closeText string, open, close int, items []T, separator string, trailingSeparator bool, writeItem func(T)) {
	obj.writeString(openText)
	obj.indent++
	if len(items) == 0 {
		obj.writeGapWithInfo(obj.gap(open, close).trailingAsLeading(), obj.indent, true, true)
	} else {
		obj.writeGapWithInfo(obj.gap(open, items[0].StartTokenIndex()).trailingAsLeading(), obj.indent, true, true)
		for i, item := range items {
			writeItem(item)
			if trailingSeparator || i+1 < len(items) {
				obj.writeString(separator)
			}
			nextBoundary := close
			if i+1 < len(items) {
				nextBoundary = items[i+1].StartTokenIndex()
			}
			obj.writeGap(item.EndTokenIndex(), nextBoundary, obj.indent, true, true)
		}
	}
	obj.indent--
	obj.writeString(closeText)
}

func (obj *printer) String() string {
	return obj.builder.String()
}

func (obj *printer) writeFileNode(root *syntax.FileNode) {
	obj.writeStatementSequence(root.Statements, -1, root.EOFToken, 0, false, false)
}

func (obj *printer) writeStatementSequence(stmts []syntax.Statement, after, before, indent int, startNewline, endNewline bool) {
	saved := obj.indent
	obj.indent = indent
	defer func() {
		obj.indent = saved
	}()

	if len(stmts) == 0 {
		obj.writeGap(after, before, indent, startNewline, true)
		return
	}

	obj.writeGap(after, stmts[0].StartTokenIndex(), indent, startNewline, true)
	for i, stmt := range stmts {
		obj.indent = indent
		obj.writeStatement(stmt)

		nextBoundary := before
		requireLine := endNewline
		if i+1 < len(stmts) {
			nextBoundary = stmts[i+1].StartTokenIndex()
			requireLine = true
		}
		obj.writeGap(stmt.EndTokenIndex(), nextBoundary, indent, requireLine, true)
	}
}

func (obj *printer) writeResourceEntrySequence(entries []syntax.ResourceEntry, open, close, indent int) {
	if len(entries) == 0 {
		obj.writeGap(open, close, indent, true, true)
		return
	}

	obj.writeGap(open, entries[0].StartTokenIndex(), indent, true, true)
	for i, entry := range entries {
		obj.indent = indent
		obj.writeResourceEntry(entry)

		nextBoundary := close
		if i+1 < len(entries) {
			nextBoundary = entries[i+1].StartTokenIndex()
		}
		obj.writeGap(entry.EndTokenIndex(), nextBoundary, indent, true, true)
	}
}

func (obj *printer) writeBlock(block *syntax.Block) {
	if len(block.Statements) == 0 && !obj.hasCommentsBetween(block.OpenBrace, block.CloseBrace) {
		obj.writeString("{}")
		return
	}
	obj.writeString("{")
	obj.indent++
	obj.writeStatementSequence(block.Statements, block.OpenBrace, block.CloseBrace, obj.indent, true, true)
	obj.indent--
	obj.writeString("}")
}

func (obj *printer) writeExpressionBlock(block *syntax.ExpressionBlock) {
	obj.writeExpressionBlockWithMode(block, obj.canInlineExpressionBlock(block))
}

func (obj *printer) writeExpressionBlockWithMode(block *syntax.ExpressionBlock, inline bool) {
	if inline {
		obj.writeString("{ ")
		obj.writeExpression(block.Value, 0)
		obj.writeString(" }")
		return
	}

	obj.writeString("{")
	obj.indent++
	obj.writeGap(block.OpenBrace, block.Value.StartTokenIndex(), obj.indent, true, true)
	obj.writeExpression(block.Value, 0)
	obj.writeGap(block.Value.EndTokenIndex(), block.CloseBrace, obj.indent, true, true)
	obj.indent--
	obj.writeString("}")
}

func (obj *printer) writeStatement(stmt syntax.Statement) {
	switch node := stmt.(type) {
	case *syntax.BindStatement:
		obj.writeString("$" + node.Name)
		if node.Type != nil {
			obj.writeString(" ")
			obj.writeTypeExpression(node.Type)
		}
		obj.writeString(" = ")
		obj.writeExpression(node.Value, 0)
	case *syntax.PanicStatement:
		obj.writeString("panic")
		obj.writeCallArguments(node.Args, node.OpenParen, node.CloseParen)
	case *syntax.CollectStatement:
		obj.writeString("collect ")
		obj.writeResourceStatement(node.Resource)
	case *syntax.IfStatement:
		obj.writeString("if ")
		obj.writeExpression(node.Condition, 0)
		obj.writeString(" ")
		obj.writeBlock(node.Then)
		if node.Else != nil {
			obj.writeString(" else ")
			obj.writeBlock(node.Else)
		}
	case *syntax.ForStatement:
		obj.writeString("for ")
		obj.writeString("$" + node.IndexName)
		obj.writeString(", ")
		obj.writeString("$" + node.ValueName)
		obj.writeString(" in ")
		obj.writeExpression(node.Iterable, 0)
		obj.writeString(" ")
		obj.writeBlock(node.Body)
	case *syntax.ForKVStatement:
		obj.writeString("forkv ")
		obj.writeString("$" + node.KeyName)
		obj.writeString(", ")
		obj.writeString("$" + node.ValueName)
		obj.writeString(" in ")
		obj.writeExpression(node.Iterable, 0)
		obj.writeString(" ")
		obj.writeBlock(node.Body)
	case *syntax.FunctionStatement:
		obj.writeString("func ")
		obj.writeString(node.Name)
		obj.writeParameters(node.Parameters, node.OpenParen, node.CloseParen)
		if node.ReturnType != nil {
			obj.writeString(" ")
			obj.writeTypeExpression(node.ReturnType)
		}
		obj.writeString(" ")
		obj.writeExpressionBlock(node.Body)
	case *syntax.ClassStatement:
		obj.writeString("class ")
		obj.writeString(strings.Join(node.NameParts, ":"))
		if node.OpenParen >= 0 {
			obj.writeParameters(node.Parameters, node.OpenParen, node.CloseParen)
		}
		obj.writeString(" ")
		obj.writeBlock(node.Body)
	case *syntax.IncludeStatement:
		obj.writeString("include ")
		obj.writeString(strings.Join(node.NameParts, "."))
		if node.OpenParen >= 0 {
			obj.writeCallArguments(node.Arguments, node.OpenParen, node.CloseParen)
		}
		if node.Alias != "" {
			obj.writeString(" as ")
			obj.writeString(node.Alias)
		}
	case *syntax.ImportStatement:
		obj.writeString("import ")
		obj.writeString(node.PathRaw)
		if node.Alias != "" {
			obj.writeString(" as ")
			obj.writeString(node.Alias)
		}
	case *syntax.ResourceStatement:
		obj.writeResourceStatement(node)
	case *syntax.EdgeStatement:
		obj.writeEdgeStatement(node)
	}
}

func (obj *printer) writeResourceStatement(node *syntax.ResourceStatement) {
	obj.writeString(strings.Join(node.KindParts, ":"))
	obj.writeString(" ")
	obj.writeExpression(node.Name, 0)
	obj.writeString(" ")
	if len(node.Entries) == 0 && !obj.hasCommentsBetween(node.OpenBrace, node.CloseBrace) {
		obj.writeString("{}")
		return
	}
	obj.writeString("{")
	obj.indent++
	obj.writeResourceEntrySequence(node.Entries, node.OpenBrace, node.CloseBrace, obj.indent)
	obj.indent--
	obj.writeString("}")
}

func (obj *printer) writeEdgeStatement(node *syntax.EdgeStatement) {
	for i, item := range node.Items {
		if i > 0 {
			obj.writeGap(node.Items[i-1].EndTokenIndex(), item.StartTokenIndex(), obj.indent, false, true)
		}
		obj.writeEdgeHalf(item.Half)
		if i+1 < len(node.Items) {
			obj.writeString(" ->")
			if !obj.hasCommentsBetween(item.EndTokenIndex(), node.Items[i+1].StartTokenIndex()) {
				obj.writeString(" ")
			}
		}
	}
}

func (obj *printer) writeResourceEntry(entry syntax.ResourceEntry) {
	switch node := entry.(type) {
	case *syntax.ResourceFieldEntry:
		obj.writeString(node.Name)
		obj.writeString(" => ")
		obj.writeExpression(node.Value, 0)
		if node.Alternative != nil {
			obj.writeString(" ?: ")
			obj.writeExpression(node.Alternative, 0)
		}
		obj.writeString(",")
	case *syntax.ResourceEdgeEntry:
		obj.writeString(node.Name)
		obj.writeString(" => ")
		if node.Condition != nil {
			obj.writeExpression(node.Condition, 0)
			obj.writeString(" ?: ")
		}
		obj.writeEdgeHalf(node.Target)
		obj.writeString(",")
	case *syntax.ResourceMetaEntry:
		obj.writeString(node.HeadRaw)
		if node.Name != "" {
			obj.writeString(":")
			obj.writeString(node.Name)
		}
		obj.writeString(" => ")
		obj.writeExpression(node.Value, 0)
		if node.Alternative != nil {
			obj.writeString(" ?: ")
			obj.writeExpression(node.Alternative, 0)
		}
		obj.writeString(",")
	}
}

func (obj *printer) writeExpression(expr syntax.Expression, parentPrecedence int) {
	if paren, ok := expr.(*syntax.ParenExpression); ok {
		obj.writeString("(")
		obj.writeExpression(paren.Inner, 0)
		obj.writeString(")")
		return
	}

	precedence := expressionPrecedence(expr)
	needParens := precedence < parentPrecedence
	if needParens {
		obj.writeString("(")
	}

	switch node := expr.(type) {
	case *syntax.BoolLiteral:
		obj.writeString(node.Raw)
	case *syntax.StringLiteral:
		obj.writeString(node.Raw)
	case *syntax.IntegerLiteral:
		obj.writeString(node.Raw)
	case *syntax.FloatLiteral:
		obj.writeString(node.Raw)
	case *syntax.ListLiteral:
		obj.writeListLiteral(node)
	case *syntax.MapLiteral:
		obj.writeMapLiteral(node)
	case *syntax.StructLiteral:
		obj.writeStructLiteral(node)
	case *syntax.NamedCallExpression:
		obj.writeString(strings.Join(node.NameParts, "."))
		obj.writeCallArguments(node.Arguments, node.OpenParen, node.CloseParen)
	case *syntax.VariableCallExpression:
		obj.writeString("$" + strings.Join(node.NameParts, "."))
		obj.writeCallArguments(node.Arguments, node.OpenParen, node.CloseParen)
	case *syntax.AnonymousCallExpression:
		obj.writeExpression(node.Callee, precedencePrimary)
		obj.writeCallArguments(node.Arguments, node.OpenParen, node.CloseParen)
	case *syntax.VariableExpression:
		obj.writeString("$" + strings.Join(node.NameParts, "."))
	case *syntax.FunctionExpression:
		obj.writeString("func")
		obj.writeParameters(node.Parameters, node.OpenParen, node.CloseParen)
		if node.ReturnType != nil {
			obj.writeString(" ")
			obj.writeTypeExpression(node.ReturnType)
		}
		obj.writeString(" ")
		obj.writeExpressionBlock(node.Body)
	case *syntax.IfExpression:
		obj.writeIfExpression(node)
	case *syntax.UnaryExpression:
		obj.writeString(node.OperatorRaw)
		obj.writeString(" ")
		obj.writeExpression(node.Operand, precedenceUnary)
	case *syntax.BinaryExpression:
		obj.writeExpression(node.Left, precedence)
		obj.writeString(" ")
		obj.writeString(node.OperatorRaw)
		obj.writeString(" ")
		obj.writeExpression(node.Right, precedence)
	case *syntax.IndexExpression:
		obj.writeExpression(node.Target, precedencePostfix)
		obj.writeString("[")
		obj.writeExpression(node.Index, 0)
		obj.writeString("]")
		if node.DefaultValue != nil {
			obj.writeString(" || ")
			obj.writeExpression(node.DefaultValue, 0)
		}
	case *syntax.FieldExpression:
		obj.writeExpression(node.Target, precedencePostfix)
		obj.writeString("->")
		obj.writeString(node.Field)
		if node.DefaultValue != nil {
			obj.writeString(" || ")
			obj.writeExpression(node.DefaultValue, 0)
		}
	}

	if needParens {
		obj.writeString(")")
	}
}

func (obj *printer) writeTypeExpression(typ syntax.TypeExpression) {
	switch node := typ.(type) {
	case *syntax.PrimitiveType:
		obj.writeString(node.Name)
	case *syntax.ListType:
		obj.writeString("[]")
		obj.writeTypeExpression(node.Element)
	case *syntax.MapType:
		obj.writeString("map{")
		obj.writeTypeExpression(node.Key)
		obj.writeString(": ")
		obj.writeTypeExpression(node.Value)
		obj.writeString("}")
	case *syntax.StructType:
		obj.writeStructType(node)
	case *syntax.FunctionType:
		obj.writeString("func")
		obj.writeFunctionTypeArguments(node.Arguments, node.OpenParen, node.CloseParen)
		obj.writeString(" ")
		obj.writeTypeExpression(node.Return)
	}
}

func (obj *printer) writeStructType(node *syntax.StructType) {
	if !obj.shouldMultilineStructType(node) {
		obj.writeString("struct{")
		for i, field := range node.Fields {
			if i > 0 {
				obj.writeString("; ")
			}
			obj.writeString(field.Name)
			obj.writeString(" ")
			obj.writeTypeExpression(field.Type)
		}
		obj.writeString("}")
		return
	}
	writeMultilineDelimitedSequence(obj, "struct{", "}", node.OpenBrace, node.CloseBrace, node.Fields, ";", false, func(field *syntax.StructTypeField) {
		obj.writeString(field.Name)
		obj.writeString(" ")
		obj.writeTypeExpression(field.Type)
	})
}

func (obj *printer) writeCallArguments(args []*syntax.CallArgument, open, close int) {
	if !hasCommentsInDelimitedSequence(obj, args, open, close) {
		writeInlineDelimitedSequence(obj, "(", ")", ", ", "", args, func(arg *syntax.CallArgument) {
			obj.writeExpression(arg.Value, 0)
		})
		return
	}
	writeMultilineDelimitedSequence(obj, "(", ")", open, close, args, ",", false, func(arg *syntax.CallArgument) {
		obj.writeExpression(arg.Value, 0)
	})
}

func (obj *printer) writeParameters(params []*syntax.Parameter, open, close int) {
	if !hasCommentsInDelimitedSequence(obj, params, open, close) {
		writeInlineDelimitedSequence(obj, "(", ")", ", ", "", params, func(param *syntax.Parameter) {
			obj.writeString("$" + param.Name)
			if param.Type != nil {
				obj.writeString(" ")
				obj.writeTypeExpression(param.Type)
			}
		})
		return
	}
	writeMultilineDelimitedSequence(obj, "(", ")", open, close, params, ",", false, func(param *syntax.Parameter) {
		obj.writeString("$" + param.Name)
		if param.Type != nil {
			obj.writeString(" ")
			obj.writeTypeExpression(param.Type)
		}
	})
}

func (obj *printer) writeFunctionTypeArguments(args []*syntax.FunctionTypeArgument, open, close int) {
	if !hasCommentsInDelimitedSequence(obj, args, open, close) {
		writeInlineDelimitedSequence(obj, "(", ")", ", ", "", args, func(arg *syntax.FunctionTypeArgument) {
			if arg.HasName {
				obj.writeString("$" + arg.Name + " ")
			}
			obj.writeTypeExpression(arg.Type)
		})
		return
	}
	writeMultilineDelimitedSequence(obj, "(", ")", open, close, args, ",", false, func(arg *syntax.FunctionTypeArgument) {
		if arg.HasName {
			obj.writeString("$" + arg.Name + " ")
		}
		obj.writeTypeExpression(arg.Type)
	})
}

func (obj *printer) writeListLiteral(node *syntax.ListLiteral) {
	if !collectionUsesMultilineLayout(obj, node.Elements, node.OpenBracket, node.CloseBracket) {
		writeInlineDelimitedSequence(obj, "[", "]", ", ", ",", node.Elements, func(element *syntax.ListElement) {
			obj.writeExpression(element.Value, 0)
		})
		return
	}
	writeMultilineDelimitedSequence(obj, "[", "]", node.OpenBracket, node.CloseBracket, node.Elements, ",", true, func(element *syntax.ListElement) {
		obj.writeExpression(element.Value, 0)
	})
}

func (obj *printer) writeMapLiteral(node *syntax.MapLiteral) {
	if !collectionUsesMultilineLayout(obj, node.Entries, node.OpenBrace, node.CloseBrace) {
		writeInlineDelimitedSequence(obj, "{", "}", ", ", ",", node.Entries, func(entry *syntax.MapEntry) {
			obj.writeExpression(entry.Key, 0)
			obj.writeString(" => ")
			obj.writeExpression(entry.Value, 0)
		})
		return
	}
	writeMultilineDelimitedSequence(obj, "{", "}", node.OpenBrace, node.CloseBrace, node.Entries, ",", true, func(entry *syntax.MapEntry) {
		obj.writeExpression(entry.Key, 0)
		obj.writeString(" => ")
		obj.writeExpression(entry.Value, 0)
	})
}

func (obj *printer) writeStructLiteral(node *syntax.StructLiteral) {
	if !collectionUsesMultilineLayout(obj, node.Fields, node.OpenBrace, node.CloseBrace) {
		writeInlineDelimitedSequence(obj, "struct{", "}", ", ", ",", node.Fields, func(field *syntax.StructLiteralField) {
			obj.writeString(field.Name)
			obj.writeString(" => ")
			obj.writeExpression(field.Value, 0)
		})
		return
	}
	writeMultilineDelimitedSequence(obj, "struct{", "}", node.OpenBrace, node.CloseBrace, node.Fields, ",", true, func(field *syntax.StructLiteralField) {
		obj.writeString(field.Name)
		obj.writeString(" => ")
		obj.writeExpression(field.Value, 0)
	})
}

func (obj *printer) writeEdgeHalf(node *syntax.EdgeHalf) {
	obj.writeString(strings.Join(node.KindParts, ":"))
	obj.writeString("[")
	obj.writeExpression(node.Name, 0)
	obj.writeString("]")
	if node.Field != "" {
		obj.writeString(".")
		obj.writeString(node.Field)
	}
}

func (obj *printer) writeGap(after, before, indent int, requireLine, preserveBlank bool) {
	obj.writeGapWithInfo(obj.gap(after, before), indent, requireLine, preserveBlank)
}

func (obj *printer) writeGapWithInfo(info gapInfo, indent int, requireLine, preserveBlank bool) {
	if info.trailing != "" {
		if obj.startOfLine {
			saved := obj.indent
			obj.indent = indent
			obj.writeString(info.trailing)
			obj.indent = saved
		} else {
			_ = info.trailingPrefix
			obj.writeString(" ")
			obj.writeString(info.trailing)
		}
		obj.writeNewline()
	}

	for _, comment := range info.leading {
		if !obj.startOfLine {
			obj.writeNewline()
		}
		if preserveBlank && comment.blankBefore {
			obj.writeNewline()
		}
		saved := obj.indent
		obj.indent = indent
		obj.writeString(comment.raw)
		obj.indent = saved
		obj.writeNewline()
	}

	if info.hasComments() {
		if preserveBlank && info.blankBeforeNext {
			obj.writeNewline()
		}
		return
	}

	if requireLine {
		obj.writeNewline()
		if preserveBlank && info.blankBeforeNext {
			obj.writeNewline()
		}
	}
}

func (obj *printer) gap(after, before int) gapInfo {
	start := 0
	if after >= 0 {
		start = after + 1
	}
	if before < start || before > len(obj.doc.Tokens) {
		return gapInfo{}
	}

	info := gapInfo{}
	newlines := 0
	seenNewline := false
	inlineSpacing := ""
	for _, tok := range obj.doc.Tokens[start:before] {
		if tok.Kind == langtoken.KindWhitespace {
			if !seenNewline {
				inlineSpacing += tok.Raw
			}
			continue
		}
		if tok.Kind == langtoken.KindNewline {
			newlines += strings.Count(tok.Raw, "\n")
			seenNewline = true
			inlineSpacing = ""
			continue
		}
		if tok.Kind != langtoken.KindComment {
			continue
		}
		if !seenNewline && info.trailing == "" {
			info.trailing = tok.Raw
			info.trailingPrefix = inlineSpacing
		} else {
			info.leading = append(info.leading, gapComment{
				raw:         tok.Raw,
				blankBefore: newlines >= 2,
			})
		}
		newlines = 0
		seenNewline = true
		inlineSpacing = ""
	}
	info.blankBeforeNext = newlines >= 2
	return info
}

func (obj *printer) hasCommentsBetween(after, before int) bool {
	return obj.gap(after, before).hasComments()
}

func (obj *printer) hasLineBreakBetween(after, before int) bool {
	start := 0
	if after >= 0 {
		start = after + 1
	}
	if before < start || before > len(obj.doc.Tokens) {
		return false
	}

	for _, tok := range obj.doc.Tokens[start:before] {
		if tok.Kind == langtoken.KindNewline {
			return true
		}
	}
	return false
}

func (obj *printer) writeString(s string) {
	if s == "" {
		return
	}
	if obj.startOfLine {
		obj.builder.WriteString(strings.Repeat("\t", obj.indent))
		obj.startOfLine = false
	}
	obj.builder.WriteString(s)
	obj.startOfLine = strings.HasSuffix(s, "\n")
}

func (obj *printer) writeNewline() {
	obj.builder.WriteByte('\n')
	obj.startOfLine = true
}

func (obj *printer) writeIfExpression(node *syntax.IfExpression) {
	inlineThen := obj.canInlineExpressionBlock(node.Then)
	inlineElse := obj.canInlineExpressionBlock(node.Else)
	inlineThen = inlineThen && inlineElse
	inlineElse = inlineThen

	obj.writeString("if ")
	obj.writeExpression(node.Condition, 0)
	obj.writeString(" ")
	obj.writeExpressionBlockWithMode(node.Then, inlineThen)
	obj.writeString(" else ")
	obj.writeExpressionBlockWithMode(node.Else, inlineElse)
}

func (obj *printer) ensureTrailingNewline() {
	if !obj.startOfLine {
		obj.writeNewline()
	}
}

// canInlineExpressionBlock reports whether a single-expression block can be
// collapsed to `{ expr }` under the current syntactic policy.
func (obj *printer) canInlineExpressionBlock(block *syntax.ExpressionBlock) bool {
	if block == nil || block.Value == nil {
		return true
	}
	if obj.hasCommentsBetween(block.OpenBrace, block.Value.StartTokenIndex()) {
		return false
	}
	if obj.hasCommentsBetween(block.Value.EndTokenIndex(), block.CloseBrace) {
		return false
	}
	return obj.isInlineBlockValue(block.Value)
}

func (obj *printer) isInlineBlockValue(expr syntax.Expression) bool {
	switch node := expr.(type) {
	case *syntax.BoolLiteral, *syntax.StringLiteral, *syntax.IntegerLiteral, *syntax.FloatLiteral, *syntax.VariableExpression:
		return true
	case *syntax.ParenExpression:
		return obj.isInlineBlockValue(node.Inner)
	case *syntax.IndexExpression:
		return obj.isInlineBlockValue(node.Target) &&
			obj.isInlineBlockValue(node.Index) &&
			(node.DefaultValue == nil || obj.isInlineBlockValue(node.DefaultValue))
	case *syntax.FieldExpression:
		return obj.isInlineBlockValue(node.Target) &&
			(node.DefaultValue == nil || obj.isInlineBlockValue(node.DefaultValue))
	default:
		return false
	}
}

func collectionUsesMultilineLayout[T tokenRange](obj *printer, items []T, open, close int) bool {
	return hasCommentsInDelimitedSequence(obj, items, open, close) || obj.hasLineBreakBetween(open, close)
}

func isSimpleInlineCollection[T tokenRange](obj *printer, items []T, open, close int, hasComplexItem func() bool) bool {
	if hasCommentsInDelimitedSequence(obj, items, open, close) {
		return false
	}
	if obj.hasLineBreakBetween(open, close) {
		return false
	}
	return !hasComplexItem()
}

func (obj *printer) isSimpleListLiteral(node *syntax.ListLiteral) bool {
	return isSimpleInlineCollection(obj, node.Elements, node.OpenBracket, node.CloseBracket, func() bool {
		for _, element := range node.Elements {
			if !obj.isSimpleExpression(element.Value) {
				return true
			}
		}
		return false
	})
}

func (obj *printer) isSimpleMapLiteral(node *syntax.MapLiteral) bool {
	return isSimpleInlineCollection(obj, node.Entries, node.OpenBrace, node.CloseBrace, func() bool {
		for _, entry := range node.Entries {
			if !obj.isSimpleExpression(entry.Key) || !obj.isSimpleExpression(entry.Value) {
				return true
			}
		}
		return false
	})
}

func (obj *printer) isSimpleStructLiteral(node *syntax.StructLiteral) bool {
	return isSimpleInlineCollection(obj, node.Fields, node.OpenBrace, node.CloseBrace, func() bool {
		for _, field := range node.Fields {
			if !obj.isSimpleExpression(field.Value) {
				return true
			}
		}
		return false
	})
}

func (obj *printer) shouldMultilineStructType(node *syntax.StructType) bool {
	return hasCommentsInDelimitedSequence(obj, node.Fields, node.OpenBrace, node.CloseBrace) || len(node.Fields) > 1
}

func (obj *printer) hasOnlySimpleCallArguments(args []*syntax.CallArgument, open, close int) bool {
	if hasCommentsInDelimitedSequence(obj, args, open, close) {
		return false
	}
	for _, arg := range args {
		if !obj.isSimpleExpression(arg.Value) {
			return false
		}
	}
	return true
}

// isSimpleExpression reports whether expr is syntactically simple enough for
// inline formatting decisions. This is intentionally recursive and syntax-only.
func (obj *printer) isSimpleExpression(expr syntax.Expression) bool {
	switch node := expr.(type) {
	case *syntax.BoolLiteral, *syntax.StringLiteral, *syntax.IntegerLiteral, *syntax.FloatLiteral, *syntax.VariableExpression:
		return true
	case *syntax.NamedCallExpression:
		return obj.hasOnlySimpleCallArguments(node.Arguments, node.OpenParen, node.CloseParen)
	case *syntax.VariableCallExpression:
		return obj.hasOnlySimpleCallArguments(node.Arguments, node.OpenParen, node.CloseParen)
	case *syntax.ParenExpression:
		return obj.isSimpleExpression(node.Inner)
	case *syntax.UnaryExpression:
		return obj.isSimpleExpression(node.Operand)
	case *syntax.BinaryExpression:
		return obj.isSimpleExpression(node.Left) && obj.isSimpleExpression(node.Right)
	case *syntax.IndexExpression:
		return obj.isSimpleExpression(node.Target) && obj.isSimpleExpression(node.Index) && (node.DefaultValue == nil || obj.isSimpleExpression(node.DefaultValue))
	case *syntax.FieldExpression:
		return obj.isSimpleExpression(node.Target) && (node.DefaultValue == nil || obj.isSimpleExpression(node.DefaultValue))
	case *syntax.ListLiteral:
		return obj.isSimpleListLiteral(node)
	case *syntax.MapLiteral:
		return obj.isSimpleMapLiteral(node)
	case *syntax.StructLiteral:
		return obj.isSimpleStructLiteral(node)
	default:
		return false
	}
}

func expressionPrecedence(expr syntax.Expression) int {
	switch node := expr.(type) {
	case *syntax.BinaryExpression:
		switch node.OperatorRaw {
		case "and", "or":
			return precedenceLogical
		case "==", "!=", "<", ">", "<=", ">=", "in":
			return precedenceCompare
		case "+", "-":
			return precedenceAdditive
		case "*", "/":
			return precedenceMultiply
		default:
			return precedenceLogical
		}
	case *syntax.UnaryExpression:
		return precedenceUnary
	case *syntax.IndexExpression, *syntax.FieldExpression:
		return precedencePostfix
	default:
		return precedencePrimary
	}
}
