# data-lake-operator

Kubernetes operator that deploys the resources needed to bootstrap a data lake.

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

## Deploying to a cluster

```bash
export IMG=<registry>/data-lake-operator:<tag>
make docker-build docker-push IMG=$IMG
make deploy IMG=$IMG
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
