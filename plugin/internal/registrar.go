// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

// Package echo is the plugin-side implementation of the echo cloud
// template. Everything a real plugin's /plugin/internal/* package would
// hold — the lifted driver, the per-resource constructors — lives here
// as a stub so an author can see the shape.
package echo

import (
	"github.com/pkg/errors"

	sdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Registrar is the echo DNS registrar's handle, cached by main.go across
// RPCs within one deploy. A real registrar carries cloud credentials,
// clients, zone-cache maps, etc. Echo keeps just the resolved zone.
type Registrar struct {
	effectiveZone string
}

// NewRegistrar is the echo analogue of sc-plugin-cloudflare/plugin/
// internal/registrar.go's `Registrar()`. Takes the stack's preferred
// `baseDnsZone` and the registrar config's `zoneName` default, and
// resolves which one wins — the STACK's zone, with the config's zone as
// the fallback. This is the one line that MUST survive the swap to a
// real cloud; see the zone-vs-baseDnsZone section of plugin-authoring.md
// and the 2026-10-28 incident (Addendum R of 04-no-destroy-invariant.md).
func NewRegistrar(_ *sdk.Context, stackZone, configZone string) (*Registrar, error) {
	if stackZone == "" && configZone == "" {
		return nil, errors.New("neither the deploying stack's baseDnsZone nor the registrar's zoneName is set — the echo registrar has nothing to resolve against")
	}
	effective := stackZone
	if effective == "" {
		effective = configZone
	}
	return &Registrar{effectiveZone: effective}, nil
}

func (r *Registrar) MainDomain() string {
	return r.effectiveZone
}

// NewRecord registers one DNS record. The echo stub registers a logical
// Pulumi ComponentResource — no cloud side-effects — and returns its URN
// as the ref.
//
// A real registrar constructs a provider resource from pulumi-<cloud>
// here (`cloudflare.NewRecord`, `yandex.NewDnsRecordSet`, ...).
func (r *Registrar) NewRecord(ctx *sdk.Context, recordName, recordType, value string) (string, error) {
	resourceName := recordName + "-" + recordType
	comp := &echoComponent{}
	if err := ctx.RegisterComponentResource(
		"echo:dns:Record",
		resourceName,
		comp,
	); err != nil {
		return "", errors.Wrapf(err, "register echo:dns:Record %q", resourceName)
	}
	// Await the URN before returning so pctx.Close() doesn't tear down
	// the engine connection mid-registration. See waitForRef notes in
	// main.go — same reason, different helper (ComponentResource.URN()
	// is a sdk.URNOutput, which we convert to a plain string).
	urnCh := make(chan string, 1)
	comp.URN().ApplyT(func(u sdk.URN) sdk.URN {
		select {
		case urnCh <- string(u):
		default:
		}
		return u
	})
	return <-urnCh, nil
}

// ProvisionEcho is the echo stub for a top-level `type: X` resource (the
// Provision RPC). Registers a logical ComponentResource and returns its
// URN. A real plugin's Provision reads credentials from the request,
// constructs cloud resources, and returns the URN of the top one.
func ProvisionEcho(ctx *sdk.Context, resourceType, resourceName string) (string, error) {
	comp := &echoComponent{}
	if err := ctx.RegisterComponentResource(
		"echo:index:"+resourceType,
		resourceName,
		comp,
	); err != nil {
		return "", errors.Wrapf(err, "register %s %q", resourceType, resourceName)
	}
	urnCh := make(chan string, 1)
	comp.URN().ApplyT(func(u sdk.URN) sdk.URN {
		select {
		case urnCh <- string(u):
		default:
		}
		return u
	})
	return <-urnCh, nil
}

// echoComponent is a no-op Pulumi ComponentResource: a logical URN with
// no children and no cloud side-effects. Every echo RPC that needs to
// return a Ref registers one of these.
type echoComponent struct {
	sdk.ResourceState
}
