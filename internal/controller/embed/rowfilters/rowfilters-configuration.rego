# Configuration for the operator's Trino row filters.
#
# Everything is read from the environment so that a change to a DataPlatform's
# spec.authz.rowFilters rolls the OPA Deployment and takes effect immediately.
# A ConfigMap would not, because OPA is not started with --watch.

# regal ignore:directory-package-mismatch
package rowfilters

env := opa.runtime().env

# Base URL of the OpenFGA HTTP API, for example http://openfga.openfga.svc:8080.
openfga_url := trim_right(object.get(env, "OPENFGA_URL", ""), "/")

# Id of the store holding row-filter tuples. Separate from the store LakeKeeper
# owns, whose authorization model LakeKeeper migrates itself.
openfga_store_id := object.get(env, "OPENFGA_STORE_ID", "")

# Pre-shared key for the OpenFGA API.
openfga_api_key := object.get(env, "OPENFGA_API_KEY", "")

# How long a set of permitted values stays cached. One Trino query fans out into
# many authorization requests, and each filtered table would otherwise re-probe
# OpenFGA every time.
cache_seconds := to_number(object.get(env, "OPENFGA_CACHE_SECONDS", "30"))

# Row filters, as rendered by the operator from spec.authz.rowFilters. Each
# entry has catalog, schema, table, column, type, relation and numeric fields.
default filters := []

filters := parsed if {
	raw := object.get(env, "TRINO_ROW_FILTERS", "")
	raw != ""
	parsed := json.unmarshal(raw)
}

# Column restrictions, as rendered from spec.authz.columnAccess. Each entry has
# catalog, schema, table, column, mask, type, relation and object fields.
default column_access := []

column_access := parsed if {
	raw := object.get(env, "TRINO_COLUMN_ACCESS", "")
	raw != ""
	parsed := json.unmarshal(raw)
}
