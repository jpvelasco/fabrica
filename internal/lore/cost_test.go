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
	if len(res) != 7 {
		t.Fatalf("len = %d, want 7 (instance + volume + S3 bucket + 4 DynamoDB tables)", len(res))
	}
	if res[2].TypeName != cloud.TypeAWSS3Bucket {
		t.Errorf("third resource TypeName = %q, want S3 bucket", res[2].TypeName)
	}
	if res[2].Name != "fabrica-lore-store" {
		t.Errorf("S3 bucket name = %q, want fabrica-lore-store", res[2].Name)
	}
	wantTables := []string{
		"fabrica-lore-store-fragments",
		"fabrica-lore-store-metadata",
		"fabrica-lore-store-mutable",
		"fabrica-lore-store-locks",
	}
	for i, w := range wantTables {
		r := res[3+i]
		if r.TypeName != cloud.TypeAWSDynamoDBTable {
			t.Errorf("resource %d TypeName = %q, want DynamoDB table", 3+i, r.TypeName)
		}
		if r.Name != w {
			t.Errorf("table %d name = %q, want %q", 3+i, r.Name, w)
		}
	}
}

func TestCostResourcesS3StoreCustomBucket(t *testing.T) {
	res := CostResources(config.LoreConfig{
		StoreBackend: "s3",
		StoreBucket:  "my-custom-bucket",
	})
	if len(res) != 7 {
		t.Fatalf("len = %d, want 7", len(res))
	}
	if res[2].Name != "my-custom-bucket" {
		t.Errorf("S3 bucket name = %q, want my-custom-bucket", res[2].Name)
	}
	if res[6].Name != "my-custom-bucket-locks" {
		t.Errorf("locks table name = %q, want my-custom-bucket-locks", res[6].Name)
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
		if r.TypeName == cloud.TypeAWSDynamoDBTable {
			t.Error("DynamoDB tables should not be in cost resources for local store")
		}
	}
}
