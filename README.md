# Data Lake Operator

Kubernetes operator that deploys the resources needed to bootstrap a data lake.

We've stood up all the individual pieces one by one and struggled with all the separate configurations and getting it all to work properly together. Let's switch gears and have it automated. 

Goals:
* Cross platform
* Runs on as many cloud platforms as we can reasonably test on
* Open Source Utilities
* Cost should be negligible and scale appropriately with data size

Built with [Kubebuilder](https://book.kubebuilder.io) v4 / controller-runtime.

## API

| Group | Version | Kind |
| --- | --- | --- |
| `datalake.opsarray.io` | `v1alpha1` | `DataLake` |

The type lives in `api/v1alpha1/datalake_types.go` and its reconciler in
`internal/controller/datalake_controller.go`.

## Prerequisites

- Go 1.26+
- Docker (or set `CONTAINER_TOOL` to another OCI builder)
- `kubectl` and access to a cluster ([kind](https://kind.sigs.k8s.io/) is fine for local work)

All other tooling (`controller-gen`, `kustomize`, `setup-envtest`, `golangci-lint`) is
downloaded into `./bin` automatically by the Makefile. That directory is gitignored.

## Common tasks

```bash
make manifests generate   # regenerate CRDs, RBAC, and DeepCopy code after editing types
make test                 # unit + envtest suite
make lint                 # golangci-lint
make build                # compile the manager to bin/manager
make run                  # run the controller locally against your current kubecontext
```

`make run` uses your active kubeconfig context, so install the CRDs first:

```bash
make install
kubectl apply -k config/samples/
```

## Container image

CI publishes multi-arch (`linux/amd64`, `linux/arm64`) images to GitHub Packages:

```
ghcr.io/opsarrayllc/data-lake-operator
```

Tags are derived from the triggering ref: `latest` and `main` from the default
branch, `sha-<commit>` for every build, and semver tags (`1.2.3`, `1.2`, `1`)
when you push a `v*` tag. Pull requests are built as a check but never pushed.

To cut a release:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

## Installing with Helm

The chart is published to GitHub Packages as an OCI artifact and installs the
CRD, RBAC, and controller together:

```bash
helm install data-lake-operator \
  oci://ghcr.io/opsarrayllc/charts/data-lake-operator \
  --version 0.1.0 \
  --namespace data-lake-system --create-namespace
```

The chart version and the operator image tag are released together, so the
default `image.tag` follows the chart's `appVersion` and you rarely need to set
it. Chart source lives in `deploy/data-lake-operator`; see its `values.yaml` for
the full set of options. Notable ones:

| Value | Default | Purpose |
| --- | --- | --- |
| `crds.enabled` | `true` | Install the `DataLake` CRD with the release |
| `crds.keep` | `true` | Keep the CRD (and all `DataLake`s) on uninstall |
| `metrics.secure` | `true` | Serve metrics over HTTPS with authn/authz |
| `metrics.serviceMonitor.enabled` | `false` | Create a Prometheus `ServiceMonitor` |
| `replicaCount` | `1` | Extra replicas are standbys; leader election picks one |

The CRD template is generated from `config/crd/bases`, so after changing the API
run `make helm-crds` and commit the result. CI fails the release if the chart's
copy is stale.

## Deploying with kustomize

```bash
export IMG=ghcr.io/opsarrayllc/data-lake-operator:latest
make deploy IMG=$IMG
```

To build and push the image yourself instead of relying on CI:

```bash
export IMG=<registry>/data-lake-operator:<tag>
make docker-build docker-push IMG=$IMG   # single-arch, your host platform
make docker-buildx IMG=$IMG              # multi-arch
```

Tear down with `make undeploy` and `make uninstall`.

To produce a single self-contained manifest for distribution:

```bash
make build-installer IMG=$IMG   # writes dist/install.yaml
```

## Adding new APIs

Use the CLI rather than hand-creating files, so the `PROJECT` file and scaffold
markers stay consistent:

```bash
kubebuilder create api --group datalake --version v1alpha1 --kind <Kind> --resource --controller
```

Then re-run `make manifests generate`.

## Notes

- `config/crd/bases/*`, `config/rbac/role.yaml`, `**/zz_generated.*.go`, and `PROJECT`
  are generated. Don't edit them by hand.
- Don't remove `// +kubebuilder:scaffold:*` comments; the CLI injects code at those points.
- End-to-end tests (`make test-e2e`) expect a dedicated kind cluster, not a real environment.
- See `AGENTS.md` for a fuller conventions guide.
