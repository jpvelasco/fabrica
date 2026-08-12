package lore

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestCostResourcesDefaults(t *testing.T) {
	res := CostResources(config.LoreConfig{})
	if len(res) != 2 {
		t.Fatalf("len = %d, want 2", len(res))
	}
	if res[0].TypeName != cloud.TypeAWSEC2Instance || res[0].Name != "m5.xlarge" {
		t.Errorf("instance = %+v", res[0])
	}
	if res[1].TypeName != cloud.TypeAWSEC2Volume || res[1].Name != "gp3-500GiB" {
		t.Errorf("volume = %+v", res[1])
	}
}

func TestCostResourcesExplicit(t *testing.T) {
	res := CostResources(config.LoreConfig{
		InstanceType: "m5.2xlarge",
		VolumeSize:   1000,
	})
	if res[0].Name != "m5.2xlarge" {
		t.Errorf("instance = %q", res[0].Name)
	}
	if res[1].Name != "gp3-1000GiB" {
		t.Errorf("volume = %q", res[1].Name)
	}
}

func TestCostResourcesS3Store(t *testing.T) {
	res := CostResources(config.LoreConfig{
		StoreBackend: "s3",
	})
	if len(res) != 3 {
		t.Fatalf("len = %d, want 3 (instance + volume + S3 bucket)", len(res))
	}
	if res[2].TypeName != cloud.TypeAWSS3Bucket {
		t.Errorf("third resource TypeName = %q, want S3 bucket", res[2].TypeName)
	}
	if res[2].Name != "fabrica-lore-store" {
		t.Errorf("S3 bucket name = %q, want fabrica-lore-store", res[2].Name)
	}
}

func TestCostResourcesS3StoreCustomBucket(t *testing.T) {
	res := CostResources(config.LoreConfig{
		StoreBackend: "s3",
		StoreBucket:  "my-custom-bucket",
	})
	if len(res) != 3 {
		t.Fatalf("len = %d, want 3", len(res))
	}
	if res[2].Name != "my-custom-bucket" {
		t.Errorf("S3 bucket name = %q, want my-custom-bucket", res[2].Name)
	}
}

func TestCostResourcesLocalStoreNoBucket(t *testing.T) {
	res := CostResources(config.LoreConfig{
		StoreBackend: "local",
	})
	if len(res) != 2 {
		t.Fatalf("len = %d, want 2 (no S3 bucket for local)", len(res))
	}
	for _, r := range res {
		if r.TypeName == cloud.TypeAWSS3Bucket {
			t.Error("S3 bucket should not be in cost resources for local store")
		}
	}
}
