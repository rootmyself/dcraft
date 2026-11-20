package core

// Section groups features in the TUI navigation. It is declared by the feature
// itself so the TUI never hard-codes feature names.
type Section string

const (
	// SectionInspect groups read-only investigation screens (조회).
	SectionInspect Section = "inspect"
	// SectionOperate groups stack lifecycle actions (운영).
	SectionOperate Section = "operate"
	// SectionData groups backup and restore actions (데이터).
	SectionData Section = "data"
	// SectionConfig groups configuration and installation screens (설정).
	SectionConfig Section = "config"
)

// sectionOrder is the fixed navigation order. A section missing from it is not
// a valid section.
var sectionOrder = []Section{SectionInspect, SectionOperate, SectionData, SectionConfig}

// Sections returns the navigation order. It is a copy, so a caller cannot
// reorder the contract for everyone else.
func Sections() []Section {
	out := make([]Section, len(sectionOrder))
	copy(out, sectionOrder)
	return out
}

// KoreanLabel returns the nav heading text. The empty section has no heading,
// so a descriptor that forgot to declare one cannot render as a valid group.
func (s Section) KoreanLabel() string {
	switch s {
	case SectionInspect:
		return "조회"
	case SectionOperate:
		return "운영"
	case SectionData:
		return "데이터"
	case SectionConfig:
		return "설정"
	default:
		return ""
	}
}

func validSection(s Section) bool {
	for _, known := range sectionOrder {
		if s == known {
			return true
		}
	}
	return false
}

// SectionGroup is one navigation heading and its features, in registry order.
type SectionGroup struct {
	Section  Section
	Features []Feature
}

// BySection groups registered features by their declared section. Group order
// is Sections(); within a group, registration order is preserved. Sections
// with no features are omitted, so a feature can be added or removed without
// editing the TUI menu.
func (r *Registry) BySection() []SectionGroup {
	groups := make([]SectionGroup, 0, len(sectionOrder))
	for _, section := range sectionOrder {
		var members []Feature
		for _, f := range r.features {
			if f.Descriptor().Section == section {
				members = append(members, f)
			}
		}
		if len(members) == 0 {
			continue
		}
		groups = append(groups, SectionGroup{Section: section, Features: members})
	}
	return groups
}
