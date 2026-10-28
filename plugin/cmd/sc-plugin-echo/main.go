// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

// sc-plugin-echo is the TEMPLATE every SC cloud plugin clones from.
//
// What it does: implements every RPC the SC plugin bus exposes as a
// canned-success stub, so `go build ./...` + `go test ./...` pass against
// the template untouched, and so an author can grep for `echo` and
// replace it piece by piece without having to re-derive the file layout
// or the common scaffolding (pulumiContextFromHeader, waitForRef,
// LogStream, strVal, main()).
//
// What it does NOT do: provision anything real. The resources registered
// below are pure Pulumi ComponentResources — logical URN groupings with
// no cloud side-effects. An author swaps the body of each RPC for the
// real cloud-driven work; the surrounding structure stays.
//
// See `docs/docs/guides/plugin-authoring.md` in the api repo for the
// walkthrough: config tier → provisioning tier → tests → release.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/pkg/errors"

	sdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/simple-container-com/api/pkg/api"
	"github.com/simple-container-com/api/pkg/clouds/vm"
	scPlugin "github.com/simple-container-com/api/pkg/plugin"
	scpluginv1 "github.com/simple-container-com/api/pkg/plugin/proto/v1"

	echoApi "github.com/simple-container-com/sc-plugin-echo/api"
	echoInternal "github.com/simple-container-com/sc-plugin-echo/plugin/internal"
)

// echoPlugin is the gRPC server impl. Fields follow the same shape every
// real plugin uses: a mutex, cached registrar/handler state carried
// between RPCs within one deploy, and a progress Publisher drained by
// LogStream.
type echoPlugin struct {
	scpluginv1.UnimplementedSCCloudPluginServer

	mu        sync.Mutex
	registrar *echoInternal.Registrar

	// progress is the plugin's side of the LogStream RPC. SC core opens
	// one LogStream per plugin after Schema() returns, and this is where
	// per-RPC narration goes. Drop progress and the plugin is silent for
	// the whole of a multi-step RPC chain — operators see nothing until
	// the deploy either succeeds or fails much later.
	progress *scPlugin.Publisher
}

// Schema is the first RPC SC core calls. The response advertises every
// token this plugin owns, both at config-parse time (so core routes
// `type: X` to this plugin's RPCs) and at runtime (so core knows which
// Pulumi providers the plugin needs on PATH).
//
// Keep this shape in sync with /api/init.go's OwnedTypes list — the two
// must agree. init.go's reflection test catches gaps in the OwnedTypes
// half; a Schema() that omits a type is caught at the next conformance
// run.
func (p *echoPlugin) Schema(_ context.Context, _ *scpluginv1.SchemaRequest) (*scpluginv1.SchemaResponse, error) {
	return &scpluginv1.SchemaResponse{
		PluginVersion:         "echo-0.0.1",
		ProviderTypes:         []string{echoApi.ProviderType},
		RegistrarTypes:        []string{echoApi.RegistrarTypeEchoDns},
		ResourceTypes:         []string{echoApi.ResourceTypeEchoBucket},
		ComputeProcessorTypes: []string{echoApi.ResourceTypeEchoBucket},
		PulumiPlugins:         []*scpluginv1.PulumiPluginRequirement{
			// Empty for echo — a real cloud lists the Pulumi provider
			// plugin names it imports: `{Package: "cloudflare", Version:
			// "6.17.0"}`, `{Package: "yandex", Version: "1.1.0"}`, etc.
			// Core materialises these in the stack's `plugins` block so
			// `pulumi up` finds them. See
			// github.com/pulumi/pulumi-<name>/sdk for versions.
		},
		VmDrivers: []*scpluginv1.VmDriverSpec{
			// A real VM-driving plugin echoes vm.Drivers[DRIVER].ToProto()
			// so core's capability table and the plugin's agree by reading
			// the same source. The echo driver advertises an in-template
			// stub so the URN contract test can exercise VmDriverSpec
			// round-tripping.
			echoVmDriverSpec(),
		},
	}, nil
}

// echoVmDriverSpec returns the VmDriverSpec for the echo-vm driver. In a
// real plugin this would be `vm.Drivers[vm.Driver<Cloud>].ToProto(name)`
// where the Drivers map entry lives in-tree (the capability facts a
// driver reports — see pkg/clouds/vm/capabilities.go for the shape).
//
// Echo doesn't have an in-tree capabilities entry, so this stub builds
// one by hand. An author replaces this with the ToProto call.
func echoVmDriverSpec() *scpluginv1.VmDriverSpec {
	return &scpluginv1.VmDriverSpec{
		Driver:      echoApi.DriverEchoVm,
		Implemented: true,
		Unverified:  "echo driver is a template stub — it does not actually create any VM. Replace with a real cloud's driver.",
		RoadmapItem: "docs/roadmap/items/sc-plugin-architecture.md",
		Capabilities: &scpluginv1.VmCapabilities{
			CloudInit:      false,
			Regionless:     true,
			SecurityGroups: false,
		},
	}
}

// pulumiContextFromHeader opens a short-lived pulumi.Context against the
// monitor address SC core puts in the request header. One per RPC — the
// scope of a Pulumi context is the scope of an RPC, and reusing a context
// across RPCs produces resources that cannot be addressed from the right
// monitor.
//
// Load-bearing: Pulumi fires RegisterResource from a goroutine, so a
// handler that returns before its outputs resolve lets `defer pctx.Close`
// tear the engine connection down mid-registration. The deploy hangs
// with no error. waitForRef below is the countermeasure.
func (p *echoPlugin) pulumiContextFromHeader(ctx context.Context, h *scpluginv1.RequestHeader) (*sdk.Context, func(), error) {
	if h.GetMonitorAddress() == "" {
		return nil, nil, errors.New("no monitor address in request header")
	}
	pctx, err := sdk.NewContext(ctx, sdk.RunInfo{
		MonitorAddr: h.GetMonitorAddress(),
		Project:     h.GetProjectName(),
		Stack:       h.GetStackName(),
	})
	if err != nil {
		return nil, nil, errors.Wrap(err, "open pulumi context from monitor address")
	}
	return pctx, func() { pctx.Close() }, nil
}

func (p *echoPlugin) ProvisionRegistrar(ctx context.Context, req *scpluginv1.ProvisionRegistrarRequest) (*scpluginv1.ProvisionRegistrarResponse, error) {
	// A real registrar reads the deploying stack's `baseDnsZone` from
	// req.GetBaseDnsZone() for its zone lookup — NOT its own configured
	// zoneName, which is only a default. Getting this wrong is how a
	// child-stack deploy writes a record into the WRONG zone and the
	// delete-replace half removes a live record (incident 2026-10-28,
	// Addendum R).
	stackZone := req.GetBaseDnsZone()
	configZone := strVal(req.GetConfig()["zoneName"])
	p.progress.Info("provision-registrar", fmt.Sprintf("initialising echo registrar; stack prefers %q (config default %q)", stackZone, configZone))

	pctx, done, err := p.pulumiContextFromHeader(ctx, req.GetHeader())
	if err != nil {
		return nil, err
	}
	defer done()

	r, err := echoInternal.NewRegistrar(pctx, stackZone, configZone)
	if err != nil {
		p.progress.Error("provision-registrar", "init failed: "+err.Error())
		return nil, errors.Wrap(err, "init echo registrar")
	}
	p.mu.Lock()
	p.registrar = r
	p.mu.Unlock()
	p.progress.Info("provision-registrar", fmt.Sprintf("ready; main domain is %q", r.MainDomain()))
	return &scpluginv1.ProvisionRegistrarResponse{MainDomain: r.MainDomain()}, nil
}

func (p *echoPlugin) NewDnsRecord(ctx context.Context, req *scpluginv1.NewDnsRecordRequest) (*scpluginv1.NewDnsRecordResponse, error) {
	pctx, done, err := p.pulumiContextFromHeader(ctx, req.GetHeader())
	if err != nil {
		return nil, err
	}
	defer done()
	p.mu.Lock()
	r := p.registrar
	p.mu.Unlock()
	if r == nil {
		return nil, errors.New("NewDnsRecord called before ProvisionRegistrar")
	}
	p.progress.Info("dns-record", fmt.Sprintf("creating %s record %q → %q", req.GetRecordType(), req.GetRecordName(), req.GetValueResolved()))
	urnStr, err := r.NewRecord(pctx, req.GetRecordName(), req.GetRecordType(), req.GetValueResolved())
	if err != nil {
		p.progress.Error("dns-record", fmt.Sprintf("record %q failed: %v", req.GetRecordName(), err))
		return nil, err
	}
	p.progress.Info("dns-record", fmt.Sprintf("record %q registered (urn=%s)", req.GetRecordName(), urnStr))
	return &scpluginv1.NewDnsRecordResponse{Urn: urnStr}, nil
}

// Provision is the catch-all RPC for a `type: X` resource whose plugin
// advertises X in ResourceTypes. The echo stub registers a logical
// ComponentResource — no cloud side-effects — and returns its URN.
//
// A real cloud resource constructs provider resources from
// pulumi-<cloud>, which is why Schema's PulumiPlugins must advertise
// those packages.
func (p *echoPlugin) Provision(ctx context.Context, req *scpluginv1.ProvisionRequest) (*scpluginv1.ProvisionResponse, error) {
	pctx, done, err := p.pulumiContextFromHeader(ctx, req.GetHeader())
	if err != nil {
		return nil, err
	}
	defer done()
	p.progress.Info("provision", fmt.Sprintf("provisioning echo resource %q (type %q)", req.GetResourceName(), req.GetResourceType()))
	urnStr, err := echoInternal.ProvisionEcho(pctx, req.GetResourceType(), req.GetResourceName())
	if err != nil {
		p.progress.Error("provision", err.Error())
		return nil, err
	}
	p.progress.Info("provision", fmt.Sprintf("resource %q registered (urn=%s)", req.GetResourceName(), urnStr))
	return &scpluginv1.ProvisionResponse{Urn: urnStr}, nil
}

// VmHosts converges a VM resource whose `provider:` names a driver this
// plugin advertises (echo-vm). The echo stub returns one byo-shaped
// host with a TEST-NET address — enough to exercise the plugin bus's
// VmHostsResponse shape without reaching a real cloud.
//
// A real VM-driving plugin registers cloud resources (instance, floating
// IP, deploy key), parses their outputs via plugin.OutputRefTo /
// AddressOutput / PrivateKeyOf, and composes the VmHostBuilder chain.
// See api/pkg/plugin/vmhosts.go for the builder surface.
func (p *echoPlugin) VmHosts(ctx context.Context, req *scpluginv1.VmHostsRequest) (*scpluginv1.VmHostsResponse, error) {
	p.progress.Info("vm-hosts", fmt.Sprintf("provisioning echo vm %q (driver=%s)", req.GetResourceName(), req.GetDriver()))
	pctx, done, err := p.pulumiContextFromHeader(ctx, req.GetHeader())
	if err != nil {
		return nil, err
	}
	defer done()

	resp, err := echoInternal.VmHosts(pctx, req.GetResourceName())
	if err != nil {
		p.progress.Error("vm-hosts", fmt.Sprintf("vm %q failed: %v", req.GetResourceName(), err))
		return nil, err
	}
	p.progress.Info("vm-hosts", fmt.Sprintf("vm %q ready, %d host(s)", req.GetResourceName(), len(resp.GetHosts())))
	return resp, nil
}

// LogStream drains the plugin's progress Publisher onto the gRPC stream.
// SC core opens one of these per plugin after Schema() returns. Ends when
// the stream context cancels (SC core teardown) or Serve stops accepting.
func (p *echoPlugin) LogStream(_ *scpluginv1.LogStreamRequest, stream scpluginv1.SCCloudPlugin_LogStreamServer) error {
	ctx := stream.Context()
	for {
		batch, ok := p.progress.Drain()
		if !ok {
			return nil
		}
		for _, ev := range batch {
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// strVal reads a string input from the plugin bus's typed input map.
// Every plugin needs this — it's a six-line helper rather than a shared
// api/ helper only because a cloud with a complex config generally wants
// to lift the whole input map into a typed struct via decodeInputsAs[T],
// and the two can't share.
func strVal(v *scpluginv1.InputValue) string {
	if v == nil {
		return ""
	}
	return v.GetStringValue()
}

// Reference used only to anchor the vm package import — a real plugin
// uses vm.Drivers[name].ToProto in Schema(). Prevents an unused-import
// warning while the stub is still a stub.
var _ = vm.ResourceTypeVm

// Reference used only to anchor the api package import for operators who
// grep. A real plugin's ProvisionRegistrar builds an api.RegistrarDescriptor,
// NewDnsRecord an api.DnsRecord, etc.
var _ = api.Credentials{}

func main() {
	impl := &echoPlugin{progress: scPlugin.NewPublisher(0)}
	scPlugin.Serve(scPlugin.ServerConfig{
		Impl: impl,
		Logger: hclog.New(&hclog.LoggerOptions{
			Name:   "sc-plugin-echo",
			Level:  hclog.Warn,
			Output: os.Stderr,
		}),
	})
}
