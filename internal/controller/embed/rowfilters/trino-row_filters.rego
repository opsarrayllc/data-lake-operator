# Row filters for Trino, driven by OpenFGA relationship tuples.
#
# LakeKeeper's OPA bridge (../opapolicies) decides whether a user may read a
# table at all. This extends the same `trino` package with the row filters that
# narrow a readable table down to the rows the user is entitled to, which Trino
# fetches from `opa.policy.row-filters-uri`.
#
# For a filter on `region`, the permitted values are the OpenFGA objects of type
# `region` on which the user holds the configured relation. `region:emea` and
# `region:us-east` become `"region" IN ('emea', 'us-east')`.

package trino

import data.configuration
import data.rowfilters

# Trino expects an array of {"expression": "<sql>"} objects, each applied as an
# extra WHERE clause. An empty result leaves the table unfiltered.
row_filters contains {"expression": expression} if {
	some filter in rowfilters.filters
	not _is_admin
	_matches_resource(filter)
	expression := _expression(filter)
}

# Admins configured on the OPA bridge read tables unfiltered, matching the
# blanket access they already have over system schemas.
_is_admin if {
	trino_user_id in configuration.trino_admin_users
}

_matches_resource(filter) if {
	table := input.action.resource.table
	filter.catalog == table.catalogName
	filter.schema == table.schemaName
	filter.table == table.tableName
}

_expression(filter) := sprintf("%s IN (%s)", [_column(filter), concat(", ", literals)]) if {
	literals := _literals(filter)
	count(literals) > 0
}

# No permitted value means no visible row. This also covers an unreachable
# OpenFGA, since _permitted_values falls back to an empty set: a row filter that
# hides everything is the safe failure, whereas returning no filter at all would
# expose the whole table.
_expression(filter) := "false" if {
	count(_literals(filter)) == 0
}

_literals(filter) := [literal |
	some value in _permitted_values(filter)
	literal := _sql_literal(filter, value)
]

# Quoted by default, doubling any embedded quote. A numeric filter compares
# against a bare literal instead, and only when the value really is a number, so
# that an object id can never be spliced into the SQL as an expression.
_sql_literal(filter, value) := sprintf("'%s'", [replace(value, "'", "''")]) if {
	not filter.numeric
}

_sql_literal(filter, value) := value if {
	filter.numeric
	regex.match(`^-?[0-9]+(\.[0-9]+)?$`, value)
}

_column(filter) := sprintf(`"%s"`, [filter.column])

# Objects of the filter's type on which this user holds the relation, with the
# type prefix stripped: ["region:emea"] becomes ["emea"]. Sorted so that the
# generated SQL is stable across evaluations.
default _permitted_values(_) := []

_permitted_values(filter) := sort([value |
	some object_id in _list_objects(filter)
	startswith(object_id, _type_prefix(filter))
	value := trim_prefix(object_id, _type_prefix(filter))
]) if {
	_list_objects(filter)
}

_type_prefix(filter) := sprintf("%s:", [filter.type])

# OpenFGA resolves group membership itself, so a tuple may grant the relation to
# "user:alice" or to "group:analysts#member".
_list_objects(filter) := object.get(response, ["body", "objects"], []) if {
	rowfilters.openfga_store_id != ""
	response := http.send({
		"method": "POST",
		"url": sprintf("%s/stores/%s/list-objects", [rowfilters.openfga_url, rowfilters.openfga_store_id]),
		"headers": {
			"Authorization": sprintf("Bearer %v", [rowfilters.openfga_api_key]),
			"Content-Type": "application/json",
		},
		"body": {
			"type": filter.type,
			"relation": filter.relation,
			"user": sprintf("user:%s", [trino_user_id]),
		},
		"force_cache": true,
		"force_cache_duration_seconds": rowfilters.cache_seconds,
		"caching_mode": "deserialized",
		"raise_error": false,
	})
	response.status_code == 200
}
