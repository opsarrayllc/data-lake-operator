Operator-owned OPA policies, kept out of `../opapolicies` so that directory stays
a clean vendored copy of LakeKeeper's OPA bridge.

`rowfilters-configuration.rego` reads the operator's environment variables, and
`trino-row_filters.rego` extends the vendored `trino` package with the
`row_filters` rule that Trino's `opa.policy.row-filters-uri` calls.

Both are loaded into the same OPA bundle as the vendored policies, so the
`trino` package is shared across the two directories.
