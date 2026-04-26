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
	"strings"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"golang.org/x/crypto/acme"
)

func acmeSolverWatch(ctx context.Context, init *engine.Init, requestNamespace string, certificates []string) error {
	keys := []string{}
	for _, certificate := range certificates {
		certificate = strings.TrimSpace(certificate)
		if certificate == "" || certificate == "*" {
			continue
		}
		ns := acmeCertificateNamespace(certificate, requestNamespace)
		keys = append(keys, acmeSpecKey(ns), acmeCurrentKey(ns), acmeAttemptKey(ns))
	}
	mapKeys := []string{acmeRequestIndexKey(requestNamespace)}
	return acmeWatchWorld(ctx, init, keys, mapKeys, 30*time.Second)
}

func acmeReadAccountInfo(ctx context.Context, world engine.StrWorld, accountName string) (*acmeAccountInfo, error) {
	if accountName == "" {
		return nil, fmt.Errorf("account must not be empty")
	}
	var info acmeAccountInfo
	exists, err := acmeWorldReadJSON(ctx, world, acmeAccountNamespace(accountName), &info)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("account info %s does not exist", acmeAccountNamespace(accountName))
	}
	if info.Version != acmeVersion {
		return nil, fmt.Errorf("unsupported account info version %d", info.Version)
	}
	return &info, nil
}

func acmeReadSpec(ctx context.Context, world engine.StrWorld, namespace string) (*acmeCertSpec, bool, error) {
	var spec acmeCertSpec
	exists, err := acmeWorldReadJSON(ctx, world, acmeSpecKey(namespace), &spec)
	if err != nil || !exists {
		return nil, exists, err
	}
	if spec.Version != acmeVersion {
		return nil, true, fmt.Errorf("unsupported spec version %d in %s", spec.Version, acmeSpecKey(namespace))
	}
	if spec.SpecDigest == "" {
		return nil, true, fmt.Errorf("spec %s has empty digest", acmeSpecKey(namespace))
	}
	return &spec, true, nil
}

func acmeCurrentBundleUsable(ctx context.Context, world engine.StrWorld, namespace string, spec *acmeCertSpec) (bool, uint64, error) {
	var current acmeCertCurrent
	exists, err := acmeWorldReadJSON(ctx, world, acmeCurrentKey(namespace), &current)
	if err != nil {
		return false, 0, err
	}
	if !exists {
		return false, 0, nil
	}
	generation := current.Generation
	if current.SpecDigest != spec.SpecDigest || current.BundleDigest == "" {
		return false, generation, nil
	}
	var bundle acmeCertBundle
	bundleExists, err := acmeWorldReadJSON(ctx, world, acmeBundleKey(namespace, current.BundleDigest), &bundle)
	if err != nil {
		return false, generation, err
	}
	if !bundleExists {
		return false, generation, nil
	}
	if err := acmeBundleUsable(&bundle, spec); err != nil {
		return false, generation, nil
	}
	return true, generation, nil
}

func acmeFindChallenge(authz *acme.Authorization, challengeType string) *acme.Challenge {
	for _, chal := range authz.Challenges {
		if chal != nil && chal.Type == challengeType {
			return chal
		}
	}
	return nil
}

func acmeIssueAndPublish(ctx context.Context, init *engine.Init, req *acmeCertificateRequest, accountInfo *acmeAccountInfo, presenter acmePresenter) error {
	client, _, err := acmeEnsureAccount(ctx, accountInfo)
	if err != nil {
		return err
	}

	certSigner, err := acmeGenerateSigner(req.Spec.KeyAlgorithm)
	if err != nil {
		return err
	}
	privatePEM, err := acmeMarshalPrivateKeyPEM(certSigner)
	if err != nil {
		return err
	}
	csrDER, err := acmeCreateCSRDER(certSigner, req.Spec.Domains)
	if err != nil {
		return err
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(req.Spec.Domains...))
	if err != nil {
		return err
	}
	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return err
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		if authz.Status != acme.StatusPending {
			return fmt.Errorf("authorization %s for %s is not pending or valid: %s", authz.URI, authz.Identifier.Value, authz.Status)
		}
		chal := acmeFindChallenge(authz, presenter.challengeType())
		if chal == nil {
			return fmt.Errorf("authorization %s for %s has no %s challenge", authz.URI, authz.Identifier.Value, presenter.challengeType())
		}
		presentCtx := ctx
		cancel := func() {}
		if timeout := presenter.presentationTimeout(); timeout > 0 {
			presentCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		cleanup, err := presenter.present(presentCtx, req, client, authz, chal)
		cancel()
		if err != nil {
			return err
		}
		_, acceptErr := client.Accept(ctx, chal)
		_, waitErr := client.WaitAuthorization(ctx, authz.URI)
		cleanupErr := cleanup(ctx)
		if acceptErr != nil {
			return acceptErr
		}
		if waitErr != nil {
			return waitErr
		}
		if cleanupErr != nil {
			return cleanupErr
		}
	}

	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return err
	}
	var der [][]byte
	if order.Status == acme.StatusValid && order.CertURL != "" {
		der, err = client.FetchCert(ctx, order.CertURL, true)
		if err != nil {
			return err
		}
	} else {
		der, _, err = client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
		if err != nil {
			return err
		}
	}

	certPEM, chainPEM, fullchainPEM, leaf, err := acmePEMFromDER(der)
	if err != nil {
		return err
	}
	bundle := &acmeCertBundle{
		Version:    acmeVersion,
		Namespace:  req.Namespace,
		SpecDigest: req.Spec.SpecDigest,
		Generation: req.Generation + 1,
		Domains:    append([]string{}, req.Spec.Domains...),
		NotBefore:  leaf.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:   leaf.NotAfter.UTC().Format(time.RFC3339),
		PrivateKey: acmePrivateKeyBundle{
			Mode: acmeDefaultPrivateKeyMode,
			PEM:  string(privatePEM),
		},
		CertPEM:      certPEM,
		ChainPEM:     chainPEM,
		FullchainPEM: fullchainPEM,
	}
	digest, err := acmeBundleDigest(bundle)
	if err != nil {
		return err
	}
	bundle.BundleDigest = digest
	if err := acmeBundleUsable(bundle, req.Spec); err != nil {
		return err
	}

	if _, err := acmeWorldWriteJSON(ctx, init.World, acmeBundleKey(req.Namespace, digest), bundle); err != nil {
		return err
	}
	current := &acmeCertCurrent{
		Version:      acmeVersion,
		SpecDigest:   req.Spec.SpecDigest,
		BundleDigest: digest,
		Generation:   bundle.Generation,
		NotBefore:    bundle.NotBefore,
		NotAfter:     bundle.NotAfter,
	}
	if _, err := acmeWorldWriteJSON(ctx, init.World, acmeCurrentKey(req.Namespace), current); err != nil {
		return err
	}
	init.Logf("ACME certificate bundle %s published for %s", digest, req.Namespace)
	return nil
}

func acmeSolverCheckApply(ctx context.Context, init *engine.Init, accountName string, requestNamespace string, certificates []string, presenter acmePresenter, apply bool) (bool, error) {
	accountInfo, err := acmeReadAccountInfo(ctx, init.World, accountName)
	if err != nil {
		return false, err
	}

	names, err := acmeResolveCertificateNames(ctx, init, requestNamespace, certificates)
	if err != nil {
		return false, err
	}

	changed := false
	prepared := false
	var cleanupPrepared func(context.Context) error
	defer func() {
		if cleanupPrepared != nil {
			_ = cleanupPrepared(context.Background())
		}
	}()
	for _, name := range names {
		namespace := acmeCertificateNamespace(name, requestNamespace)
		spec, exists, err := acmeReadSpec(ctx, init.World, namespace)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		canSolve, err := presenter.canSolve(spec)
		if err != nil {
			return false, err
		}
		if !canSolve {
			continue
		}
		usable, generation, err := acmeCurrentBundleUsable(ctx, init.World, namespace, spec)
		if err != nil {
			return false, err
		}
		if usable {
			continue
		}
		if until := presenter.cooldownUntil(namespace); !until.IsZero() && time.Now().Before(until) {
			continue
		}
		if !apply {
			return false, nil
		}
		if !prepared {
			cleanup, eligible, err := presenter.prepare(ctx)
			if err != nil {
				return false, err
			}
			if !eligible {
				return true, nil
			}
			cleanupPrepared = cleanup
			prepared = true
		}
		req := &acmeCertificateRequest{
			Name:       name,
			Namespace:  namespace,
			Spec:       spec,
			Generation: generation,
		}
		if err := acmeIssueAndPublish(ctx, init, req, accountInfo, presenter); err != nil {
			if cooldown := presenter.cooldownDuration(); cooldown > 0 {
				presenter.setCooldown(namespace, time.Now().Add(cooldown))
			}
			return false, err
		}
		changed = true
	}

	if changed {
		return false, nil
	}
	return true, nil
}
