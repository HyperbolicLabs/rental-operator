# Contributing to rental-operator

Thanks for your interest. Bug reports, fixes, docs and features are all welcome. See a missing feature? Open an issue to discuss it, or send a PR.

## Before you start

- **Bugs:** open an issue with the bug template. Include versions, the resource YAML and operator logs, with API keys removed.
- **Features and API changes:** open an issue first so we can agree on the design before you write code. Changes to the CRDs need a short design note in the issue.
- **Security issues:** do not open a public issue. See [SECURITY.md](SECURITY.md).

## Development

You need Go (the version in `go.mod`), Docker, and for end-to-end tests [kind](https://kind.sigs.k8s.io/). The Makefile downloads the other tools (controller-gen, kustomize, golangci-lint, envtest) into `./bin`.

The operator talks to Hyperbolic through [`hyperbolic-go`](https://github.com/HyperbolicLabs/hyperbolic-go), which is private for now. Until it is public you need read access to it, and:

```console
export GOPRIVATE=github.com/HyperbolicLabs/*
export HYPERBOLIC_GO_TOKEN=<a read-only GitHub token>   # only for make docker-build
```

CI reads the same token from the `HYPERBOLIC_GO_READ_TOKEN` Actions secret.

```console
make build              # regenerate code and manifests, then build bin/manager
make test               # unit and envtest suites
make lint               # golangci-lint
make manifests generate # regenerate CRDs, RBAC and deepcopy after changing api/
make test-e2e           # end-to-end tests on a throwaway kind cluster
```

After changing anything under `api/` or a `+kubebuilder` marker, run `make manifests generate` and commit the result, including the CRD copies under `charts/rental-operator/crds/`.

Tests must never call the real Hyperbolic API. Controller tests run against the in-memory fake from `hyperbolic-go/hyperbolictest`, which can also inject failures (rate limits, 5xx, timeouts, failures after the server applied a request). See `internal/controller/fakeapi_test.go`.

## Pull requests

- Keep PRs focused: one logical change per PR.
- Add or update tests for every behavior change. Code paths that create or terminate rentals (anything that spends money) need tests for their failure modes, not only the happy path.
- Update the README or `docs/` when user-visible behavior changes.
- CI (lint, tests, end-to-end) must pass before review.
- PR titles follow `type(scope): subject` (for example `fix(gpurental): never remove the finalizer while a rental may be billing`). PRs are squash-merged, so the title becomes the commit on `main`.
- A breaking change to the API needs `!` in the title, the `breaking-api-change` label, and migration steps in the description. CI fails on an incompatible CRD change without the label.

## Releases

Releases follow [semantic versioning](https://semver.org/). Before 1.0, minor versions may contain breaking changes. They are always called out in the release notes. See [Releasing](docs/development.md#releasing) for how a release is cut.
