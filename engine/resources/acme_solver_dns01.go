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
	"reflect"
	"strings"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"golang.org/x/crypto/acme"
)

func init() {
	engine.RegisterResource(KindAcmeSolverDNS01, func() engine.Res { return &AcmeSolverDNS01Res{} })
}

// AcmeSolverDNS01Res is a scheduled ACME issuer worker which publishes dns-01
// attempt data for ordinary DNS resources and waits for TXT propagation.
type AcmeSolverDNS01Res struct {
	traits.Base

	init *engine.Init

	// Account is the acme:account resource name. The account resource publishes
	// its non-secret configuration into World under acme/account/<name>.
	Account string `lang:"account" yaml:"account"`
	// RequestNamespace is the World namespace containing certificate requests.
	RequestNamespace string `lang:"request_namespace" yaml:"request_namespace"`
	// Certificates are request names this solver may issue, or "*" for all
	// indexed requests in RequestNamespace.
	Certificates []string `lang:"certificates" yaml:"certificates"`

	// Zones optionally restrict which DNS zones this solver may solve.
	Zones []string `lang:"zones" yaml:"zones"`

	Resolvers []string `lang:"resolvers" yaml:"resolvers"`

	AttemptTTL          uint64 `lang:"attempt_ttl" yaml:"attempt_ttl"`
	PresentationTimeout uint64 `lang:"presentation_timeout" yaml:"presentation_timeout"`
	PollInterval        uint64 `lang:"poll_interval" yaml:"poll_interval"`
	PresentationSettle  uint64 `lang:"presentation_settle" yaml:"presentation_settle"`
	Cooldown            uint64 `lang:"cooldown" yaml:"cooldown"`

	cooldowns map[string]time.Time
}

// Default returns some sensible defaults for this resource.
func (obj *AcmeSolverDNS01Res) Default() engine.Res {
	return &AcmeSolverDNS01Res{
		RequestNamespace:    acmeDefaultRequestNamespace,
		AttemptTTL:          uint64(900),
		PresentationTimeout: acmeDefaultPropagation,
		PollInterval:        acmeDefaultPollInterval,
		PresentationSettle:  acmeDefaultPresentationSettle,
		Cooldown:            acmeDefaultCooldown,
	}
}

// Validate if the params passed in are valid data.
func (obj *AcmeSolverDNS01Res) Validate() error {
	if obj.Account == "" {
		return fmt.Errorf("account must not be empty")
	}
	if obj.requestNamespace() == "" {
		return fmt.Errorf("request_namespace must not be empty")
	}
	if len(obj.Certificates) == 0 {
		return fmt.Errorf("certificates must not be empty")
	}
	for _, name := range obj.Certificates {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("certificate name must not be empty")
		}
		if name != "*" && strings.Contains(name, "/") {
			return fmt.Errorf("certificate name %q must not contain slash", name)
		}
	}
	if len(obj.Zones) == 0 {
		return fmt.Errorf("zones must not be empty")
	}
	for _, zone := range obj.Zones {
		z := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
		if err := acmeValidateDNSIdentifier(z); err != nil {
			return err
		}
	}
	if obj.presentationTimeout() < obj.pollInterval() {
		return fmt.Errorf("presentation_timeout must be greater than or equal to poll_interval")
	}
	return nil
}

// Init initializes the resource.
func (obj *AcmeSolverDNS01Res) Init(init *engine.Init) error {
	obj.init = init
	obj.cooldowns = make(map[string]time.Time)
	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *AcmeSolverDNS01Res) Cleanup() error { return nil }

// Watch is the primary listener for this resource and it outputs events.
func (obj *AcmeSolverDNS01Res) Watch(ctx context.Context) error {
	return acmeSolverWatch(ctx, obj.init, obj.requestNamespace(), obj.Certificates)
}

func (obj *AcmeSolverDNS01Res) requestNamespace() string {
	return acmeRequestNamespace(obj.RequestNamespace)
}

func (obj *AcmeSolverDNS01Res) attemptTTL() time.Duration {
	seconds := obj.AttemptTTL
	if seconds == 0 {
		seconds = 900
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverDNS01Res) presentationTimeout() time.Duration {
	seconds := obj.PresentationTimeout
	if seconds == 0 {
		seconds = acmeDefaultPropagation
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverDNS01Res) pollInterval() time.Duration {
	seconds := obj.PollInterval
	if seconds == 0 {
		seconds = acmeDefaultPollInterval
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverDNS01Res) presentationSettle() time.Duration {
	return time.Duration(obj.PresentationSettle) * time.Second
}

func (obj *AcmeSolverDNS01Res) cooldownDuration() time.Duration {
	seconds := obj.Cooldown
	if seconds == 0 {
		seconds = acmeDefaultCooldown
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverDNS01Res) cooldownUntil(namespace string) time.Time {
	return obj.cooldowns[namespace]
}

func (obj *AcmeSolverDNS01Res) setCooldown(namespace string, until time.Time) {
	obj.cooldowns[namespace] = until
}

func (obj *AcmeSolverDNS01Res) owner() string {
	return obj.init.Hostname + "/" + obj.Kind() + "[" + obj.Name() + "]"
}

func (obj *AcmeSolverDNS01Res) challengeType() string { return acmeChallengeDNS01 }

func (obj *AcmeSolverDNS01Res) prepare(ctx context.Context) (func(context.Context) error, bool, error) {
	return nil, true, nil
}

func (obj *AcmeSolverDNS01Res) canSolve(spec *acmeCertSpec) (bool, error) {
	zones := []string{}
	for _, zone := range obj.Zones {
		zones = append(zones, strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), "."))
	}
	for _, domain := range spec.Domains {
		base := strings.TrimPrefix(domain, "*.")
		ok := false
		for _, zone := range zones {
			if base == zone || strings.HasSuffix(base, "."+zone) {
				ok = true
				break
			}
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (obj *AcmeSolverDNS01Res) zoneForDomain(domain string) (string, error) {
	base := strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(domain, ".")), "*.")
	for _, zone := range obj.Zones {
		z := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
		if base == z || strings.HasSuffix(base, "."+z) {
			return z, nil
		}
	}
	return "", fmt.Errorf("dns-01 solver %s is not configured for domain %q", obj.Name(), domain)
}

func (obj *AcmeSolverDNS01Res) present(ctx context.Context, req *acmeCertificateRequest, client *acme.Client, authz *acme.Authorization, chal *acme.Challenge) (func(context.Context) error, error) {
	baseDomain := strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(authz.Identifier.Value, ".")), "*.")
	dnsName := "_acme-challenge." + baseDomain
	dnsValue, err := client.DNS01ChallengeRecord(chal.Token)
	if err != nil {
		return nil, err
	}
	zone, err := obj.zoneForDomain(authz.Identifier.Value)
	if err != nil {
		return nil, err
	}
	record := &acmeAttemptRecord{
		Domain:   baseDomain,
		DNSZone:  zone,
		DNSName:  dnsName,
		DNSValue: dnsValue,
	}
	if err := acmePublishAttempt(ctx, obj.init, req, obj, record); err != nil {
		return nil, err
	}
	cleanup := func(ctx context.Context) error {
		return acmeClearAttempt(ctx, obj.init, req.Namespace)
	}
	if settle := obj.presentationSettle(); settle > 0 {
		select {
		case <-time.After(settle):
		case <-ctx.Done():
			_ = cleanup(context.Background())
			return nil, ctx.Err()
		}
	}
	if err := obj.waitPresented(ctx, dnsName, dnsValue); err != nil {
		_ = cleanup(ctx)
		return nil, err
	}
	return cleanup, nil
}

func (obj *AcmeSolverDNS01Res) waitPresented(ctx context.Context, dnsName, dnsValue string) error {
	return acmeWaitForTXT(ctx, dnsName, dnsValue, obj.Resolvers, obj.presentationTimeout(), obj.pollInterval())
}

// CheckApply method for resource.
func (obj *AcmeSolverDNS01Res) CheckApply(ctx context.Context, apply bool) (bool, error) {
	return acmeSolverCheckApply(ctx, obj.init, obj.Account, obj.requestNamespace(), obj.Certificates, obj, apply)
}

// Cmp compares two resources and returns an error if they are not equivalent.
func (obj *AcmeSolverDNS01Res) Cmp(r engine.Res) error {
	res, ok := r.(*AcmeSolverDNS01Res)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.Account != res.Account {
		return fmt.Errorf("the Account differs")
	}
	if obj.requestNamespace() != res.requestNamespace() {
		return fmt.Errorf("the RequestNamespace differs")
	}
	if !reflect.DeepEqual(obj.Certificates, res.Certificates) {
		return fmt.Errorf("the Certificates differs")
	}
	if !reflect.DeepEqual(obj.Zones, res.Zones) {
		return fmt.Errorf("the Zones differs")
	}
	if !reflect.DeepEqual(obj.Resolvers, res.Resolvers) {
		return fmt.Errorf("the Resolvers differs")
	}
	if obj.attemptTTL() != res.attemptTTL() {
		return fmt.Errorf("the AttemptTTL differs")
	}
	if obj.presentationTimeout() != res.presentationTimeout() {
		return fmt.Errorf("the PresentationTimeout differs")
	}
	if obj.pollInterval() != res.pollInterval() {
		return fmt.Errorf("the PollInterval differs")
	}
	if obj.presentationSettle() != res.presentationSettle() {
		return fmt.Errorf("the PresentationSettle differs")
	}
	if obj.cooldownDuration() != res.cooldownDuration() {
		return fmt.Errorf("the Cooldown differs")
	}
	return nil
}

// AcmeSolverDNS01UID is the UID struct for AcmeSolverDNS01Res.
type AcmeSolverDNS01UID struct {
	engine.BaseUID
	name string
}

// UIDs includes all params to make a unique identification of this object.
func (obj *AcmeSolverDNS01Res) UIDs() []engine.ResUID {
	x := &AcmeSolverDNS01UID{
		BaseUID: engine.BaseUID{Name: obj.Name(), Kind: obj.Kind()},
		name:    obj.Name(),
	}
	return []engine.ResUID{x}
}

// UnmarshalYAML is the custom unmarshal handler for this struct.
func (obj *AcmeSolverDNS01Res) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawRes AcmeSolverDNS01Res
	def := obj.Default()
	res, ok := def.(*AcmeSolverDNS01Res)
	if !ok {
		return fmt.Errorf("could not convert to AcmeSolverDNS01Res")
	}
	raw := rawRes(*res)
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*obj = AcmeSolverDNS01Res(raw)
	return nil
}
