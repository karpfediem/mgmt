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

// Config controls formatter policy decisions that do not affect syntax or
// semantics.
type Config struct {
	Blocks      BlockRules      `yaml:"blocks"`
	Collections CollectionRules `yaml:"collections"`
	Comments    CommentRules    `yaml:"comments"`
	Spacing     SpacingRules    `yaml:"spacing"`
}

// BlockRules control statement and expression block formatting.
type BlockRules struct {
	// KeepEmptyInline preserves empty statement and resource blocks as {}.
	KeepEmptyInline bool `yaml:"keep_empty_inline"`

	// Expression controls inline formatting of expression blocks.
	Expression ExpressionBlockRules `yaml:"expression"`
}

// ExpressionBlockRules control when an expression block is printed as
// `{ expr }` instead of a multiline block. The decision is local to each
// block: the block must contain exactly one expression, must not own comments
// that would become ambiguous when collapsed, and that expression must be
// syntactically simple under the enabled rules below.
type ExpressionBlockRules struct {
	// InlineSimpleValues allows literals, variables, field access, index access,
	// and parens around those forms to count as simple. Simplicity is a
	// recursive syntactic predicate, not a semantic or line-length heuristic.
	InlineSimpleValues bool `yaml:"inline_simple_values"`

	// AllowCalls permits inline expression blocks containing call expressions
	// when their arguments also satisfy the printer's simple-expression checks.
	AllowCalls bool `yaml:"allow_calls"`

	// AllowOperators permits unary and binary operator expressions to stay
	// inline when their operands are otherwise inlineable.
	AllowOperators bool `yaml:"allow_operators"`

	// AllowCollections permits inlineable list, map, and struct literals to
	// stay inline inside expression blocks.
	AllowCollections bool `yaml:"allow_collections"`

	// AllowNestedConditionals permits `if` expressions themselves to count as
	// simple when their condition and both branches satisfy the same syntactic
	// simplicity checks. Each nested `if` still formats its own branches
	// recursively even when the surrounding block stays multiline.
	AllowNestedConditionals bool `yaml:"allow_nested_conditionals"`

	// SymmetricIfBranches keeps `if` expression branches in the same layout mode
	// so one multiline branch forces the other branch multiline as well.
	SymmetricIfBranches bool `yaml:"symmetric_if_branches"`
}

// CollectionRules control short collection layout decisions.
type CollectionRules struct {
	// MaxInlineListElements is the largest simple list that stays inline. Lists
	// above this size are expanded vertically.
	MaxInlineListElements int `yaml:"max_inline_list_elements"`

	// MaxInlineMapEntries is the largest simple map literal that stays inline.
	// Maps above this size are expanded vertically.
	MaxInlineMapEntries int `yaml:"max_inline_map_entries"`

	// MaxInlineStructFields is the largest simple struct literal that stays
	// inline. Struct literals above this size are expanded vertically.
	MaxInlineStructFields int `yaml:"max_inline_struct_fields"`

	// PreserveExistingMultiline keeps author-chosen multiline layout for short
	// lists, maps, and struct literals when they otherwise qualify for inline
	// formatting.
	PreserveExistingMultiline bool `yaml:"preserve_existing_multiline"`

	// CollapseShortLists allows already-multiline short lists to collapse to a
	// single line when they otherwise fit the canonical inline style and
	// PreserveExistingMultiline is disabled.
	CollapseShortLists bool `yaml:"collapse_short_lists"`

	// CollapseShortMaps allows already-multiline short maps to collapse to a
	// single line when they otherwise fit the canonical inline style and
	// PreserveExistingMultiline is disabled.
	CollapseShortMaps bool `yaml:"collapse_short_maps"`

	// CollapseShortStructs allows already-multiline short struct literals to
	// collapse to a single line when they otherwise fit the canonical inline
	// style and PreserveExistingMultiline is disabled.
	CollapseShortStructs bool `yaml:"collapse_short_structs"`
}

// CommentRules control comment spacing normalization.
type CommentRules struct {
	// NormalizeInlineSpacing rewrites inline comment gaps to a single space
	// before `#`.
	NormalizeInlineSpacing bool `yaml:"normalize_inline_spacing"`
}

// SpacingRules control token spacing normalization.
type SpacingRules struct {
	// SpaceAroundBinaryOperators inserts spaces around symbolic binary
	// operators. Word operators such as `and`, `or`, and `in` always keep their
	// required separating spaces.
	SpaceAroundBinaryOperators bool `yaml:"space_around_binary_operators"`
}

// DefaultConfig returns the default formatter policy.
func DefaultConfig() Config {
	return Config{
		Blocks: BlockRules{
			KeepEmptyInline: true,
			Expression: ExpressionBlockRules{
				InlineSimpleValues:      true,
				SymmetricIfBranches:     true,
				AllowCalls:              false,
				AllowOperators:          false,
				AllowCollections:        false,
				AllowNestedConditionals: false,
			},
		},
		Collections: CollectionRules{
			MaxInlineListElements:     3,
			MaxInlineMapEntries:       3,
			MaxInlineStructFields:     3,
			PreserveExistingMultiline: true,
			CollapseShortLists:        true,
			CollapseShortMaps:         true,
			CollapseShortStructs:      true,
		},
		Comments: CommentRules{
			NormalizeInlineSpacing: true,
		},
		Spacing: SpacingRules{
			SpaceAroundBinaryOperators: true,
		},
	}
}

func (obj Config) normalized() Config {
	if obj.Collections.MaxInlineListElements < 0 {
		obj.Collections.MaxInlineListElements = 0
	}
	if obj.Collections.MaxInlineMapEntries < 0 {
		obj.Collections.MaxInlineMapEntries = 0
	}
	if obj.Collections.MaxInlineStructFields < 0 {
		obj.Collections.MaxInlineStructFields = 0
	}
	return obj
}
