/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
)

const (
	// authzUserType is the OpenFGA type a row-filter tuple names directly, as
	// in "user:alice viewer region:emea".
	authzUserType = "user"
	// authzGroupType lets tuples target a whole group, as in
	// "group:analysts#member viewer region:emea".
	authzGroupType = "group"
	// authzGroupRelation is the membership relation on authzGroupType.
	authzGroupRelation = "member"
	authzSchemaVersion = "1.1"
	// authzStorePageSize caps a single page of the store listing. OpenFGA's own
	// maximum is 100.
	authzStorePageSize = 100
)

// authzModel is an OpenFGA authorization model.
type authzModel struct {
	SchemaVersion   string                `json:"schema_version"`
	TypeDefinitions []authzTypeDefinition `json:"type_definitions"`
	Conditions      map[string]any        `json:"conditions"`
}

type authzTypeDefinition struct {
	Type      string                  `json:"type"`
	Relations map[string]authzUserset `json:"relations,omitempty"`
	Metadata  *authzTypeMetadata      `json:"metadata,omitempty"`
}

// authzUserset is a relation defined as directly assignable, spelled
// "define viewer: [user]" in OpenFGA's DSL.
type authzUserset struct {
	This map[string]any `json:"this"`
}

type authzTypeMetadata struct {
	Relations map[string]authzRelationMetadata `json:"relations"`
}

type authzRelationMetadata struct {
	DirectlyRelatedUserTypes []authzRelationReference `json:"directly_related_user_types"`
}

type authzRelationReference struct {
	Type     string `json:"type"`
	Relation string `json:"relation,omitempty"`
}

// EnsureAuthzStore creates the named OpenFGA store if it is missing and writes
// an authorization model defining req.Types. Authorization models are immutable
// and append-only in OpenFGA, so an unchanged model is left alone rather than
// rewritten on every reconcile. Returns the store id.
func (c *proxyCatalogClient) EnsureAuthzStore(
	ctx context.Context,
	namespace, service string,
	port int32,
	bearer string,
	req AuthzStoreRequest,
) (string, error) {
	storeID, err := c.findAuthzStore(ctx, namespace, service, port, bearer, req.Store)
	if err != nil {
		return "", err
	}
	if storeID == "" {
		storeID, err = c.createAuthzStore(ctx, namespace, service, port, bearer, req.Store)
		if err != nil {
			return "", err
		}
	}

	desired := authzModelJSON(req.Types)
	current, err := c.latestAuthzModel(ctx, namespace, service, port, bearer, storeID)
	if err != nil {
		return "", err
	}
	if authzModelMatches(current, desired) {
		return storeID, nil
	}

	status, body, err := c.do(ctx, http.MethodPost, namespace, service, port,
		"stores/"+storeID+"/authorization-models", bearer, desired)
	if err != nil {
		return "", err
	}
	if !isSuccess(status) {
		return "", fmt.Errorf("write authorization model returned %d: %s", status, truncate(body))
	}
	return storeID, nil
}

func (c *proxyCatalogClient) findAuthzStore(
	ctx context.Context,
	namespace, service string,
	port int32,
	bearer, name string,
) (string, error) {
	token := ""
	for {
		query := url.Values{"page_size": {fmt.Sprint(authzStorePageSize)}}
		if token != "" {
			query.Set("continuation_token", token)
		}
		status, body, err := c.do(ctx, http.MethodGet, namespace, service, port,
			"stores?"+query.Encode(), bearer, nil)
		if err != nil {
			return "", err
		}
		if !isSuccess(status) {
			return "", fmt.Errorf("list authz stores returned %d: %s", status, truncate(body))
		}
		var parsed struct {
			Stores []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				DeletedAt string `json:"deleted_at"`
			} `json:"stores"`
			ContinuationToken string `json:"continuation_token"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return "", fmt.Errorf("parse authz store list: %w", err)
		}
		for _, store := range parsed.Stores {
			if store.Name == name && store.DeletedAt == "" {
				return store.ID, nil
			}
		}
		if parsed.ContinuationToken == "" {
			return "", nil
		}
		token = parsed.ContinuationToken
	}
}

func (c *proxyCatalogClient) createAuthzStore(
	ctx context.Context,
	namespace, service string,
	port int32,
	bearer, name string,
) (string, error) {
	payload := struct {
		Name string `json:"name"`
	}{Name: name}
	status, body, err := c.do(ctx, http.MethodPost, namespace, service, port, "stores", bearer, payload)
	if err != nil {
		return "", err
	}
	if !isSuccess(status) {
		return "", fmt.Errorf("create authz store returned %d: %s", status, truncate(body))
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parse created authz store: %w", err)
	}
	if parsed.ID == "" {
		return "", fmt.Errorf("created authz store %q has no id", name)
	}
	return parsed.ID, nil
}

// latestAuthzModel returns the most recent authorization model, or nil when the
// store has none yet.
func (c *proxyCatalogClient) latestAuthzModel(
	ctx context.Context,
	namespace, service string,
	port int32,
	bearer, storeID string,
) (*authzModel, error) {
	status, body, err := c.do(ctx, http.MethodGet, namespace, service, port,
		"stores/"+storeID+"/authorization-models?page_size=1", bearer, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if !isSuccess(status) {
		return nil, fmt.Errorf("list authorization models returned %d: %s", status, truncate(body))
	}
	var parsed struct {
		AuthorizationModels []authzModel `json:"authorization_models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse authorization models: %w", err)
	}
	if len(parsed.AuthorizationModels) == 0 {
		return nil, nil
	}
	return &parsed.AuthorizationModels[0], nil
}

// authzModelJSON renders an authorization model in which every row-filter
// relation is assignable to a user directly or through a group.
func authzModelJSON(types []AuthzType) *authzModel {
	assignable := []authzRelationReference{
		{Type: authzUserType},
		{Type: authzGroupType, Relation: authzGroupRelation},
	}
	definitions := make([]authzTypeDefinition, 0, len(types)+2)
	definitions = append(definitions,
		authzTypeDefinition{Type: authzUserType},
		authzTypeJSON(authzGroupType, []string{authzGroupRelation},
			[]authzRelationReference{{Type: authzUserType}}),
	)
	for _, t := range types {
		definitions = append(definitions, authzTypeJSON(t.Name, t.Relations, assignable))
	}
	return &authzModel{
		SchemaVersion:   authzSchemaVersion,
		TypeDefinitions: definitions,
		Conditions:      map[string]any{},
	}
}

func authzTypeJSON(name string, relations []string, assignable []authzRelationReference) authzTypeDefinition {
	if len(relations) == 0 {
		return authzTypeDefinition{Type: name}
	}
	definition := authzTypeDefinition{
		Type:      name,
		Relations: make(map[string]authzUserset, len(relations)),
		Metadata: &authzTypeMetadata{
			Relations: make(map[string]authzRelationMetadata, len(relations)),
		},
	}
	for _, relation := range relations {
		definition.Relations[relation] = authzUserset{This: map[string]any{}}
		definition.Metadata.Relations[relation] = authzRelationMetadata{
			DirectlyRelatedUserTypes: assignable,
		}
	}
	return definition
}

// authzModelMatches compares a model fetched from OpenFGA against a desired one
// by the types it defines and the relations on each. The user types a relation
// accepts are fixed by authzModelJSON, so they cannot drift on their own.
func authzModelMatches(current, desired *authzModel) bool {
	if current == nil {
		return false
	}
	return maps.EqualFunc(authzModelShape(current), authzModelShape(desired), slices.Equal)
}

func authzModelShape(model *authzModel) map[string][]string {
	shape := make(map[string][]string, len(model.TypeDefinitions))
	for _, definition := range model.TypeDefinitions {
		relations := slices.Sorted(maps.Keys(definition.Relations))
		if relations == nil {
			relations = []string{}
		}
		shape[definition.Type] = relations
	}
	return shape
}
