package graph

import "slices"

type ManagedCompilerOptions struct {
	CompilerContractVersion, CompilerBuild string
	DependencyContractID, DependencyBuild  string
	EnvironmentCompatibility               []string
	Registry                               *RegistryGeneration
	NoExecutionRegistryDigest              string
	Locale                                 string
	ExecutionExtensions                    []string
}

type CompiledPresentation struct{ Title, Description string }
type CompiledProcesses struct{ PreProcess, PostProcess string }
type ResolvedInstruction struct {
	Kind, Text, Label string
	HasLabel          bool
	Reference         ManagedReference
}

// StructuralPlan is a pure immutable compiler result. No native callable or
// resource adapter is executed during compilation.
type StructuralPlan struct {
	index         *GraphIndex
	bindings      map[string]ResolvedBinding
	contracts     map[string]PublicationContract
	manifest      *ExecutionManifest
	locale        string
	presentations map[string]CompiledPresentation
	instructions  map[string][]TemplatePart
	processes     map[string]CompiledProcesses
}

func CompileManaged(definition *GraphDefinition, o ManagedCompilerOptions) (*StructuralPlan, error) {
	if definition == nil || !definition.valid || len(o.ExecutionExtensions) != 0 {
		return nil, invalid("Validated definition required; executable extensions unsupported")
	}
	locale, err := NormalizeLocale(o.Locale)
	if err != nil {
		return nil, err
	}
	registryDigest := o.NoExecutionRegistryDigest
	bindings := map[string]ResolvedBinding{}
	contracts := map[string]PublicationContract{}
	if definition.mode == "runtime" {
		if o.Registry == nil || o.NoExecutionRegistryDigest != "" {
			return nil, invalid("Runtime compilation requires an exact registry generation")
		}
		registryDigest = o.Registry.digest
		for _, node := range definition.Nodes() {
			if node.kind != Tool {
				continue
			}
			binding, ok := node.Binding()
			if !ok {
				return nil, invalid("Runtime Tool requires binding")
			}
			assertion, _ := node.InputContract()
			resolved, err := o.Registry.Resolve(binding, assertion)
			if err != nil {
				return nil, err
			}
			bindings[node.id], contracts[node.id] = resolved, resolved.publication
		}
	} else if !validDigest(registryDigest) {
		return nil, invalid("Disclosure compilation requires an explicit no-execution registry digest")
	}
	index, err := BuildGraphIndex(definition, contracts)
	if err != nil {
		return nil, err
	}
	entries := []any{}
	for _, id := range index.preorder {
		if binding, ok := bindings[id]; ok {
			entries = append(entries, binding.Entry())
		}
	}
	manifest, err := NewExecutionManifest(ExecutionManifestOptions{
		CompilerContractVersion: o.CompilerContractVersion, CompilerBuild: o.CompilerBuild,
		RegistryDigest: registryDigest, BindingEntries: entries,
		DependencyContractID: o.DependencyContractID, DependencyBuild: o.DependencyBuild,
		EnvironmentCompatibility: o.EnvironmentCompatibility,
	})
	if err != nil {
		return nil, err
	}
	plan := &StructuralPlan{index: index, bindings: bindings, contracts: contracts, manifest: manifest,
		locale: locale, presentations: map[string]CompiledPresentation{},
		instructions: map[string][]TemplatePart{}, processes: map[string]CompiledProcesses{}}
	for _, id := range index.preorder {
		node := definition.nodes[id]
		presentation := CompiledPresentation{node.name, node.description}
		localized, ok, err := index.Presentation(id, locale)
		if err != nil {
			return nil, err
		}
		if ok {
			if localized.HasTitle {
				presentation.Title = localized.Title
			}
			if localized.HasDescription {
				presentation.Description = localized.Description
			}
		}
		plan.presentations[id] = presentation
		if node.kind != Tool {
			parts := node.instructions.Parts()
			if !node.instructions.template {
				parts = []TemplatePart{{Kind: "Text", Text: node.instructions.text}}
			}
			plan.instructions[id] = parts
		}
		if node.kind == Role {
			plan.processes[id] = CompiledProcesses{node.members.pre, node.members.post}
		}
	}
	return plan, nil
}

func (p *StructuralPlan) IsCompiled() bool             { return p != nil && p.index != nil && p.manifest != nil }
func (p *StructuralPlan) Index() *GraphIndex           { return p.index }
func (p *StructuralPlan) Definition() GraphDefinition  { return p.index.Definition() }
func (p *StructuralPlan) Manifest() *ExecutionManifest { return p.manifest }
func (p *StructuralPlan) SelectedLocale() string       { return p.locale }
func (p *StructuralPlan) Binding(id string) (ResolvedBinding, bool) {
	r, ok := p.bindings[id]
	return r, ok
}
func (p *StructuralPlan) Presentation(id string) (CompiledPresentation, bool) {
	v, ok := p.presentations[id]
	return v, ok
}
func (p *StructuralPlan) Instructions(id string) []TemplatePart {
	return slices.Clone(p.instructions[id])
}
func (p *StructuralPlan) Processes(id string) (CompiledProcesses, bool) {
	v, ok := p.processes[id]
	return v, ok
}
func (p *StructuralPlan) ResolveInstructions(id, generationUUID string) ([]ResolvedInstruction, error) {
	if _, err := NewManagedReference(p.index.definition.graphID, generationUUID, id); err != nil {
		return nil, err
	}
	parts, ok := p.instructions[id]
	if !ok {
		return nil, invalid("Node has no instruction material")
	}
	result := make([]ResolvedInstruction, 0, len(parts))
	for _, part := range parts {
		resolved := ResolvedInstruction{Kind: part.Kind, Text: part.Text, Label: part.Label, HasLabel: part.HasLabel}
		if part.Kind == "NodeRef" {
			ref, err := NewManagedReference(p.index.definition.graphID, generationUUID, part.TargetID)
			if err != nil {
				return nil, err
			}
			resolved.Reference = ref
		}
		result = append(result, resolved)
	}
	return result, nil
}
