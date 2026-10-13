package model

import (
	"slices"
	"testing"
)

func TestSetupComponentsIsDevStackPlusPermissions(t *testing.T) {
	want := []ComponentID{ComponentEngram, ComponentSkills, ComponentPermission, ComponentPersona}
	if got := SetupComponents(); !slices.Equal(got, want) {
		t.Fatalf("SetupComponents() = %v, want %v", got, want)
	}
}

func TestSetupComponentsReturnsAFreshSlice(t *testing.T) {
	first := SetupComponents()
	first[0] = ComponentPersona
	if got := SetupComponents(); got[0] != ComponentEngram {
		t.Fatalf("SetupComponents() shares its backing array: %v", got)
	}
}
