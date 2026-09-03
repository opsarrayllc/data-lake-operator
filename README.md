# Data Platform Operator

Kubernetes operator that deploys the resources needed to bootstrap a data platform.

Goals:
* OIDC immediately
  * Keycloak locally
  * OIDC Configuration overrides
* Fine-Grained Authorization immediately
  * OpenFGA
* Runs on as many cloud platforms as we can reasonably test on
* Open Source Utilities
* Cost should be negligible and scale appropriately

Built with [Kubebuilder](https://book.kubebuilder.io) v4 / controller-runtime.

## API

| Group | Version | Kind |
| --- | --- | --- |
| `dataplatform.opsarray.io` | `v1alpha1` | `DataPlatform` |

The type lives in `api/v1alpha1/dataplatform_types.go` and its reconciler in
`internal/controller/dataplatform_controller.go`.

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
make test-rego            # OPA policy tests (internal/controller/embed)
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
ghcr.io/opsarrayllc/data-platform-operator
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
helm install data-platform-operator \
  oci://ghcr.io/opsarrayllc/charts/data-platform-operator \
  --version 0.1.0 \
  --namespace data-platform-system --create-namespace
```

The chart version and the operator image tag are released together, so the
default `image.tag` follows the chart's `appVersion` and you rarely need to set
it. Chart source lives in `deploy/data-platform-operator`; see its `values.yaml` for
the full set of options. Notable ones:

| Value | Default | Purpose |
| --- | --- | --- |
| `crds.enabled` | `true` | Install the `DataPlatform` CRD with the release |
| `crds.keep` | `true` | Keep the CRD (and all `DataPlatform`s) on uninstall |
| `metrics.secure` | `true` | Serve metrics over HTTPS with authn/authz |
| `metrics.serviceMonitor.enabled` | `false` | Create a Prometheus `ServiceMonitor` |
| `replicaCount` | `1` | Extra replicas are standbys; leader election picks one |

The CRD template is generated from `config/crd/bases`, so after changing the API
run `make helm-crds` and commit the result. CI fails the release if the chart's
copy is stale.

## Deploying with kustomize

```bash
export IMG=ghcr.io/opsarrayllc/data-platform-operator:latest
make deploy IMG=$IMG
```

To build and push the image yourself instead of relying on CI:

```bash
export IMG=<registry>/data-platform-operator:<tag>
make docker-build docker-push IMG=$IMG   # single-arch, your host platform
make docker-buildx IMG=$IMG              # multi-arch
```

Tear down with `make undeploy` and `make uninstall`.

To produce a single self-contained manifest for distribution:

```bash
make build-installer IMG=$IMG   # writes dist/install.yaml
```

## Row-level access control

LakeKeeper and OpenFGA authorize whole objects: a warehouse, a namespace, a
table. Two optional lists narrow that further in Trino:

- `spec.authz.rowFilters` keeps only the rows whose key column holds a value
  the user may see.
- `spec.authz.columnAccess` replaces a restricted column with a mask (NULL by
  default) unless the user holds a grant on that column.

```yaml
spec:
  authz:
    rowFilters:
      - schema: sales
        table: orders
        column: region
        openfga:
          type: region
          relation: viewer
    columnAccess:
      - schema: sales
        table: orders
        column: ssn
        openfga:
          type: column
          relation: viewer
```

The operator provisions a dedicated OpenFGA store for these types (separate
from LakeKeeper's, whose authorization model LakeKeeper migrates itself), points
Trino at OPA's row-filter and batch-column-mask endpoints, and OPA turns each
user's grants into a `WHERE region IN (...)` clause or a column mask.

Granting a user a value or a column is deliberately not the operator's job,
since it owns neither your user list nor your data. Write those tuples against
the store id in `status.rowFilterStoreID`, using the API key in secret
`openfga/openfga`:

```bash
STORE=$(kubectl get dataplatform <name> -o jsonpath='{.status.rowFilterStoreID}')
curl -sS "$OPENFGA/stores/$STORE/write" \
  -H "Authorization: Bearer $OPENFGA_API_KEY" -H 'Content-Type: application/json' \
  -d '{"writes":{"tuple_keys":[
        {"user":"user:alice","relation":"viewer","object":"region:emea"},
        {"user":"group:analysts#member","relation":"viewer","object":"region:us-east"},
        {"user":"user:alice","relation":"viewer","object":"column:lakekeeper.sales.orders.ssn"}
      ]}}'
```

The default column object is `{catalog}.{schema}.{table}.{column}`. Set
`openfga.object` to a short name such as `ssn` to share one grant across
tables. Tuple users are Trino usernames (the `preferred_username` claim), and a
relation may be granted to a user directly or to a group's members.

Both features fail closed: a user with no row-filter grant sees no rows, a
user with no column grant sees the mask, and everyone does if OpenFGA becomes
unreachable. Restricted columns still appear in the schema; their values do
not. See `config/samples/dataplatform_v1alpha1_rowfilters.yaml`.

## Adding new APIs

Use the CLI rather than hand-creating files, so the `PROJECT` file and scaffold
markers stay consistent:

```bash
kubebuilder create api --group dataplatform --version v1alpha1 --kind <Kind> --resource --controller
```

Then re-run `make manifests generate`.

## Notes

- `config/crd/bases/*`, `config/rbac/role.yaml`, `**/zz_generated.*.go`, and `PROJECT`
  are generated. Don't edit them by hand.
- Don't remove `// +kubebuilder:scaffold:*` comments; the CLI injects code at those points.
- End-to-end tests (`make test-e2e`) expect a dedicated kind cluster, not a real environment.
- See `AGENTS.md` for a fuller conventions guide.
