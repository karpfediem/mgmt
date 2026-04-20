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

package resources

import (
	"context"
	"fmt"
	"maps"
	"net"
	"slices"
	"sort"
	"strings"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func init() {
	engine.RegisterResource("hetzner:firewall", func() engine.Res { return &HetznerFirewallRes{} })
}

// HetznerFirewallRuleSpec mirrors the official Hetzner firewall rule shape.
type HetznerFirewallRuleSpec struct {
	Direction      string   `lang:"direction"`
	SourceIPs      []string `lang:"sourceips"`
	DestinationIPs []string `lang:"destinationips"`
	Protocol       string   `lang:"protocol"`
	Port           string   `lang:"port"`
	Description    string   `lang:"description"`
}

// HetznerFirewallApplyToSpec mirrors the official Hetzner apply_to schema.
type HetznerFirewallApplyToSpec struct {
	Type          string `lang:"type"`
	Server        string `lang:"server"`
	LabelSelector string `lang:"labelselector"`
}

// HetznerFirewallRes manages a Hetzner firewall lifecycle object, including
// its rules and where it applies.
type HetznerFirewallRes struct {
	traits.Base
	traits.Reversible

	init *engine.Init

	APIToken string                       `lang:"apitoken"`
	Rules    []HetznerFirewallRuleSpec    `lang:"rules"`
	ApplyTo  []HetznerFirewallApplyToSpec `lang:"applyto"`
	Labels   map[string]string            `lang:"labels"`

	WaitInterval uint32 `lang:"waitinterval"`
	WaitTimeout  uint32 `lang:"waittimeout"`

	client         *hcloud.Client
	actionWaiter   hetznerActionWaiter
	firewallClient hetznerFirewallLifecycleClient
	serverClient   hetznerServerLookupClient

	firewall *hcloud.Firewall
}

// ReversibleMeta returns the reverse metadata with overwrite enabled by
// default, so reactive firewall spec changes can update the stored reverse
// snapshot without conflicting with an older pending one.
func (obj *HetznerFirewallRes) ReversibleMeta() *engine.ReversibleMeta {
	if obj.Xmeta == nil {
		obj.Xmeta = &engine.ReversibleMeta{
			Overwrite: true,
		}
	}
	return obj.Xmeta
}

// SetReversibleMeta sets the reverse metadata for this resource.
func (obj *HetznerFirewallRes) SetReversibleMeta(meta *engine.ReversibleMeta) {
	obj.Xmeta = meta
}

// Default returns conservative defaults for this resource.
func (obj *HetznerFirewallRes) Default() engine.Res {
	return &HetznerFirewallRes{
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
}

// Validate checks whether the requested firewall spec is valid.
func (obj *HetznerFirewallRes) Validate() error {
	if obj.APIToken == "" {
		return fmt.Errorf("empty token string")
	}
	for i, spec := range obj.Rules {
		if _, err := hetznerFirewallRuleFromSpec(spec); err != nil {
			return errwrap.Wrapf(err, "invalid rules[%d]", i)
		}
	}
	for i, spec := range obj.ApplyTo {
		if err := validateHetznerFirewallApplyToSpec(spec); err != nil {
			return errwrap.Wrapf(err, "invalid applyto[%d]", i)
		}
	}
	if obj.MetaParams().Poll < HetznerPollLimit {
		return fmt.Errorf("invalid polling interval (minimum %d s)", HetznerPollLimit)
	}
	if obj.WaitInterval < HetznerWaitIntervalLimit {
		return fmt.Errorf("invalid wait interval (minimum %d)", HetznerWaitIntervalLimit)
	}
	return nil
}

// Init runs startup code for this resource.
func (obj *HetznerFirewallRes) Init(init *engine.Init) error {
	obj.init = init
	obj.client = newHetznerClient(init, obj.APIToken, obj.WaitInterval)
	obj.actionWaiter = &obj.client.Action
	obj.firewallClient = &obj.client.Firewall
	obj.serverClient = &obj.client.Server
	return nil
}

// Cleanup removes authentication and client state from memory.
func (obj *HetznerFirewallRes) Cleanup() error {
	obj.APIToken = ""
	obj.client = nil
	obj.actionWaiter = nil
	obj.firewallClient = nil
	obj.serverClient = nil
	obj.firewall = nil
	return nil
}

// Watch is not implemented for this resource, since the Hetzner API does not provide events.
func (obj *HetznerFirewallRes) Watch(context.Context) error {
	return fmt.Errorf("invalid Watch call: requires poll metaparam")
}

// CheckApply checks and applies firewall lifecycle state.
func (obj *HetznerFirewallRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	if err := obj.getFirewallUpdate(ctx); err != nil {
		return false, errwrap.Wrapf(err, "getFirewallUpdate failed")
	}

	if obj.firewall == nil {
		if obj.ReversibleMeta().Reversal {
			return true, nil
		}
		if !apply {
			return false, nil
		}
		if err := obj.create(ctx); err != nil {
			return false, errwrap.Wrapf(err, "create failed")
		}
		return false, nil
	}

	if obj.ReversibleMeta().Reversal {
		if !apply {
			return false, nil
		}
		if err := obj.delete(ctx); err != nil {
			return false, errwrap.Wrapf(err, "delete failed")
		}
		return false, nil
	}

	desiredRules, err := hetznerFirewallRulesFromSpecs(obj.Rules)
	if err != nil {
		return false, errwrap.Wrapf(err, "hetznerFirewallRulesFromSpecs failed")
	}
	desiredApplyTo, err := obj.desiredApplyTo(ctx)
	if err != nil {
		return false, errwrap.Wrapf(err, "desiredApplyTo failed")
	}

	checkOK := true
	if !maps.Equal(obj.firewall.Labels, obj.Labels) {
		if !apply {
			return false, nil
		}
		if err := obj.update(ctx); err != nil {
			return false, errwrap.Wrapf(err, "update failed")
		}
		checkOK = false
	}

	if !hetznerFirewallRulesEqual(obj.firewall.Rules, desiredRules) {
		if !apply {
			return false, nil
		}
		if err := obj.setRules(ctx, desiredRules); err != nil {
			return false, errwrap.Wrapf(err, "setRules failed")
		}
		obj.firewall.Rules = slices.Clone(desiredRules)
		checkOK = false
	}

	if !hetznerFirewallApplyToEqual(obj.firewall.AppliedTo, desiredApplyTo) {
		if !apply {
			return false, nil
		}
		if err := obj.reconcileApplyTo(ctx, desiredApplyTo); err != nil {
			return false, errwrap.Wrapf(err, "reconcileApplyTo failed")
		}
		obj.firewall.AppliedTo = slices.Clone(desiredApplyTo)
		checkOK = false
	}

	return checkOK, nil
}

// Copy returns a copy of the resource for compare and reversal handling.
func (obj *HetznerFirewallRes) Copy() engine.CopyableRes {
	rules := make([]HetznerFirewallRuleSpec, 0, len(obj.Rules))
	for _, rule := range obj.Rules {
		rules = append(rules, HetznerFirewallRuleSpec{
			Direction:      rule.Direction,
			SourceIPs:      slices.Clone(rule.SourceIPs),
			DestinationIPs: slices.Clone(rule.DestinationIPs),
			Protocol:       rule.Protocol,
			Port:           rule.Port,
			Description:    rule.Description,
		})
	}
	return &HetznerFirewallRes{
		APIToken:     obj.APIToken,
		Rules:        rules,
		ApplyTo:      slices.Clone(obj.ApplyTo),
		Labels:       maps.Clone(obj.Labels),
		WaitInterval: obj.WaitInterval,
		WaitTimeout:  obj.WaitTimeout,
	}
}

// Reversed returns the reverse resource for cleanup after the firewall has
// disappeared from the graph.
func (obj *HetznerFirewallRes) Reversed() (engine.ReversibleRes, error) {
	cp, err := engine.ResCopy(obj)
	if err != nil {
		return nil, errwrap.Wrapf(err, "could not copy")
	}
	rev, ok := cp.(engine.ReversibleRes)
	if !ok {
		return nil, fmt.Errorf("not reversible")
	}
	rev.ReversibleMeta().Disabled = true
	return rev, nil
}

func (obj *HetznerFirewallRes) create(ctx context.Context) error {
	rules, err := hetznerFirewallRulesFromSpecs(obj.Rules)
	if err != nil {
		return err
	}
	applyTo, err := obj.desiredApplyTo(ctx)
	if err != nil {
		return err
	}
	result, _, err := obj.firewallClient.Create(ctx, hcloud.FirewallCreateOpts{
		Name:    obj.Name(),
		Labels:  obj.Labels,
		Rules:   rules,
		ApplyTo: applyTo,
	})
	if err != nil {
		return err
	}
	if err := hetznerWaitForActions(ctx, obj.WaitTimeout, obj.actionWaiter, result.Actions); err != nil {
		return errwrap.Wrapf(err, "wait for create actions failed")
	}
	obj.firewall = result.Firewall
	return nil
}

func (obj *HetznerFirewallRes) update(ctx context.Context) error {
	firewall, _, err := obj.firewallClient.Update(ctx, obj.firewall, hcloud.FirewallUpdateOpts{
		Labels: obj.Labels,
	})
	if err != nil {
		return err
	}
	obj.firewall = firewall
	return nil
}

func (obj *HetznerFirewallRes) delete(ctx context.Context) error {
	if len(obj.firewall.AppliedTo) > 0 {
		if err := obj.reconcileApplyTo(ctx, nil); err != nil {
			return errwrap.Wrapf(err, "could not detach firewall resources before delete")
		}
		obj.firewall.AppliedTo = nil
	}
	_, err := obj.firewallClient.Delete(ctx, obj.firewall)
	return err
}

func (obj *HetznerFirewallRes) setRules(ctx context.Context, rules []hcloud.FirewallRule) error {
	actions, _, err := obj.firewallClient.SetRules(ctx, obj.firewall, hcloud.FirewallSetRulesOpts{
		Rules: rules,
	})
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForActions(ctx, obj.WaitTimeout, obj.actionWaiter, actions), "wait for set rules actions failed")
}

func (obj *HetznerFirewallRes) reconcileApplyTo(ctx context.Context, desired []hcloud.FirewallResource) error {
	liveMap := hetznerFirewallApplyToMap(obj.firewall.AppliedTo)
	desiredMap := hetznerFirewallApplyToMap(desired)

	removeKeys := []string{}
	for key := range liveMap {
		if _, exists := desiredMap[key]; !exists {
			removeKeys = append(removeKeys, key)
		}
	}
	sort.Strings(removeKeys)
	if len(removeKeys) > 0 {
		removeFrom := make([]hcloud.FirewallResource, 0, len(removeKeys))
		for _, key := range removeKeys {
			removeFrom = append(removeFrom, liveMap[key])
		}
		actions, _, err := obj.firewallClient.RemoveResources(ctx, obj.firewall, removeFrom)
		if err != nil {
			return err
		}
		if err := hetznerWaitForActions(ctx, obj.WaitTimeout, obj.actionWaiter, actions); err != nil {
			return errwrap.Wrapf(err, "wait for remove resources actions failed")
		}
	}

	addKeys := []string{}
	for key := range desiredMap {
		if _, exists := liveMap[key]; !exists {
			addKeys = append(addKeys, key)
		}
	}
	sort.Strings(addKeys)
	if len(addKeys) > 0 {
		applyTo := make([]hcloud.FirewallResource, 0, len(addKeys))
		for _, key := range addKeys {
			applyTo = append(applyTo, desiredMap[key])
		}
		actions, _, err := obj.firewallClient.ApplyResources(ctx, obj.firewall, applyTo)
		if err != nil {
			return err
		}
		if err := hetznerWaitForActions(ctx, obj.WaitTimeout, obj.actionWaiter, actions); err != nil {
			return errwrap.Wrapf(err, "wait for apply resources actions failed")
		}
	}

	return nil
}

func (obj *HetznerFirewallRes) getFirewallUpdate(ctx context.Context) error {
	firewall, err := hetznerFirewallByName(ctx, obj.firewallClient, obj.Name())
	if err != nil {
		return errwrap.Wrapf(err, "firewall lookup failed")
	}
	obj.firewall = firewall
	return nil
}

func (obj *HetznerFirewallRes) desiredApplyTo(ctx context.Context) ([]hcloud.FirewallResource, error) {
	out := make([]hcloud.FirewallResource, 0, len(obj.ApplyTo))
	seen := map[string]struct{}{}
	for _, spec := range obj.ApplyTo {
		resource, err := obj.applyToResourceFromSpec(ctx, spec)
		if err != nil {
			return nil, err
		}
		key := hetznerFirewallApplyToKey(resource)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, resource)
	}
	return out, nil
}

func (obj *HetznerFirewallRes) applyToResourceFromSpec(ctx context.Context, spec HetznerFirewallApplyToSpec) (hcloud.FirewallResource, error) {
	switch spec.Type {
	case string(hcloud.FirewallResourceTypeServer):
		server, err := hetznerServerByName(ctx, obj.serverClient, spec.Server)
		if err != nil {
			return hcloud.FirewallResource{}, errwrap.Wrapf(err, "server lookup failed")
		}
		if server == nil {
			return hcloud.FirewallResource{}, fmt.Errorf("server not found: %s", spec.Server)
		}
		return hcloud.FirewallResource{
			Type:   hcloud.FirewallResourceTypeServer,
			Server: &hcloud.FirewallResourceServer{ID: server.ID},
		}, nil
	case string(hcloud.FirewallResourceTypeLabelSelector):
		return hcloud.FirewallResource{
			Type:          hcloud.FirewallResourceTypeLabelSelector,
			LabelSelector: &hcloud.FirewallResourceLabelSelector{Selector: spec.LabelSelector},
		}, nil
	default:
		return hcloud.FirewallResource{}, fmt.Errorf("invalid applyto type: %s", spec.Type)
	}
}

// Cmp compares two resources.
func (obj *HetznerFirewallRes) Cmp(r engine.Res) error {
	res, ok := r.(*HetznerFirewallRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.APIToken != res.APIToken {
		return fmt.Errorf("apitoken differs")
	}
	leftRules, err := hetznerFirewallRulesFromSpecs(obj.Rules)
	if err != nil {
		return errwrap.Wrapf(err, "left rules invalid")
	}
	rightRules, err := hetznerFirewallRulesFromSpecs(res.Rules)
	if err != nil {
		return errwrap.Wrapf(err, "right rules invalid")
	}
	if !hetznerFirewallRulesEqual(leftRules, rightRules) {
		return fmt.Errorf("rules differ")
	}
	if !hetznerFirewallApplyToSpecsEqual(obj.ApplyTo, res.ApplyTo) {
		return fmt.Errorf("applyto differs")
	}
	if !maps.Equal(obj.Labels, res.Labels) {
		return fmt.Errorf("labels differ")
	}
	if obj.WaitInterval != res.WaitInterval {
		return fmt.Errorf("waitinterval differs")
	}
	if obj.WaitTimeout != res.WaitTimeout {
		return fmt.Errorf("waittimeout differs")
	}
	return nil
}

func validateHetznerFirewallApplyToSpec(spec HetznerFirewallApplyToSpec) error {
	switch spec.Type {
	case string(hcloud.FirewallResourceTypeServer):
		if spec.Server == "" {
			return fmt.Errorf("empty server")
		}
		if spec.LabelSelector != "" {
			return fmt.Errorf("labelselector is only valid for type %s", hcloud.FirewallResourceTypeLabelSelector)
		}
	case string(hcloud.FirewallResourceTypeLabelSelector):
		if spec.LabelSelector == "" {
			return fmt.Errorf("empty labelselector")
		}
		if spec.Server != "" {
			return fmt.Errorf("server is only valid for type %s", hcloud.FirewallResourceTypeServer)
		}
	default:
		return fmt.Errorf("invalid type: %s", spec.Type)
	}
	return nil
}

func hetznerFirewallRulesFromSpecs(specs []HetznerFirewallRuleSpec) ([]hcloud.FirewallRule, error) {
	out := make([]hcloud.FirewallRule, 0, len(specs))
	for _, spec := range specs {
		rule, err := hetznerFirewallRuleFromSpec(spec)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

func hetznerFirewallRuleFromSpec(spec HetznerFirewallRuleSpec) (hcloud.FirewallRule, error) {
	var rule hcloud.FirewallRule
	switch spec.Direction {
	case string(hcloud.FirewallRuleDirectionIn):
		rule.Direction = hcloud.FirewallRuleDirectionIn
		if len(spec.DestinationIPs) > 0 {
			return rule, fmt.Errorf("destinationips are only valid for direction %s", hcloud.FirewallRuleDirectionOut)
		}
	case string(hcloud.FirewallRuleDirectionOut):
		rule.Direction = hcloud.FirewallRuleDirectionOut
		if len(spec.SourceIPs) > 0 {
			return rule, fmt.Errorf("sourceips are only valid for direction %s", hcloud.FirewallRuleDirectionIn)
		}
	default:
		return rule, fmt.Errorf("invalid direction: %s", spec.Direction)
	}

	switch spec.Protocol {
	case string(hcloud.FirewallRuleProtocolTCP):
		rule.Protocol = hcloud.FirewallRuleProtocolTCP
	case string(hcloud.FirewallRuleProtocolUDP):
		rule.Protocol = hcloud.FirewallRuleProtocolUDP
	case string(hcloud.FirewallRuleProtocolICMP):
		rule.Protocol = hcloud.FirewallRuleProtocolICMP
	case string(hcloud.FirewallRuleProtocolESP):
		rule.Protocol = hcloud.FirewallRuleProtocolESP
	case string(hcloud.FirewallRuleProtocolGRE):
		rule.Protocol = hcloud.FirewallRuleProtocolGRE
	default:
		return rule, fmt.Errorf("invalid protocol: %s", spec.Protocol)
	}

	switch rule.Protocol {
	case hcloud.FirewallRuleProtocolTCP, hcloud.FirewallRuleProtocolUDP:
		if spec.Port == "" {
			return rule, fmt.Errorf("empty port")
		}
		port := spec.Port
		rule.Port = &port
	default:
		if spec.Port != "" {
			return rule, fmt.Errorf("port is only valid for tcp and udp protocols")
		}
	}

	sourceIPs, err := hetznerFirewallCIDRsFromStrings(spec.SourceIPs)
	if err != nil {
		return rule, errwrap.Wrapf(err, "invalid sourceips")
	}
	destinationIPs, err := hetznerFirewallCIDRsFromStrings(spec.DestinationIPs)
	if err != nil {
		return rule, errwrap.Wrapf(err, "invalid destinationips")
	}
	rule.SourceIPs = sourceIPs
	rule.DestinationIPs = destinationIPs
	if spec.Description != "" {
		description := spec.Description
		rule.Description = &description
	}
	return rule, nil
}

func hetznerFirewallCIDRsFromStrings(values []string) ([]net.IPNet, error) {
	out := make([]net.IPNet, 0, len(values))
	for _, value := range values {
		cidr, err := parseHetznerCIDR(value)
		if err != nil {
			return nil, err
		}
		if cidr == nil {
			return nil, fmt.Errorf("invalid empty cidr")
		}
		out = append(out, *cidr)
	}
	return out, nil
}

func hetznerFirewallRuleKey(rule hcloud.FirewallRule) string {
	sourceIPs := make([]string, 0, len(rule.SourceIPs))
	for _, cidr := range rule.SourceIPs {
		cidr := cidr
		sourceIPs = append(sourceIPs, hetznerCIDRString(&cidr))
	}
	sort.Strings(sourceIPs)
	destinationIPs := make([]string, 0, len(rule.DestinationIPs))
	for _, cidr := range rule.DestinationIPs {
		cidr := cidr
		destinationIPs = append(destinationIPs, hetznerCIDRString(&cidr))
	}
	sort.Strings(destinationIPs)
	port := ""
	if rule.Port != nil {
		port = *rule.Port
	}
	description := ""
	if rule.Description != nil {
		description = *rule.Description
	}
	return strings.Join([]string{
		string(rule.Direction),
		string(rule.Protocol),
		port,
		description,
		strings.Join(sourceIPs, ","),
		strings.Join(destinationIPs, ","),
	}, "|")
}

func hetznerFirewallRulesEqual(left []hcloud.FirewallRule, right []hcloud.FirewallRule) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, rule := range left {
		counts[hetznerFirewallRuleKey(rule)]++
	}
	for _, rule := range right {
		key := hetznerFirewallRuleKey(rule)
		count, exists := counts[key]
		if !exists || count == 0 {
			return false
		}
		counts[key]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func hetznerFirewallApplyToSpecsEqual(left []HetznerFirewallApplyToSpec, right []HetznerFirewallApplyToSpec) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, spec := range left {
		counts[hetznerFirewallApplyToSpecKey(spec)]++
	}
	for _, spec := range right {
		key := hetznerFirewallApplyToSpecKey(spec)
		count, exists := counts[key]
		if !exists || count == 0 {
			return false
		}
		counts[key]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func hetznerFirewallApplyToSpecKey(spec HetznerFirewallApplyToSpec) string {
	return strings.Join([]string{spec.Type, spec.Server, spec.LabelSelector}, "|")
}

func hetznerFirewallApplyToEqual(left []hcloud.FirewallResource, right []hcloud.FirewallResource) bool {
	leftMap := hetznerFirewallApplyToMap(left)
	rightMap := hetznerFirewallApplyToMap(right)
	if len(leftMap) != len(rightMap) {
		return false
	}
	for key := range leftMap {
		if _, exists := rightMap[key]; !exists {
			return false
		}
	}
	return true
}

func hetznerFirewallApplyToMap(resources []hcloud.FirewallResource) map[string]hcloud.FirewallResource {
	out := make(map[string]hcloud.FirewallResource, len(resources))
	for _, resource := range resources {
		direct := hcloud.FirewallResource{Type: resource.Type}
		switch resource.Type {
		case hcloud.FirewallResourceTypeServer:
			if resource.Server == nil {
				continue
			}
			direct.Server = &hcloud.FirewallResourceServer{ID: resource.Server.ID}
		case hcloud.FirewallResourceTypeLabelSelector:
			if resource.LabelSelector == nil {
				continue
			}
			direct.LabelSelector = &hcloud.FirewallResourceLabelSelector{Selector: resource.LabelSelector.Selector}
		default:
			continue
		}
		out[hetznerFirewallApplyToKey(direct)] = direct
	}
	return out
}

func hetznerFirewallApplyToKey(resource hcloud.FirewallResource) string {
	switch resource.Type {
	case hcloud.FirewallResourceTypeServer:
		if resource.Server == nil {
			return "server:"
		}
		return fmt.Sprintf("server:%d", resource.Server.ID)
	case hcloud.FirewallResourceTypeLabelSelector:
		if resource.LabelSelector == nil {
			return "label_selector:"
		}
		return "label_selector:" + resource.LabelSelector.Selector
	default:
		return "unknown:"
	}
}

func hetznerWaitForActions(ctx context.Context, timeout uint32, waiter hetznerActionWaiter, actions []*hcloud.Action) error {
	if len(actions) == 0 {
		return nil
	}
	return hetznerWaitForAction(ctx, timeout, waiter, actions...)
}
