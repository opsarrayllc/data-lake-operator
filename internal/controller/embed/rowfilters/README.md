Operator-owned OPA policies, kept out of `../opapolicies` so that directory stays
a clean vendored copy of LakeKeeper's OPA bridge.

`rowfilters-configuration.rego` reads the operator's environment variables.
`trino-row_filters.rego` and `trino-column_masks.rego` extend the vendored
`trino` package with the rules Trino's `opa.policy.row-filters-uri` and
`opa.policy.batch-column-masking-uri` call.

Both are loaded into the same OPA bundle as the vendored policies, so the
`trino` package is shared across the two directories.
