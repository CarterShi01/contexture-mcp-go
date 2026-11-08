// Package presentation supplies immutable, SDK-neutral human-facing copy.
package presentation

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func invalid(message string) error {
	return fmt.Errorf("%w: %s", foundation.ErrInvalidDeclaration, message)
}

func text(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", invalid("presentation text requires Unicode scalar values")
	}
	value = strings.TrimFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
	})
	if value == "" {
		return "", invalid("presentation text must not be empty")
	}
	return value, nil
}

func optionalText(value *string) (string, error) {
	if value == nil {
		return "", nil
	}
	return text(*value)
}

// GatewayOptions distinguishes absent titles from explicitly empty invalid titles.
type GatewayOptions struct {
	Discover, Inspect, Open, InvokeReadOnly, Invoke *string
}

// GatewayPresentation changes labels, never the five gateway machine names.
type GatewayPresentation struct {
	titles map[foundation.GatewayName]string
	valid  bool
}

// NewGatewayPresentation validates and copies explicit gateway title overrides.
func NewGatewayPresentation(options GatewayOptions) (*GatewayPresentation, error) {
	result := &GatewayPresentation{valid: true, titles: map[foundation.GatewayName]string{}}
	rows := []struct {
		name     foundation.GatewayName
		fallback string
		value    *string
	}{
		{foundation.DiscoverGatewayName, "Discover capabilities", options.Discover},
		{foundation.InspectGatewayName, "Inspect capabilities", options.Inspect},
		{foundation.OpenGatewayName, "Open capability", options.Open},
		{foundation.InvokeReadOnlyGatewayName, "Read data", options.InvokeReadOnly},
		{foundation.InvokeGatewayName, "Make a change", options.Invoke},
	}
	for _, row := range rows {
		value := row.fallback
		if row.value != nil {
			parsed, err := text(*row.value)
			if err != nil {
				return nil, err
			}
			value = parsed
		}
		result.titles[row.name] = value
	}
	return result, nil
}

// TitleFor returns the human title of one fixed Contexture gateway.
func (value *GatewayPresentation) TitleFor(name foundation.GatewayName) (string, error) {
	if value == nil || !value.valid {
		return "", invalid("constructed gateway presentation required")
	}
	title, ok := value.titles[name]
	if !ok {
		return "", invalid("unknown Contexture gateway name")
	}
	return title, nil
}

// NodeOptions is copy for one canonical ref; Description is optional.
type NodeOptions struct {
	Title       string
	Description *string
}

// NodePresentation is immutable routing copy, not instructions or authority.
type NodePresentation struct {
	title, description string
	valid              bool
}

// NewNodePresentation validates one required title and optional description.
func NewNodePresentation(options NodeOptions) (*NodePresentation, error) {
	title, err := text(options.Title)
	if err != nil {
		return nil, err
	}
	description, err := optionalText(options.Description)
	if err != nil {
		return nil, err
	}
	return &NodePresentation{title: title, description: description, valid: true}, nil
}

// Title returns the display title, never a machine name.
func (value *NodePresentation) Title() string { return value.title }

// Description returns selected copy, or empty when no override is declared.
func (value *NodePresentation) Description() string { return value.description }

// Options declares one language-neutral or selected-locale presentation.
type Options struct {
	Title, Description *string
	Gateway            *GatewayPresentation
	Nodes              map[string]*NodePresentation
}

// Presentation is a deeply immutable snapshot of explicit presentation options.
type Presentation struct {
	title, description string
	gateway            *GatewayPresentation
	nodes              map[string]*NodePresentation
	valid              bool
}

// NewPresentation validates labels and detaches the canonical-ref map.
func NewPresentation(options Options) (*Presentation, error) {
	title, err := optionalText(options.Title)
	if err != nil {
		return nil, err
	}
	description, err := optionalText(options.Description)
	if err != nil {
		return nil, err
	}
	if options.Gateway != nil && !options.Gateway.valid {
		return nil, invalid("constructed gateway presentation required")
	}
	nodes := map[string]*NodePresentation{}
	for _, ref := range keys(options.Nodes) {
		normalized, err := text(ref)
		if err != nil {
			return nil, err
		}
		node := options.Nodes[ref]
		if node == nil || !node.valid {
			return nil, invalid("constructed node presentation required")
		}
		if _, exists := nodes[normalized]; exists {
			return nil, invalid("duplicate normalized presentation ref")
		}
		nodes[normalized] = node
	}
	return &Presentation{title: title, description: description, gateway: options.Gateway, nodes: nodes, valid: true}, nil
}

// Title returns the optional product display title.
func (value *Presentation) Title() string { return value.title }

// Description returns the optional product description.
func (value *Presentation) Description() string { return value.description }

// Gateway returns immutable explicitly configured gateway titles, or nil.
func (value *Presentation) Gateway() *GatewayPresentation { return value.gateway }

// Node returns selected immutable routing copy for one exact ref.
func (value *Presentation) Node(ref string) (*NodePresentation, bool) {
	node, ok := value.nodes[ref]
	return node, ok
}

// Nodes returns a defensive copy of the complete presentation map.
func (value *Presentation) Nodes() map[string]*NodePresentation {
	result := map[string]*NodePresentation{}
	for ref, node := range value.nodes {
		result[ref] = node
	}
	return result
}

// Source is sealed to validated immutable language-neutral/catalog values.
type Source interface {
	presentationSource()
	Resolve(*string) (string, *Presentation, error)
	Variants() ([]*Presentation, error)
}

func (*Presentation) presentationSource() {}

// Resolve retains neutral copy and validates any explicitly requested locale.
func (value *Presentation) Resolve(locale *string) (string, *Presentation, error) {
	if value == nil || !value.valid {
		return "", nil, invalid("constructed presentation required")
	}
	tag := ""
	if locale != nil {
		var err error
		tag, err = NormalizeLocale(*locale)
		if err != nil {
			return "", nil, err
		}
	}
	return tag, value, nil
}

// Variants enumerates the neutral presentation without exposing mutable storage.
func (value *Presentation) Variants() ([]*Presentation, error) {
	if value == nil || !value.valid {
		return nil, invalid("constructed presentation required")
	}
	return []*Presentation{value}, nil
}

// CatalogOptions selects an explicit default and immutable locale presentations.
type CatalogOptions struct {
	DefaultLocale string
	Locales       map[string]*Presentation
}

// PresentationCatalog resolves exact tag, parents, then the explicit default.
type PresentationCatalog struct {
	defaultLocale string
	locales       map[string]*Presentation
	valid         bool
}

// NewPresentationCatalog rejects normalized duplicate tags and missing defaults.
func NewPresentationCatalog(options CatalogOptions) (*PresentationCatalog, error) {
	defaultLocale, err := NormalizeLocale(options.DefaultLocale)
	if err != nil {
		return nil, err
	}
	locales := map[string]*Presentation{}
	for _, raw := range keys(options.Locales) {
		tag, err := NormalizeLocale(raw)
		if err != nil {
			return nil, err
		}
		value := options.Locales[raw]
		if value == nil || !value.valid {
			return nil, invalid("constructed locale presentation required")
		}
		if _, exists := locales[tag]; exists {
			return nil, invalid("duplicate normalized presentation locale")
		}
		locales[tag] = value
	}
	if _, exists := locales[defaultLocale]; !exists {
		return nil, invalid("presentation default locale must be declared")
	}
	return &PresentationCatalog{defaultLocale: defaultLocale, locales: locales, valid: true}, nil
}

func (*PresentationCatalog) presentationSource() {}

// DefaultLocale returns the normalized declared default.
func (value *PresentationCatalog) DefaultLocale() string { return value.defaultLocale }

// Resolve never prefers a language not selected by the caller or declared default.
func (value *PresentationCatalog) Resolve(locale *string) (string, *Presentation, error) {
	if value == nil || !value.valid {
		return "", nil, invalid("constructed presentation catalog required")
	}
	requested := value.defaultLocale
	if locale != nil {
		var err error
		requested, err = NormalizeLocale(*locale)
		if err != nil {
			return "", nil, err
		}
	}
	parts := strings.Split(requested, "-")
	for length := len(parts); length > 0; length-- {
		tag := strings.Join(parts[:length], "-")
		if result, exists := value.locales[tag]; exists {
			return tag, result, nil
		}
	}
	return value.defaultLocale, value.locales[value.defaultLocale], nil
}

// Variants enumerates all locale values deterministically, including inactive ones.
func (value *PresentationCatalog) Variants() ([]*Presentation, error) {
	if value == nil || !value.valid {
		return nil, invalid("constructed presentation catalog required")
	}
	result := make([]*Presentation, 0, len(value.locales))
	for _, locale := range keys(value.locales) {
		result = append(result, value.locales[locale])
	}
	return result, nil
}

// ResolvePresentation treats a nil source as the unchanged neutral default.
func ResolvePresentation(source Source, locale *string) (string, *Presentation, error) {
	if source == nil {
		source = &Presentation{nodes: map[string]*NodePresentation{}, valid: true}
	}
	return source.Resolve(locale)
}

// NormalizeLocale follows the current reference's practical BCP-47-style tags.
func NormalizeLocale(locale string) (string, error) {
	locale, err := text(locale)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.ReplaceAll(locale, "_", "-"), "-")
	for position, part := range parts {
		length := utf8.RuneCountInString(part)
		if length == 0 || length > 8 || (position == 0 && length < 2) {
			return "", invalid("invalid BCP-47-style presentation locale")
		}
		for _, character := range part {
			if !unicode.IsLetter(character) && (position == 0 || !unicode.IsNumber(character)) {
				return "", invalid("invalid BCP-47-style presentation locale")
			}
		}
	}
	lower, upper, title := cases.Lower(language.Und), cases.Upper(language.Und), cases.Title(language.Und)
	parts[0] = lower.String(parts[0])
	for position := 1; position < len(parts); position++ {
		part := parts[position]
		letters, digits := true, true
		for _, character := range part {
			letters = letters && unicode.IsLetter(character)
			digits = digits && unicode.IsDigit(character)
		}
		length := utf8.RuneCountInString(part)
		switch {
		case length == 4 && letters:
			parts[position] = titlePart(part, &lower, &title)
		case length == 2 && letters || length == 3 && digits:
			parts[position] = upper.String(part)
		default:
			parts[position] = lower.String(part)
		}
	}
	return strings.Join(parts, "-"), nil
}

func titlePart(value string, lower, title *cases.Caser) string {
	// Python titlecase resets at uncased letters, not Unicode word boundaries.
	lowered := lower.String(value)
	var result strings.Builder
	offset, cased := 0, false
	for _, character := range value {
		raw := string(character)
		length := len(lower.String(raw))
		if cased {
			result.WriteString(lowered[offset : offset+length])
		} else {
			result.WriteString(title.String(raw))
		}
		offset += length
		cased = unicode.IsLower(character) || unicode.IsUpper(character) ||
			unicode.IsTitle(character) || unicode.Is(unicode.Other_Lowercase, character) ||
			unicode.Is(unicode.Other_Uppercase, character)
	}
	return result.String()
}

func keys[T any](values map[string]T) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
