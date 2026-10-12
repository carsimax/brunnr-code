package catalog

import "github.com/carsimax/brunnr-code/internal/model"

type Component struct {
	ID          model.ComponentID
	Name        string
	Description string
}

var mvpComponents = []Component{
	{ID: model.ComponentEngram, Name: "Engram", Description: "Persistent cross-session memory"},
	{ID: model.ComponentSkills, Name: "Skills", Description: "Curated coding skill library"},
	{ID: model.ComponentPersona, Name: "Persona", Description: "Managed agent behavior and conversation tone"},
	{ID: model.ComponentPermission, Name: "Permissions", Description: "Security-first defaults and guardrails"},
}

func MVPComponents() []Component {
	components := make([]Component, len(mvpComponents))
	copy(components, mvpComponents)
	return components
}

// SelectableComponents returns the components a user may toggle in the custom
// preset. Mandatory components (the managed persona) are always installed and
// are never offered as a choice.
func SelectableComponents() []Component {
	components := make([]Component, 0, len(mvpComponents))
	for _, component := range mvpComponents {
		if !model.IsMandatoryComponent(component.ID) {
			components = append(components, component)
		}
	}
	return components
}
