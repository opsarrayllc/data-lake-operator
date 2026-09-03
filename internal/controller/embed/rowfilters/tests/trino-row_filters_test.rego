# Tests for the operator's Trino row filters.
#
# Deliberately outside the parent directory so that `go:embed` does not ship
# them to OPA. Run them with `make test-rego`.

package trino_test

import data.trino

# Config as the operator renders it into TRINO_ROW_FILTERS.
region_filter := {
	"catalog": "lakekeeper",
	"schema": "sales",
	"table": "orders",
	"column": "region",
	"type": "region",
	"relation": "viewer",
	"numeric": false,
}

select_orders := {
	"context": {"identity": {"user": "alice"}},
	"action": {
		"operation": "SelectFromColumns",
		"resource": {"table": {
			"catalogName": "lakekeeper",
			"schemaName": "sales",
			"tableName": "orders",
		}},
	},
}

openfga_ok(objects) := {"status_code": 200, "body": {"objects": objects}}

test_permitted_values_become_an_in_clause if {
	trino.row_filters == {{"expression": `"region" IN ('emea', 'us-east')`}} with input as select_orders
		with data.rowfilters.filters as [region_filter]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["region:us-east", "region:emea"])
}

# A user with no grants sees no rows, rather than the whole table.
test_no_permitted_values_hides_every_row if {
	trino.row_filters == {{"expression": "false"}} with input as select_orders
		with data.rowfilters.filters as [region_filter]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok([])
}

# Fail closed. An OPA that cannot reach OpenFGA must not return an empty filter
# set, because Trino reads that as "no filtering required".
test_unreachable_openfga_hides_every_row if {
	trino.row_filters == {{"expression": "false"}} with input as select_orders
		with data.rowfilters.filters as [region_filter]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as {"status_code": 503, "body": {}}
}

test_missing_store_id_hides_every_row if {
	trino.row_filters == {{"expression": "false"}} with input as select_orders
		with data.rowfilters.filters as [region_filter]
		with data.rowfilters.openfga_store_id as ""
		with http.send as openfga_ok(["region:emea"])
}

# Objects of an unrelated type must not leak into the clause.
test_other_types_are_ignored if {
	trino.row_filters == {{"expression": `"region" IN ('emea')`}} with input as select_orders
		with data.rowfilters.filters as [region_filter]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["region:emea", "tenant:acme"])
}

test_unfiltered_table_returns_no_filters if {
	other := json.patch(select_orders, [{
		"op": "replace",
		"path": "/action/resource/table/tableName",
		"value": "returns",
	}])
	trino.row_filters == set() with input as other
		with data.rowfilters.filters as [region_filter]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["region:emea"])
}

test_no_configured_filters_returns_no_filters if {
	trino.row_filters == set() with input as select_orders
		with data.rowfilters.filters as []
		with data.rowfilters.openfga_store_id as "store1"
}

test_admin_reads_unfiltered if {
	trino.row_filters == set() with input as select_orders
		with data.rowfilters.filters as [region_filter]
		with data.rowfilters.openfga_store_id as "store1"
		with data.configuration.trino_admin_users as ["alice"]
		with http.send as openfga_ok([])
}

# A quote in an object id must not terminate the SQL string literal.
test_quotes_in_values_are_escaped if {
	trino.row_filters == {{"expression": `"region" IN ('o''brien')`}} with input as select_orders
		with data.rowfilters.filters as [region_filter]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["region:o'brien"])
}

test_numeric_filter_emits_bare_literals if {
	numeric := json.patch(region_filter, [
		{"op": "replace", "path": "/column", "value": "tenant_id"},
		{"op": "replace", "path": "/type", "value": "tenant"},
		{"op": "replace", "path": "/numeric", "value": true},
	])
	trino.row_filters == {{"expression": `"tenant_id" IN (17, 42)`}} with input as select_orders
		with data.rowfilters.filters as [numeric]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["tenant:42", "tenant:17"])
}

# An unquoted literal is spliced straight into the query, so anything that is
# not a number is dropped instead.
test_numeric_filter_drops_non_numeric_values if {
	numeric := json.patch(region_filter, [
		{"op": "replace", "path": "/column", "value": "tenant_id"},
		{"op": "replace", "path": "/type", "value": "tenant"},
		{"op": "replace", "path": "/numeric", "value": true},
	])
	trino.row_filters == {{"expression": `"tenant_id" IN (42)`}} with input as select_orders
		with data.rowfilters.filters as [numeric]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["tenant:42", "tenant:1 OR 1=1"])
}

# Two filters on one table are both returned; Trino ANDs them together.
test_multiple_filters_on_one_table if {
	tenant := json.patch(region_filter, [
		{"op": "replace", "path": "/column", "value": "tenant"},
		{"op": "replace", "path": "/type", "value": "tenant"},
	])
	count(trino.row_filters) == 2 with input as select_orders
		with data.rowfilters.filters as [region_filter, tenant]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["region:emea", "tenant:acme"])
}
