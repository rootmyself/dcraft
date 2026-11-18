package update

import (
	"sort"

	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/state"
)

// recordedSecretRefs loads the project's saved runtime secret references and
// converts them to the neutral record shape. Only references are copied: the
// resolved values are never read or stored here.
func recordedSecretRefs(projectDir string) ([]state.SecretReference, error) {
	catalog, err := secretref.Load(projectDir)
	if err != nil {
		return nil, err
	}
	refs := make([]state.SecretReference, 0, len(catalog.References))
	for _, ref := range catalog.References {
		refs = append(refs, state.SecretReference{
			Service:  ref.Service,
			Phase:    string(ref.Phase),
			Delivery: string(ref.Delivery),
			Source:   string(ref.Source),
			Name:     ref.Ref,
			Target:   ref.EffectiveTarget(),
		})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Service != refs[j].Service {
			return refs[i].Service < refs[j].Service
		}
		if refs[i].Delivery != refs[j].Delivery {
			return refs[i].Delivery < refs[j].Delivery
		}
		return refs[i].Target < refs[j].Target
	})
	return refs, nil
}
