package controller

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"k8s.io/utils/ptr"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

func rowFilter(schema, table, column, fgaType, relation string) dataplatformv1alpha1.RowFilterSpec {
	return dataplatformv1alpha1.RowFilterSpec{
		Schema: schema,
		Table:  table,
		Column: column,
		OpenFGA: dataplatformv1alpha1.RowFilterSubjectSpec{
			Type:     fgaType,
			Relation: relation,
		},
	}
}

// TestRowFiltersJSONResolvesDefaults checks that the policy receives a fully
// resolved filter. The Rego reads these fields directly, so an absent catalog
// or relation would silently stop a filter from ever matching.
func TestRowFiltersJSONResolvesDefaults(t *testing.T) {
	filters := []dataplatformv1alpha1.RowFilterSpec{
		rowFilter("sales", "orders", "region", "region", ""),
		{
			Catalog: "warehouse",
			Schema:  "sales",
			Table:   "invoices",
			Column:  "tenant_id",
			OpenFGA: dataplatformv1alpha1.RowFilterSubjectSpec{Type: "tenant", Relation: "reader"},
			Numeric: ptr.To(true),
		},
	}

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(rowFiltersJSON(filters)), &parsed); err != nil {
		t.Fatalf("row filters are not valid JSON: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("got %d filters, want 2", len(parsed))
	}

	want := map[string]any{
		"catalog":  dataplatformv1alpha1.DefaultTrinoCatalog,
		"schema":   "sales",
		"table":    "orders",
		"column":   "region",
		"type":     "region",
		"relation": dataplatformv1alpha1.DefaultRowFilterRelation,
		"numeric":  false,
	}
	if !reflect.DeepEqual(parsed[0], want) {
		t.Errorf("defaulted filter = %v, want %v", parsed[0], want)
	}
	if parsed[1]["catalog"] != "warehouse" || parsed[1]["relation"] != "reader" || parsed[1]["numeric"] != true {
		t.Errorf("explicit filter lost its values: %v", parsed[1])
	}
}

// TestRowFilterAuthzTypesMergeRelations checks that filters sharing an OpenFGA
// type collapse into one type definition. Two definitions for one type is not a
// valid authorization model.
func TestAccessAuthzTypesMergeRelations(t *testing.T) {
	types := accessAuthzTypes(dataplatformv1alpha1.AuthzSpec{
		RowFilters: []dataplatformv1alpha1.RowFilterSpec{
			rowFilter("sales", "orders", "region", "region", "viewer"),
			rowFilter("sales", "invoices", "region", "region", "auditor"),
			rowFilter("sales", "invoices", "region", "region", "viewer"),
			rowFilter("hr", "staff", "office", "tenant", ""),
		},
		ColumnAccess: []dataplatformv1alpha1.ColumnAccessSpec{{
			Schema:  "sales",
			Table:   "orders",
			Column:  "ssn",
			OpenFGA: dataplatformv1alpha1.ColumnAccessSubjectSpec{Type: "column", Relation: "viewer"},
		}},
	})

	want := []AuthzType{
		{Name: "column", Relations: []string{"viewer"}},
		{Name: "region", Relations: []string{"auditor", "viewer"}},
		{Name: "tenant", Relations: []string{dataplatformv1alpha1.DefaultRowFilterRelation}},
	}
	if !reflect.DeepEqual(types, want) {
		t.Errorf("authz types = %v, want %v", types, want)
	}
}

func TestColumnAccessJSONResolvesDefaults(t *testing.T) {
	access := []dataplatformv1alpha1.ColumnAccessSpec{
		{
			Schema:  "sales",
			Table:   "orders",
			Column:  "ssn",
			OpenFGA: dataplatformv1alpha1.ColumnAccessSubjectSpec{Type: "column"},
		},
		{
			Catalog: "warehouse",
			Schema:  "sales",
			Table:   "invoices",
			Column:  "email",
			Mask:    "'***'",
			OpenFGA: dataplatformv1alpha1.ColumnAccessSubjectSpec{Type: "pii", Relation: "reader", Object: "email"},
		},
	}

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(columnAccessJSON(access)), &parsed); err != nil {
		t.Fatalf("column access is not valid JSON: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("got %d entries, want 2", len(parsed))
	}

	want := map[string]any{
		"catalog":  dataplatformv1alpha1.DefaultTrinoCatalog,
		"schema":   "sales",
		"table":    "orders",
		"column":   "ssn",
		"mask":     dataplatformv1alpha1.DefaultColumnMask,
		"type":     "column",
		"relation": dataplatformv1alpha1.DefaultRowFilterRelation,
		"object":   "lakekeeper.sales.orders.ssn",
	}
	if !reflect.DeepEqual(parsed[0], want) {
		t.Errorf("defaulted access = %v, want %v", parsed[0], want)
	}
	if parsed[1]["object"] != "email" || parsed[1]["mask"] != "'***'" || parsed[1]["relation"] != "reader" {
		t.Errorf("explicit access lost its values: %v", parsed[1])
	}
}

// TestAuthzModelGrantsUsersAndGroups checks that a row-filter relation can be
// assigned to a user directly or through a group.
func TestAuthzModelGrantsUsersAndGroups(t *testing.T) {
	model := authzModelJSON([]AuthzType{{Name: "region", Relations: []string{"viewer"}}})

	if model.SchemaVersion != "1.1" {
		t.Errorf("schema version = %q, want 1.1", model.SchemaVersion)
	}

	found := map[string]bool{}
	for _, definition := range model.TypeDefinitions {
		found[definition.Type] = true
		if definition.Type != "region" {
			continue
		}
		assignable := definition.Metadata.Relations["viewer"].DirectlyRelatedUserTypes
		want := []authzRelationReference{
			{Type: "user"},
			{Type: "group", Relation: "member"},
		}
		if !reflect.DeepEqual(assignable, want) {
			t.Errorf("viewer is assignable to %v, want %v", assignable, want)
		}
	}
	for _, required := range []string{"user", "group", "region"} {
		if !found[required] {
			t.Errorf("model is missing type %q", required)
		}
	}

	// The relation must serialise as OpenFGA's directly-assignable userset.
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	if !strings.Contains(string(raw), `"relations":{"viewer":{"this":{}}}`) {
		t.Errorf("viewer is not a directly assignable relation:\n%s", raw)
	}
}

// TestAuthzModelMatches guards against rewriting an unchanged model. OpenFGA
// models are append-only, so a false negative grows a new version on every
// reconcile.
func TestAuthzModelMatches(t *testing.T) {
	desired := authzModelJSON([]AuthzType{{Name: "region", Relations: []string{"viewer"}}})

	// Round-trip through JSON to mimic a model read back from OpenFGA rather
	// than compared against the value that built it.
	raw, err := json.Marshal(desired)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	current := &authzModel{}
	if err := json.Unmarshal(raw, current); err != nil {
		t.Fatalf("unmarshal model: %v", err)
	}

	if !authzModelMatches(current, desired) {
		t.Error("an unchanged model was reported as changed")
	}
	if authzModelMatches(nil, desired) {
		t.Error("a store with no model was reported as up to date")
	}

	added := authzModelJSON([]AuthzType{
		{Name: "region", Relations: []string{"viewer"}},
		{Name: "tenant", Relations: []string{"viewer"}},
	})
	if authzModelMatches(current, added) {
		t.Error("a model gaining a type was reported as up to date")
	}

	relation := authzModelJSON([]AuthzType{{Name: "region", Relations: []string{"auditor"}}})
	if authzModelMatches(current, relation) {
		t.Error("a model gaining a relation was reported as up to date")
	}
}

// TestAccessControlRowFilterURI checks that Trino is only pointed at the row
// filter endpoint when a filter exists, since an endpoint that returns nothing
// costs a request per table scan.
func TestAccessControlRowFilterURI(t *testing.T) {
	const opaURL = "http://opa.openfga.svc:8181"
	const property = "opa.policy.row-filters-uri=http://opa.openfga.svc:8181/v1/data/trino/row_filters"

	if got := trinoAccessControlProperties(opaURL, false, false); strings.Contains(got, "row-filters-uri") {
		t.Errorf("row filter URI configured without any filter:\n%s", got)
	}
	got := trinoAccessControlProperties(opaURL, true, false)
	if !strings.Contains(got, property) {
		t.Errorf("access control properties missing %q:\n%s", property, got)
	}
	if got := trinoAccessControlProperties("", true, false); got != "" {
		t.Errorf("access control configured without OPA: %q", got)
	}
}

func TestAccessControlColumnMaskURI(t *testing.T) {
	const opaURL = "http://opa.openfga.svc:8181"
	const property = "opa.policy.batch-column-masking-uri=http://opa.openfga.svc:8181/v1/data/trino/batch_column_masks"

	if got := trinoAccessControlProperties(opaURL, false, false); strings.Contains(got, "column-masking") {
		t.Errorf("column mask URI configured without any restriction:\n%s", got)
	}
	got := trinoAccessControlProperties(opaURL, false, true)
	if !strings.Contains(got, property) {
		t.Errorf("access control properties missing %q:\n%s", property, got)
	}
	if strings.Contains(got, "row-filters-uri") {
		t.Errorf("row filter URI configured without any filter:\n%s", got)
	}
}
