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

import "github.com/purpleidea/mgmt/lang/frontend/source"

// Node is one syntax-preserving CST node in the frontend parse tree.
type Node interface {
	Span() source.Span
	StartTokenIndex() int
	EndTokenIndex() int
}

// Statement is one top-level or block statement.
type Statement interface {
	Node
	statementNode()
}

// Expression is one CST expression node.
type Expression interface {
	Node
	expressionNode()
}

// ResourceEntry is one entry inside a resource body.
type ResourceEntry interface {
	Node
	resourceEntryNode()
}

// TypeExpression is one CST type expression.
type TypeExpression interface {
	Node
	typeExpressionNode()
}

type nodeInfo struct {
	span            source.Span
	startTokenIndex int
	endTokenIndex   int
}

// Span returns the source span covered by this syntax node.
func (obj nodeInfo) Span() source.Span {
	return obj.span
}

// StartTokenIndex returns the original token index where this node begins.
func (obj nodeInfo) StartTokenIndex() int {
	return obj.startTokenIndex
}

// EndTokenIndex returns the original token index where this node ends.
func (obj nodeInfo) EndTokenIndex() int {
	return obj.endTokenIndex
}

// FileNode is the parsed root node for one source snapshot.
type FileNode struct {
	nodeInfo
	Statements []Statement
	EOFToken   int
}

// Block is a statement block delimited by curly braces.
type Block struct {
	nodeInfo
	OpenBrace  int
	CloseBrace int
	Statements []Statement
}

// ExpressionBlock is a single expression wrapped in curly braces.
type ExpressionBlock struct {
	nodeInfo
	OpenBrace  int
	CloseBrace int
	Value      Expression
}

// Parameter is one function or class parameter.
type Parameter struct {
	nodeInfo
	Name string
	Type TypeExpression
}

// CallArgument is one call argument.
type CallArgument struct {
	nodeInfo
	Value Expression
}

// ListElement is one list element.
type ListElement struct {
	nodeInfo
	Value Expression
}

// MapEntry is one map literal entry.
type MapEntry struct {
	nodeInfo
	Key   Expression
	Value Expression
}

// StructLiteralField is one struct literal field.
type StructLiteralField struct {
	nodeInfo
	Name  string
	Value Expression
}

// StructTypeField is one struct type field.
type StructTypeField struct {
	nodeInfo
	Name string
	Type TypeExpression
}

// FunctionTypeArgument is one function type argument.
type FunctionTypeArgument struct {
	nodeInfo
	Name    string
	HasName bool
	Type    TypeExpression
}

// EdgeHalf is one resource edge endpoint.
type EdgeHalf struct {
	nodeInfo
	KindParts []string
	Name      Expression
	Field     string
}

// EdgeChainItem is one half of an edge statement. Non-final items include the
// following arrow token in their range so trivia ownership stays stable.
type EdgeChainItem struct {
	nodeInfo
	Half *EdgeHalf
}

// BindStatement binds a variable.
type BindStatement struct {
	nodeInfo
	Name  string
	Type  TypeExpression
	Value Expression
}

func (*BindStatement) statementNode() {}

// PanicStatement calls panic(...).
type PanicStatement struct {
	nodeInfo
	KeywordToken int
	OpenParen    int
	CloseParen   int
	Args         []*CallArgument
}

func (*PanicStatement) statementNode() {}

// CollectStatement wraps a resource statement in collect.
type CollectStatement struct {
	nodeInfo
	KeywordToken int
	Resource     *ResourceStatement
}

func (*CollectStatement) statementNode() {}

// IfStatement is an if/else statement.
type IfStatement struct {
	nodeInfo
	IfToken   int
	ElseToken int
	Condition Expression
	Then      *Block
	Else      *Block
}

func (*IfStatement) statementNode() {}

// ForStatement is a for statement.
type ForStatement struct {
	nodeInfo
	KeywordToken int
	CommaToken   int
	InToken      int
	IndexName    string
	ValueName    string
	Iterable     Expression
	Body         *Block
}

func (*ForStatement) statementNode() {}

// ForKVStatement is a forkv statement.
type ForKVStatement struct {
	nodeInfo
	KeywordToken int
	CommaToken   int
	InToken      int
	KeyName      string
	ValueName    string
	Iterable     Expression
	Body         *Block
}

func (*ForKVStatement) statementNode() {}

// FunctionStatement is a named function definition.
type FunctionStatement struct {
	nodeInfo
	KeywordToken int
	Name         string
	OpenParen    int
	CloseParen   int
	Parameters   []*Parameter
	ReturnType   TypeExpression
	Body         *ExpressionBlock
}

func (*FunctionStatement) statementNode() {}

// ClassStatement is a class definition.
type ClassStatement struct {
	nodeInfo
	KeywordToken int
	NameParts    []string
	OpenParen    int
	CloseParen   int
	Parameters   []*Parameter
	Body         *Block
}

func (*ClassStatement) statementNode() {}

// IncludeStatement is an include statement.
type IncludeStatement struct {
	nodeInfo
	KeywordToken int
	NameParts    []string
	OpenParen    int
	CloseParen   int
	Arguments    []*CallArgument
	AsToken      int
	Alias        string
}

func (*IncludeStatement) statementNode() {}

// ImportStatement is an import statement.
type ImportStatement struct {
	nodeInfo
	KeywordToken int
	PathRaw      string
	AsToken      int
	Alias        string
	AliasAll     bool
}

func (*ImportStatement) statementNode() {}

// ResourceStatement is a resource statement.
type ResourceStatement struct {
	nodeInfo
	KindParts  []string
	Name       Expression
	OpenBrace  int
	CloseBrace int
	Entries    []ResourceEntry
}

func (*ResourceStatement) statementNode() {}

// EdgeStatement is a top-level edge statement.
type EdgeStatement struct {
	nodeInfo
	SendRecv bool
	Items    []*EdgeChainItem
}

func (*EdgeStatement) statementNode() {}

// ResourceFieldEntry is one resource field entry.
type ResourceFieldEntry struct {
	nodeInfo
	Name        string
	Value       Expression
	Alternative Expression
}

func (*ResourceFieldEntry) resourceEntryNode() {}

// ResourceEdgeEntry is one resource edge entry.
type ResourceEdgeEntry struct {
	nodeInfo
	Name      string
	Condition Expression
	Target    *EdgeHalf
}

func (*ResourceEdgeEntry) resourceEntryNode() {}

// ResourceMetaEntry is one resource meta entry.
type ResourceMetaEntry struct {
	nodeInfo
	HeadRaw     string
	Name        string
	Value       Expression
	Alternative Expression
}

func (*ResourceMetaEntry) resourceEntryNode() {}

// BoolLiteral is a boolean literal expression.
type BoolLiteral struct {
	nodeInfo
	Raw string
}

func (*BoolLiteral) expressionNode() {}

// StringLiteral is a string literal expression.
type StringLiteral struct {
	nodeInfo
	Raw string
}

func (*StringLiteral) expressionNode() {}

// IntegerLiteral is an integer literal expression.
type IntegerLiteral struct {
	nodeInfo
	Raw string
}

func (*IntegerLiteral) expressionNode() {}

// FloatLiteral is a float literal expression.
type FloatLiteral struct {
	nodeInfo
	Raw string
}

func (*FloatLiteral) expressionNode() {}

// ListLiteral is a list literal expression.
type ListLiteral struct {
	nodeInfo
	OpenBracket  int
	CloseBracket int
	Elements     []*ListElement
}

func (*ListLiteral) expressionNode() {}

// MapLiteral is a map literal expression.
type MapLiteral struct {
	nodeInfo
	OpenBrace  int
	CloseBrace int
	Entries    []*MapEntry
}

func (*MapLiteral) expressionNode() {}

// StructLiteral is a struct literal expression.
type StructLiteral struct {
	nodeInfo
	KeywordToken int
	OpenBrace    int
	CloseBrace   int
	Fields       []*StructLiteralField
}

func (*StructLiteral) expressionNode() {}

// NamedCallExpression calls a named function.
type NamedCallExpression struct {
	nodeInfo
	NameParts  []string
	OpenParen  int
	CloseParen int
	Arguments  []*CallArgument
}

func (*NamedCallExpression) expressionNode() {}

// VariableCallExpression calls a function value stored in a variable.
type VariableCallExpression struct {
	nodeInfo
	NameParts  []string
	OpenParen  int
	CloseParen int
	Arguments  []*CallArgument
}

func (*VariableCallExpression) expressionNode() {}

// AnonymousCallExpression calls an anonymous function expression.
type AnonymousCallExpression struct {
	nodeInfo
	Callee     *FunctionExpression
	OpenParen  int
	CloseParen int
	Arguments  []*CallArgument
}

func (*AnonymousCallExpression) expressionNode() {}

// VariableExpression references a variable.
type VariableExpression struct {
	nodeInfo
	NameParts []string
}

func (*VariableExpression) expressionNode() {}

// FunctionExpression is an anonymous function expression.
type FunctionExpression struct {
	nodeInfo
	KeywordToken int
	OpenParen    int
	CloseParen   int
	Parameters   []*Parameter
	ReturnType   TypeExpression
	Body         *ExpressionBlock
}

func (*FunctionExpression) expressionNode() {}

// IfExpression is an if expression.
type IfExpression struct {
	nodeInfo
	IfToken   int
	ElseToken int
	Condition Expression
	Then      *ExpressionBlock
	Else      *ExpressionBlock
}

func (*IfExpression) expressionNode() {}

// ParenExpression preserves explicit grouping parentheses.
type ParenExpression struct {
	nodeInfo
	OpenParen  int
	CloseParen int
	Inner      Expression
}

func (*ParenExpression) expressionNode() {}

// UnaryExpression is a unary operator expression.
type UnaryExpression struct {
	nodeInfo
	OperatorToken int
	OperatorRaw   string
	Operand       Expression
}

func (*UnaryExpression) expressionNode() {}

// BinaryExpression is a binary operator expression.
type BinaryExpression struct {
	nodeInfo
	Left          Expression
	OperatorToken int
	OperatorRaw   string
	Right         Expression
}

func (*BinaryExpression) expressionNode() {}

// IndexExpression is a postfix index/default expression.
type IndexExpression struct {
	nodeInfo
	Target       Expression
	OpenBracket  int
	CloseBracket int
	Index        Expression
	DefaultToken int
	DefaultValue Expression
}

func (*IndexExpression) expressionNode() {}

// FieldExpression is a postfix field/default expression.
type FieldExpression struct {
	nodeInfo
	Target       Expression
	ArrowToken   int
	Field        string
	DefaultToken int
	DefaultValue Expression
}

func (*FieldExpression) expressionNode() {}

// PrimitiveType is a primitive type expression.
type PrimitiveType struct {
	nodeInfo
	Name string
}

func (*PrimitiveType) typeExpressionNode() {}

// ListType is a list type expression.
type ListType struct {
	nodeInfo
	OpenBracket  int
	CloseBracket int
	Element      TypeExpression
}

func (*ListType) typeExpressionNode() {}

// MapType is a map type expression.
type MapType struct {
	nodeInfo
	KeywordToken int
	OpenBrace    int
	CloseBrace   int
	Key          TypeExpression
	Value        TypeExpression
}

func (*MapType) typeExpressionNode() {}

// StructType is a struct type expression.
type StructType struct {
	nodeInfo
	KeywordToken int
	OpenBrace    int
	CloseBrace   int
	Fields       []*StructTypeField
}

func (*StructType) typeExpressionNode() {}

// FunctionType is a function type expression.
type FunctionType struct {
	nodeInfo
	KeywordToken int
	OpenParen    int
	CloseParen   int
	Arguments    []*FunctionTypeArgument
	Return       TypeExpression
}

func (*FunctionType) typeExpressionNode() {}
