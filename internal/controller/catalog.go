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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// CatalogClient talks to LakeKeeper's management API.
type CatalogClient interface {
	Bootstrap(ctx context.Context, namespace, service string, port int32) error
	EnsureWarehouse(ctx context.Context, namespace, service string, port int32, req WarehouseRequest) error
}

// WarehouseRequest is a LakeKeeper create-warehouse payload.
type WarehouseRequest struct {
	Name            string
	Bucket          string
	Region          string
	KeyPrefix       string
	Endpoint        string
	PathStyleAccess bool
	Flavor          string
	STSEnabled      bool
	STSRoleARN      string
	AccessKeyID     string
	SecretAccessKey string
}

type proxyCatalogClient struct {
	restClient rest.Interface
}

// NewProxyCatalogClient reaches LakeKeeper through the Kubernetes API service proxy.
func NewProxyCatalogClient(cfg *rest.Config) (CatalogClient, error) {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &proxyCatalogClient{restClient: cs.CoreV1().RESTClient()}, nil
}

func (c *proxyCatalogClient) Bootstrap(ctx context.Context, namespace, service string, port int32) error {
	status, body, err := c.do(ctx, http.MethodPost, namespace, service, port, "management/v1/bootstrap",
		map[string]any{"accept-terms-of-use": true})
	if err != nil {
		return err
	}
	if isSuccess(status) || isAlreadyDone(status, body) {
		return nil
	}
	return fmt.Errorf("bootstrap returned %d: %s", status, truncate(body))
}

func (c *proxyCatalogClient) EnsureWarehouse(ctx context.Context, namespace, service string, port int32, req WarehouseRequest) error {
	status, body, err := c.do(ctx, http.MethodGet, namespace, service, port, "management/v1/warehouse", nil)
	if err != nil {
		return err
	}
	if isSuccess(status) && warehouseListed(body, req.Name) {
		return nil
	}
	payload := warehouseCreateJSON(req)
	status, body, err = c.do(ctx, http.MethodPost, namespace, service, port, "management/v1/warehouse", payload)
	if err != nil {
		return err
	}
	if isSuccess(status) || isAlreadyDone(status, body) {
		return nil
	}
	return fmt.Errorf("create warehouse returned %d: %s", status, truncate(body))
}

func (c *proxyCatalogClient) do(
	ctx context.Context,
	method, namespace, service string,
	port int32,
	path string,
	payload any,
) (int, []byte, error) {
	name := service + ":" + strconv.Itoa(int(port))
	req := c.restClient.Verb(method).
		Namespace(namespace).
		Resource("services").
		Name(name).
		SubResource("proxy").
		Suffix(path).
		SetHeader("Content-Type", "application/json")
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		req = req.Body(raw)
	}
	result := req.Do(ctx)
	var statusCode int
	result.StatusCode(&statusCode)
	raw, err := result.Raw()
	if err != nil && statusCode == 0 {
		return 0, nil, err
	}
	if raw == nil && err != nil {
		raw = []byte(err.Error())
	}
	return statusCode, raw, nil
}

func warehouseCreateJSON(req WarehouseRequest) map[string]any {
	profile := map[string]any{
		"type":        "s3",
		"bucket":      req.Bucket,
		"region":      req.Region,
		"flavor":      req.Flavor,
		"sts-enabled": req.STSEnabled,
	}
	if req.KeyPrefix != "" {
		profile["key-prefix"] = req.KeyPrefix
	}
	if req.Endpoint != "" {
		profile["endpoint"] = req.Endpoint
	}
	if req.PathStyleAccess {
		profile["path-style-access"] = true
	}
	if req.STSRoleARN != "" {
		profile["sts-role-arn"] = req.STSRoleARN
	}
	return map[string]any{
		"warehouse-name":  req.Name,
		"storage-profile": profile,
		"storage-credential": map[string]any{
			"type":                  "s3",
			"credential-type":       "access-key",
			"aws-access-key-id":     req.AccessKeyID,
			"aws-secret-access-key": req.SecretAccessKey,
		},
	}
}

func warehouseListed(body []byte, name string) bool {
	var parsed struct {
		Warehouses []struct {
			Name          string `json:"name"`
			WarehouseName string `json:"warehouse-name"`
		} `json:"warehouses"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return bytes.Contains(body, []byte(`"`+name+`"`))
	}
	for _, w := range parsed.Warehouses {
		if w.Name == name || w.WarehouseName == name {
			return true
		}
	}
	return false
}

func isSuccess(code int) bool {
	return code >= 200 && code < 300
}

func isAlreadyDone(code int, body []byte) bool {
	if code == http.StatusConflict {
		return true
	}
	s := strings.ToLower(string(body))
	return strings.Contains(s, "already") && (code == http.StatusBadRequest || code == http.StatusUnprocessableEntity)
}

func truncate(b []byte) string {
	const max = 512
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
