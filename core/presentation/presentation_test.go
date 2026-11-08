package presentation_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
	"github.com/CarterShi01/contexture-mcp-go/core/presentation"
)

func pointer(value string) *string { return &value }

func TestPresentationNormalizeLocales(t *testing.T) {
	for _, row := range []struct{ input, expected string }{
		{" zh_hans_cn ", "zh-Hans-CN"},
		{"EN-us", "en-US"},
		{"es-419", "es-419"},
		{"de-DE-u-co-phonebk", "de-DE-u-CO-phonebk"},
		{"\u0085en\u0085", "en"},
		{"en-\u00dfabc", "en-Ssabc"},
		{"en-\u01c6abc", "en-\u01c5abc"},
		{"en-\u1f80abc", "en-\u1f88abc"},
		{"en-\u0130abc", "en-\u0130abc"},
		{"en-\u4e2dabc", "en-\u4e2dAbc"},
		{"en-ab\u03a3\u03a3", "en-Ab\u03c3\u03c2"},
		{"en-\u0295abc", "en-\u0295abc"},
		{"en-a\u01bbbb", "en-A\u01bbBb"},
		{"en-aa\u01bbb", "en-Aa\u01bbB"},
		{"en-a\u0294bb", "en-A\u0294Bb"},
	} {
		t.Run(row.input, func(t *testing.T) {
			got, err := presentation.NormalizeLocale(row.input)
			if err != nil || got != row.expected {
				t.Fatalf("got %q, %v; want %q", got, err, row.expected)
			}
		})
	}
}

func TestPresentationRejectsInvalidAndTypedNilValues(t *testing.T) {
	for _, value := range []string{"", " ", "e", "en--US", "en-123456789", "en/US", "\ufeffen", string([]byte{0xff})} {
		if _, err := presentation.NormalizeLocale(value); !errors.Is(err, foundation.ErrInvalidDeclaration) {
			t.Fatalf("invalid locale %q: %v", value, err)
		}
	}
	if _, err := presentation.NewNodePresentation(presentation.NodeOptions{Title: " "}); err == nil {
		t.Fatal("empty title accepted")
	}
	if _, err := presentation.NewGatewayPresentation(presentation.GatewayOptions{Discover: pointer("")}); err == nil {
		t.Fatal("explicit empty gateway accepted")
	}
	if _, err := presentation.NewPresentation(presentation.Options{Title: pointer("")}); err == nil {
		t.Fatal("explicit empty product title accepted")
	}
	if _, err := presentation.NewPresentation(presentation.Options{Gateway: &presentation.GatewayPresentation{}}); err == nil {
		t.Fatal("unconstructed gateway accepted")
	}
	if _, err := presentation.NewPresentation(presentation.Options{Nodes: map[string]*presentation.NodePresentation{"ops": nil}}); err == nil {
		t.Fatal("nil node copy accepted")
	}
	if _, err := presentation.NewPresentationCatalog(presentation.CatalogOptions{DefaultLocale: "en"}); err == nil {
		t.Fatal("missing default accepted")
	}
	for _, source := range []presentation.Source{(*presentation.Presentation)(nil), &presentation.Presentation{}, (*presentation.PresentationCatalog)(nil)} {
		if _, _, err := presentation.ResolvePresentation(source, nil); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
	if _, _, err := presentation.ResolvePresentation(nil, pointer("")); err == nil {
		t.Fatal("explicit empty locale accepted")
	}
}

func TestPresentationSnapshotsAndLocaleResolution(t *testing.T) {
	node, err := presentation.NewNodePresentation(presentation.NodeOptions{Title: " Label ", Description: pointer(" Copy ")})
	if err != nil {
		t.Fatal(err)
	}
	original := map[string]*presentation.NodePresentation{"ops": node}
	title := "Title"
	neutral, err := presentation.NewPresentation(presentation.Options{Title: &title, Nodes: original})
	if err != nil {
		t.Fatal(err)
	}
	title = "changed"
	delete(original, "ops")
	exposed := neutral.Nodes()
	delete(exposed, "ops")
	got, ok := neutral.Node("ops")
	if !ok || got.Title() != "Label" || got.Description() != "Copy" || neutral.Title() != "Title" {
		t.Fatal("mutable input escaped")
	}
	if _, err := presentation.NewPresentation(presentation.Options{Nodes: map[string]*presentation.NodePresentation{"ops": node, " ops ": node}}); err == nil {
		t.Fatal("duplicate normalized ref accepted")
	}
	english, err := presentation.NewPresentation(presentation.Options{Title: pointer("English")})
	if err != nil {
		t.Fatal(err)
	}
	locales := map[string]*presentation.Presentation{"en": english, "fr": neutral}
	catalog, err := presentation.NewPresentationCatalog(presentation.CatalogOptions{DefaultLocale: "fr", Locales: locales})
	if err != nil {
		t.Fatal(err)
	}
	delete(locales, "fr")
	for _, value := range []*string{nil, pointer("fr-CA"), pointer("zz-XX")} {
		tag, selected, err := catalog.Resolve(value)
		if err != nil || tag != "fr" || selected != neutral {
			t.Fatalf("resolution %v: %s %p %v", value, tag, selected, err)
		}
	}
	if _, err := presentation.NewPresentationCatalog(presentation.CatalogOptions{DefaultLocale: "en", Locales: map[string]*presentation.Presentation{"en": neutral, "EN": neutral}}); err == nil {
		t.Fatal("duplicate normalized locale accepted")
	}
}

func TestPresentationGatewayAndConcurrentIndependentLocales(t *testing.T) {
	gateway, err := presentation.NewGatewayPresentation(presentation.GatewayOptions{Discover: pointer(" Explorer ")})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := gateway.TitleFor(foundation.DiscoverGatewayName); err != nil || value != "Explorer" {
		t.Fatal(value, err)
	}
	if _, err := gateway.TitleFor("not-a-gateway"); err == nil {
		t.Fatal("unknown gateway accepted")
	}
	neutral, err := presentation.NewPresentation(presentation.Options{})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := presentation.NewPresentationCatalog(presentation.CatalogOptions{DefaultLocale: "en", Locales: map[string]*presentation.Presentation{"en": neutral, "fr": neutral}})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for attempt := 0; attempt < 20; attempt++ {
				tag, value, err := catalog.Resolve(pointer("fr-CA"))
				if err != nil || tag != "fr" || !reflect.DeepEqual(value, neutral) {
					t.Error("concurrent resolution failed", err)
					return
				}
				if _, err := presentation.NormalizeLocale("en-\u1f80abc"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	group.Wait()
}
