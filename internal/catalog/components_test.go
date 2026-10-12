package catalog

import (
	"reflect"
	"testing"

	"github.com/carsimax/brunnr-code/internal/model"
)

// TestMVPComponentsMatchShippedComponentIDs keeps the catalog and the model's
// shipped-component list in sync, because state restore filters persisted
// component IDs through the model list.
func TestMVPComponentsMatchShippedComponentIDs(t *testing.T) {
	var catalogIDs []model.ComponentID
	for _, component := range MVPComponents() {
		catalogIDs = append(catalogIDs, component.ID)
	}
	if want := model.ShippedComponentIDs(); !reflect.DeepEqual(catalogIDs, want) {
		t.Fatalf("catalog component IDs = %v, want model.ShippedComponentIDs() = %v", catalogIDs, want)
	}
}
