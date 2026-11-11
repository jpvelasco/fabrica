package stateutil_test

import (
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"testing"
)

func TestResourceByTypeNilModule(t *testing.T) {
	if _, ok := stateutil.ResourceByType(nil, "AWS::EC2::Instance"); ok {
		t.Fatal("nil module returned a resource")
	}
}
