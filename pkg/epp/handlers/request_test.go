/*
Copyright 2025 The Kubernetes Authors.

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

package handlers

import (
	"context"
	"strings"
	"testing"

	configPb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/structpb"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/metadata"
)

func TestHandleRequestHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		headers        []*configPb.HeaderValue
		wantHeaders    map[string]string
		wantFairnessID string
	}{
		{
			name: "Extracts Fairness ID and Removes Header",
			headers: []*configPb.HeaderValue{
				{Key: "x-test", Value: "val"},
				{Key: metadata.FlowFairnessIDKey, Value: "user-123"},
			},
			wantHeaders:    map[string]string{"x-test": "val"},
			wantFairnessID: "user-123",
		},
		{
			name: "Prefers RawValue over Value",
			headers: []*configPb.HeaderValue{
				{Key: metadata.FlowFairnessIDKey, RawValue: []byte("binary-id"), Value: "wrong-id"},
			},
			wantFairnessID: "binary-id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := &StreamingServer{}
			reqCtx := &RequestContext{
				Request: &Request{Headers: make(map[string]string)},
			}
			req := &extProcPb.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extProcPb.HttpHeaders{
					Headers: &configPb.HeaderMap{Headers: tc.headers},
				},
			}

			err := server.HandleRequestHeaders(context.Background(), reqCtx, req)
			assert.NoError(t, err, "HandleRequestHeaders should not return an error")

			assert.Equal(t, tc.wantFairnessID, reqCtx.FairnessID, "FairnessID should match expected value")

			if tc.wantHeaders != nil {
				for k, v := range tc.wantHeaders {
					assert.Equal(t, v, reqCtx.Request.Headers[k], "Header %q should match expected value", k)
				}
			}
		})
	}
}

func TestGenerateHeaders_Sanitization(t *testing.T) {
	server := &StreamingServer{}
	reqCtx := &RequestContext{
		TargetEndpoint: "1.2.3.4:8080",
		RequestSize:    123,
		Request: &Request{
			Headers: map[string]string{
				"x-user-data":                   "important",              // should passthrough
				metadata.ObjectiveKey:           "sensitive-objective-id", // should be stripped
				metadata.DestinationEndpointKey: "1.1.1.1:666",            // should be stripped
				"content-length":                "99999",                  // should be stripped (re-added by logic)
			},
		},
	}

	results := server.generateHeaders(context.Background(), reqCtx)

	gotHeaders := make(map[string]string)
	for _, h := range results {
		gotHeaders[h.Header.Key] = string(h.Header.RawValue)
	}

	assert.Contains(t, gotHeaders, "x-user-data")
	assert.NotContains(t, gotHeaders, metadata.ObjectiveKey)
	assert.Equal(t, "1.2.3.4:8080", gotHeaders[metadata.DestinationEndpointKey])
	assert.Equal(t, "123", gotHeaders["Content-Length"])
}

func TestGenerateRequestHeaderResponse_MergeMetadata(t *testing.T) {
	t.Parallel()

	server := &StreamingServer{}
	reqCtx := &RequestContext{
		TargetEndpoint: "1.2.3.4:8080",
		Request: &Request{
			Headers: make(map[string]string),
		},
		Response: &Response{
			DynamicMetadata: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					"existing_namespace": {
						Kind: &structpb.Value_StructValue{
							StructValue: &structpb.Struct{
								Fields: map[string]*structpb.Value{
									"existing_key": {Kind: &structpb.Value_StringValue{StringValue: "existing_value"}},
								},
							},
						},
					},
				},
			},
		},
	}

	resp := server.generateRequestHeaderResponse(context.Background(), reqCtx)

	// Check that the existing metadata is preserved
	existingNamespace, ok := resp.DynamicMetadata.Fields["existing_namespace"]
	assert.True(t, ok, "Expected existing_namespace to be in DynamicMetadata")
	existingKey, ok := existingNamespace.GetStructValue().Fields["existing_key"]
	assert.True(t, ok, "Expected existing_key to be in existing_namespace")
	assert.Equal(t, "existing_value", existingKey.GetStringValue(), "Unexpected value for existing_key")

	// Check that the new metadata is added
	endpointNamespace, ok := resp.DynamicMetadata.Fields[metadata.DestinationEndpointNamespace]
	assert.True(t, ok, "Expected DestinationEndpointNamespace to be in DynamicMetadata")
	endpointKey, ok := endpointNamespace.GetStructValue().Fields[metadata.DestinationEndpointKey]
	assert.True(t, ok, "Expected DestinationEndpointKey to be in DestinationEndpointNamespace")
	assert.Equal(t, "1.2.3.4:8080", endpointKey.GetStringValue(), "Unexpected value for DestinationEndpointKey")
}

func TestExtractWorkloadContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		headers           map[string]string
		wantWorkloadID    string
		wantCriticality   int
		checkUniqueID     bool // For cases where we generate unique IDs
	}{
		{
			name: "Valid workload context",
			headers: map[string]string{
				"x-workload-context": `{"workload_id":"fraud-detection","criticality":5}`,
			},
			wantWorkloadID:  "fraud-detection",
			wantCriticality: 5,
		},
		{
			name: "Valid workload context with medium criticality",
			headers: map[string]string{
				"x-workload-context": `{"workload_id":"chatbot-prod","criticality":3}`,
			},
			wantWorkloadID:  "chatbot-prod",
			wantCriticality: 3,
		},
		{
			name:            "Missing workload context header",
			headers:         map[string]string{},
			wantCriticality: 3,
			checkUniqueID:   true,
		},
		{
			name: "Empty workload context header",
			headers: map[string]string{
				"x-workload-context": "",
			},
			wantCriticality: 3,
			checkUniqueID:   true,
		},
		{
			name: "Invalid JSON",
			headers: map[string]string{
				"x-workload-context": `{invalid json}`,
			},
			wantCriticality: 3,
			checkUniqueID:   true,
		},
		{
			name: "Empty workload_id in JSON",
			headers: map[string]string{
				"x-workload-context": `{"workload_id":"","criticality":4}`,
			},
			wantCriticality: 4,
			checkUniqueID:   true,
		},
		{
			name: "Criticality below minimum (0)",
			headers: map[string]string{
				"x-workload-context": `{"workload_id":"test-workload","criticality":0}`,
			},
			wantWorkloadID:  "test-workload",
			wantCriticality: 1, // Clamped to min
		},
		{
			name: "Criticality below minimum (negative)",
			headers: map[string]string{
				"x-workload-context": `{"workload_id":"test-workload","criticality":-5}`,
			},
			wantWorkloadID:  "test-workload",
			wantCriticality: 1, // Clamped to min
		},
		{
			name: "Criticality above maximum",
			headers: map[string]string{
				"x-workload-context": `{"workload_id":"test-workload","criticality":10}`,
			},
			wantWorkloadID:  "test-workload",
			wantCriticality: 5, // Clamped to max
		},
		{
			name: "Criticality at minimum boundary",
			headers: map[string]string{
				"x-workload-context": `{"workload_id":"test-workload","criticality":1}`,
			},
			wantWorkloadID:  "test-workload",
			wantCriticality: 1,
		},
		{
			name: "Criticality at maximum boundary",
			headers: map[string]string{
				"x-workload-context": `{"workload_id":"test-workload","criticality":5}`,
			},
			wantWorkloadID:  "test-workload",
			wantCriticality: 5,
		},
		{
			name: "Workload ID with special characters",
			headers: map[string]string{
				"x-workload-context": `{"workload_id":"my-app_v2.0","criticality":3}`,
			},
			wantWorkloadID:  "my-app_v2.0",
			wantCriticality: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			result := extractWorkloadContext(ctx, tt.headers)

			assert.NotNil(t, result, "extractWorkloadContext should not return nil")
			assert.Equal(t, tt.wantCriticality, result.Criticality, "Criticality should match expected value")

			if tt.checkUniqueID {
				// Verify it's a unique auto-generated ID
				assert.True(t, strings.HasPrefix(result.WorkloadID, "auto-"), 
					"Auto-generated workload ID should have 'auto-' prefix, got: %s", result.WorkloadID)
				assert.Greater(t, len(result.WorkloadID), 10, 
					"Auto-generated workload ID should be sufficiently long")
			} else {
				assert.Equal(t, tt.wantWorkloadID, result.WorkloadID, "WorkloadID should match expected value")
			}
		})
	}
}

func TestExtractWorkloadContext_UniqueIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	headers := map[string]string{} // No workload context

	// Generate multiple workload contexts and verify they're unique
	ids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		result := extractWorkloadContext(ctx, headers)
		assert.False(t, ids[result.WorkloadID], "Generated workload IDs should be unique")
		ids[result.WorkloadID] = true
	}

	assert.Equal(t, 10, len(ids), "Should have generated 10 unique workload IDs")
}
