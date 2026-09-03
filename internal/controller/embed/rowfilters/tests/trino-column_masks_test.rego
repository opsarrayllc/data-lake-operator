# Tests for the operator's Trino column access.

package trino_test

import data.trino

ssn_access := {
	"catalog": "lakekeeper",
	"schema": "sales",
	"table": "orders",
	"column": "ssn",
	"mask": "NULL",
	"type": "column",
	"relation": "viewer",
	"object": "lakekeeper.sales.orders.ssn",
}

batch_orders := {
	"context": {"identity": {"user": "alice"}},
	"action": {
		"operation": "GetColumnMask",
		"filterResources": [
			{"column": {
				"catalogName": "lakekeeper",
				"schemaName": "sales",
				"tableName": "orders",
				"columnName": "amount",
			}},
			{"column": {
				"catalogName": "lakekeeper",
				"schemaName": "sales",
				"tableName": "orders",
				"columnName": "ssn",
			}},
		],
	},
}

openfga_ok(objects) := {"status_code": 200, "body": {"objects": objects}}

masked_ssn := {{
	"index": 1,
	"viewExpression": {"expression": "NULL"},
}}

test_missing_grant_masks_the_column if {
	trino.batch_column_masks == masked_ssn with input as batch_orders
		with data.rowfilters.column_access as [ssn_access]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok([])
}

test_grant_leaves_the_column_unmasked if {
	trino.batch_column_masks == set() with input as batch_orders
		with data.rowfilters.column_access as [ssn_access]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["column:lakekeeper.sales.orders.ssn"])
}

# Fail closed. An OPA that cannot reach OpenFGA must not return an empty mask
# set, because Trino reads that as "show the real values".
test_unreachable_openfga_masks_the_column if {
	trino.batch_column_masks == masked_ssn with input as batch_orders
		with data.rowfilters.column_access as [ssn_access]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as {"status_code": 503, "body": {}}
}

test_missing_store_id_masks_the_column if {
	trino.batch_column_masks == masked_ssn with input as batch_orders
		with data.rowfilters.column_access as [ssn_access]
		with data.rowfilters.openfga_store_id as ""
		with http.send as openfga_ok(["column:lakekeeper.sales.orders.ssn"])
}

test_unrestricted_column_is_not_masked if {
	# amount has no columnAccess entry, so even an empty grant list leaves it
	# visible. Only ssn is masked.
	trino.batch_column_masks == masked_ssn with input as batch_orders
		with data.rowfilters.column_access as [ssn_access]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok([])
}

test_no_configured_access_returns_no_masks if {
	trino.batch_column_masks == set() with input as batch_orders
		with data.rowfilters.column_access as []
		with data.rowfilters.openfga_store_id as "store1"
}

test_admin_sees_columns_unmasked if {
	trino.batch_column_masks == set() with input as batch_orders
		with data.rowfilters.column_access as [ssn_access]
		with data.rowfilters.openfga_store_id as "store1"
		with data.configuration.trino_admin_users as ["alice"]
		with http.send as openfga_ok([])
}

test_custom_mask_expression_is_used if {
	redact := json.patch(ssn_access, [{"op": "replace", "path": "/mask", "value": "'***'"}])
	trino.batch_column_masks == {{
		"index": 1,
		"viewExpression": {"expression": "'***'"},
	}} with input as batch_orders
		with data.rowfilters.column_access as [redact]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok([])
}

# A short object id lets one grant cover the same logical column on many tables.
test_shared_object_id_is_honored if {
	shared := json.patch(ssn_access, [{"op": "replace", "path": "/object", "value": "ssn"}])
	trino.batch_column_masks == set() with input as batch_orders
		with data.rowfilters.column_access as [shared]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["column:ssn"])
}

# A grant for a different object must not unmask this column.
test_other_objects_do_not_unmask if {
	trino.batch_column_masks == masked_ssn with input as batch_orders
		with data.rowfilters.column_access as [ssn_access]
		with data.rowfilters.openfga_store_id as "store1"
		with http.send as openfga_ok(["column:lakekeeper.sales.orders.email"])
}
