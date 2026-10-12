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

// TestSelectableComponentsExcludeMandatoryPersona pins that the managed persona
// is never offered as a toggle: it is always installed.
func TestSelectableComponentsExcludeMandatoryPersona(t *testing.T) {
	var got []model.ComponentID
	for _, component := range SelectableComponents() {
		got = append(got, component.ID)
	}
	want := []model.ComponentID{model.ComponentEngram, model.ComponentSkills, model.ComponentPermission}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SelectableComponents() IDs = %v, want %v", got, want)
	}
}
