# Column access for Trino, driven by OpenFGA relationship tuples.
#
# LakeKeeper's OPA bridge (../opapolicies) decides whether a user may read a
# table at all, and that decision is all-or-nothing for every column. This
# extends the same `trino` package with the masks that hide a restricted
# column's values, which Trino fetches from
# `opa.policy.batch-column-masking-uri`.
#
# A user who holds the configured relation on the column's object sees the
# real value. Everyone else sees the mask (NULL by default).

package trino

import data.rowfilters

# Trino expects an array of {index, viewExpression} objects. An empty result
# leaves every column unmasked.
batch_column_masks contains {
	"index": i,
	"viewExpression": {"expression": access.mask},
} if {
	some i
	column := input.action.filterResources[i].column
	some access in rowfilters.column_access
	not _is_admin
	_matches_column(access, column)
	not _column_permitted(access)
}

_matches_column(access, column) if {
	access.catalog == column.catalogName
	access.schema == column.schemaName
	access.table == column.tableName
	access.column == column.columnName
}

# Fail closed. A missing store, an unreachable OpenFGA, or a user with no
# grant all produce an empty object list, so the column is masked. Returning
# no mask at all would expose the real values.
_column_permitted(access) if {
	wanted := sprintf("%s:%s", [access.type, access.object])
	some listed in _list_objects(access)
	listed == wanted
}
