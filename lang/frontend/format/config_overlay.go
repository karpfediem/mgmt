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

// ConfigOverlay is a partial formatter configuration used by callers such as
// the CLI to override a base Config without having to restate defaults.
type ConfigOverlay struct {
	Blocks      *BlockRulesOverlay      `yaml:"blocks"`
	Collections *CollectionRulesOverlay `yaml:"collections"`
	Comments    *CommentRulesOverlay    `yaml:"comments"`
	Spacing     *SpacingRulesOverlay    `yaml:"spacing"`
}

// BlockRulesOverlay is a partial override for BlockRules.
type BlockRulesOverlay struct {
	KeepEmptyInline *bool                        `yaml:"keep_empty_inline"`
	Expression      *ExpressionBlockRulesOverlay `yaml:"expression"`
}

// ExpressionBlockRulesOverlay is a partial override for ExpressionBlockRules.
type ExpressionBlockRulesOverlay struct {
	InlineSimpleValues      *bool `yaml:"inline_simple_values"`
	AllowCalls              *bool `yaml:"allow_calls"`
	AllowOperators          *bool `yaml:"allow_operators"`
	AllowCollections        *bool `yaml:"allow_collections"`
	AllowNestedConditionals *bool `yaml:"allow_nested_conditionals"`
	SymmetricIfBranches     *bool `yaml:"symmetric_if_branches"`
}

// CollectionRulesOverlay is a partial override for CollectionRules.
type CollectionRulesOverlay struct {
	MaxInlineListElements     *int  `yaml:"max_inline_list_elements"`
	MaxInlineMapEntries       *int  `yaml:"max_inline_map_entries"`
	MaxInlineStructFields     *int  `yaml:"max_inline_struct_fields"`
	PreserveExistingMultiline *bool `yaml:"preserve_existing_multiline"`
	CollapseShortLists        *bool `yaml:"collapse_short_lists"`
	CollapseShortMaps         *bool `yaml:"collapse_short_maps"`
	CollapseShortStructs      *bool `yaml:"collapse_short_structs"`
}

// CommentRulesOverlay is a partial override for CommentRules.
type CommentRulesOverlay struct {
	NormalizeInlineSpacing *bool `yaml:"normalize_inline_spacing"`
}

// SpacingRulesOverlay is a partial override for SpacingRules.
type SpacingRulesOverlay struct {
	SpaceAroundBinaryOperators *bool `yaml:"space_around_binary_operators"`
}

func setBool(dst *bool, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func setInt(dst *int, src *int) {
	if src != nil {
		*dst = *src
	}
}

// ApplyTo applies the overlay to cfg in place.
func (obj ConfigOverlay) ApplyTo(cfg *Config) {
	if obj.Blocks != nil {
		obj.Blocks.applyTo(&cfg.Blocks)
	}
	if obj.Collections != nil {
		obj.Collections.applyTo(&cfg.Collections)
	}
	if obj.Comments != nil {
		obj.Comments.applyTo(&cfg.Comments)
	}
	if obj.Spacing != nil {
		obj.Spacing.applyTo(&cfg.Spacing)
	}
}

func (obj *BlockRulesOverlay) applyTo(cfg *BlockRules) {
	setBool(&cfg.KeepEmptyInline, obj.KeepEmptyInline)
	if obj.Expression != nil {
		obj.Expression.applyTo(&cfg.Expression)
	}
}

func (obj *ExpressionBlockRulesOverlay) applyTo(cfg *ExpressionBlockRules) {
	setBool(&cfg.InlineSimpleValues, obj.InlineSimpleValues)
	setBool(&cfg.AllowCalls, obj.AllowCalls)
	setBool(&cfg.AllowOperators, obj.AllowOperators)
	setBool(&cfg.AllowCollections, obj.AllowCollections)
	setBool(&cfg.AllowNestedConditionals, obj.AllowNestedConditionals)
	setBool(&cfg.SymmetricIfBranches, obj.SymmetricIfBranches)
}

func (obj *CollectionRulesOverlay) applyTo(cfg *CollectionRules) {
	setInt(&cfg.MaxInlineListElements, obj.MaxInlineListElements)
	setInt(&cfg.MaxInlineMapEntries, obj.MaxInlineMapEntries)
	setInt(&cfg.MaxInlineStructFields, obj.MaxInlineStructFields)
	setBool(&cfg.PreserveExistingMultiline, obj.PreserveExistingMultiline)
	setBool(&cfg.CollapseShortLists, obj.CollapseShortLists)
	setBool(&cfg.CollapseShortMaps, obj.CollapseShortMaps)
	setBool(&cfg.CollapseShortStructs, obj.CollapseShortStructs)
}

func (obj *CommentRulesOverlay) applyTo(cfg *CommentRules) {
	setBool(&cfg.NormalizeInlineSpacing, obj.NormalizeInlineSpacing)
}

func (obj *SpacingRulesOverlay) applyTo(cfg *SpacingRules) {
	setBool(&cfg.SpaceAroundBinaryOperators, obj.SpaceAroundBinaryOperators)
}
