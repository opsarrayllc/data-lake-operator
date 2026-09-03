package controller

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"

	dataplatformv1alpha1 "github.com/opsarrayllc/data-platform-operator/api/v1alpha1"
)

// TestSamplesDecodeStrictly guards the samples against field names the API does
// not have. The API server prunes unknown fields rather than rejecting them, so
// a typo in a sample would apply cleanly and then silently do nothing.
func TestSamplesDecodeStrictly(t *testing.T) {
	samples, err := filepath.Glob("../../config/samples/dataplatform_v1alpha1_*.yaml")
	if err != nil {
		t.Fatalf("glob samples: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("no samples found")
	}

	for _, sample := range samples {
		t.Run(filepath.Base(sample), func(t *testing.T) {
			raw, err := os.ReadFile(sample)
			if err != nil {
				t.Fatalf("read sample: %v", err)
			}
			dp := &dataplatformv1alpha1.DataPlatform{}
			if err := yaml.UnmarshalStrict(raw, dp); err != nil {
				t.Fatalf("decode sample: %v", err)
			}
			if dp.Name == "" {
				t.Error("sample has no name")
			}
		})
	}
}
