# sc-plugin-echo

Template every SC cloud plugin clones from. The point of this repo is
NOT to deploy anything — the resources below register as logical Pulumi
ComponentResources with no cloud side-effects. The point is to carry the
scaffolding and conventions every plugin needs, so a new cloud plugin is
a `gh repo create --template` and a grep-replace of `echo` away.

## Structure

- `/api` — the CONFIG TIER. Separate Go module (SC core imports it).
  Pattern per `init.go`:
  - A `const ProviderType` plus a set of per-token constants
    (AuthType*, RegistrarType*, TemplateType*, ResourceType*,
    Driver*, StateStorageType*, SecretsProviderType*).
  - `init()` calls `api.RegisterProviderConfig(...)` to wire the config
    readers and `api.RegisterPluginRequirement(...)` so SC core's
    fool-protection auto-inject can find this plugin by any owned
    token.
  - **OwnedTypes must list every token this plugin owns.** Missing one
    is a silent gap — an operator who only uses the un-listed token
    gets no auto-inject and the deploy fails later with
    "unknown resource type". `api/init_test.go` guards this.

- `/plugin` — the PROVISIONING TIER. Separate Go module.
  `cmd/sc-plugin-echo/main.go` is the go-plugin gRPC server. Pattern:
  - `Schema()` echoes every owned token into the proto, plus
    `PulumiPlugins` (the SDKs this plugin links against) and
    `VmDrivers` (the vm.Drivers table rows this plugin owns).
  - `pulumiContextFromHeader` opens ONE `sdk.Context` per RPC against
    the monitor address in the request header. Reusing a context
    across RPCs produces resources that cannot be addressed from the
    right monitor.
  - `waitForRef` is the countermeasure for Pulumi's async
    RegisterResource — a handler that returns before its outputs
    resolve lets `defer pctx.Close()` tear the engine connection down
    mid-registration, and the deploy hangs with no error.

- `/plugin/internal/urn_contract_test.go` — the gate that locks the
  URN name contract. Uses `pulumi.NewContext` + `RunInfo.Mocks`,
  **not** `pulumi.RunErr`, so the assertion lands on the handler's
  return rather than on the harness's RPC drain (which would silently
  complete stranded registrations — Addendum R §R3 of
  `04-no-destroy-invariant.md`, incident 2026-10-28).

- `.github/workflows/release.yml` — CalVer `vYYYY.MM.DD-<sha>` auto-release
  on push to `main`, or named releases on `v*` tags. Three-platform
  build (linux amd64+arm64, darwin arm64) + `sha256sums.txt`.
  **Carries the OOM-safe pins** (`GOMAXPROCS=1 GOMEMLIMIT=6GiB`,
  `go build -p 1`). Keep them even for a plugin whose Pulumi SDK is
  small today.

## Running tests

```
cd api && go test ./...
cd plugin && go test ./...
```

Both must stay green on `main`.

## Writing a new plugin from this template

See `docs/docs/guides/plugin-authoring.md` in the api repo for the full
walkthrough. Short version in the README.
