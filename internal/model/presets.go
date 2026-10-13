package model

// SetupComponents returns the managed components of the single setup, "Dev
// Stack + Permissions", in install order. Every install and sync resolves to
// this list; there is no other component selection.
func SetupComponents() []ComponentID {
	return []ComponentID{
		ComponentEngram,
		ComponentSkills,
		ComponentPermission,
		ComponentPersona,
	}
}
