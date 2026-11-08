package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func presentationPointer(value string) *string { return &value }

func presentationNode(t *testing.T, title string, description *string) *contexture.NodePresentation {
	t.Helper()
	value, err := contexture.NewNodePresentation(contexture.NodePresentationOptions{Title: title, Description: description})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func presentationCopy(t *testing.T, options contexture.PresentationOptions) *contexture.Presentation {
	t.Helper()
	value, err := contexture.NewPresentation(options)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func presentationCatalog(t *testing.T) *contexture.PresentationCatalog {
	t.Helper()
	gateway, err := contexture.NewGatewayPresentation(contexture.GatewayPresentationOptions{Discover: presentationPointer("Explorer")})
	if err != nil {
		t.Fatal(err)
	}
	en := presentationCopy(t, contexture.PresentationOptions{Title: presentationPointer("Human title"), Nodes: map[string]*contexture.NodePresentation{
		"ops": presentationNode(t, "Operations", nil),
	}})
	fr := presentationCopy(t, contexture.PresentationOptions{
		Title: presentationPointer("Titre"), Description: presentationPointer("Description localisee"), Gateway: gateway,
		Nodes: map[string]*contexture.NodePresentation{
			"ops":        presentationNode(t, "Operations FR", presentationPointer("Agir.")),
			"ops/read":   presentationNode(t, "Lire", presentationPointer("Lecture.")),
			"ops/before": presentationNode(t, "Before label", nil),
			"private":    presentationNode(t, "Commande", nil),
		},
	})
	value, err := contexture.NewPresentationCatalog(contexture.PresentationCatalogOptions{DefaultLocale: "en", Locales: map[string]*contexture.Presentation{"en": en, "fr": fr}})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func presentationApplication(t *testing.T, source contexture.PresentationSource, constructions *int) *contexture.Application {
	t.Helper()
	read, err := contexture.NewTool("read", "Read.", true, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	value, err := contexture.Contexture(contexture.ApplicationDeclaration{
		Name: "stable-machine-name", Presentation: source,
		Roots: []contexture.Factory{func() contexture.Node {
			if constructions != nil {
				(*constructions)++
			}
			return &contexture.Role{Name: "ops", Description: "Operate.", Instructions: "Original program.",
				PreProcess: func() *contexture.PreProcess {
					return &contexture.PreProcess{Name: "before", Description: "Prepare.", Instructions: "Original process."}
				},
				Skills: []contexture.Factory{func() contexture.Node {
					return &contexture.Skill{Name: "method", Description: "Method.", Instructions: "Do not translate.", Uses: []string{"ops/read"}}
				}},
				Tools: []contexture.Factory{func() contexture.Node { return read }},
			}
		}},
		PromptRoots: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "private", Description: "Person only.", Instructions: "Ask a person."}
		}},
		Prompts: []contexture.Prompt{{Opens: "private", Description: "Person command.", ModelOpen: contexture.ModelReservedForPerson}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func semanticPresentation(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPresentationNativeRootFacadeIsLazyAndRejectsInactiveUnknownRefs(t *testing.T) {
	constructions := 0
	application := presentationApplication(t, presentationCatalog(t), &constructions)
	if constructions != 0 {
		t.Fatal("presentation constructed a node")
	}
	if _, err := contexture.Compile(application); err != nil {
		t.Fatal(err)
	}
	if constructions != 1 {
		t.Fatal("one compilation constructed roots more than once")
	}
	neutral := presentationCopy(t, contexture.PresentationOptions{})
	bad := presentationCopy(t, contexture.PresentationOptions{Nodes: map[string]*contexture.NodePresentation{"missing": presentationNode(t, "Missing", nil)}})
	source, err := contexture.NewPresentationCatalog(contexture.PresentationCatalogOptions{DefaultLocale: "en", Locales: map[string]*contexture.Presentation{"en": neutral, "fr": bad}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.Compile(presentationApplication(t, source, nil)); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("inactive missing ref accepted: %v", err)
	}
}

func TestPresentationNativeCardsProcessAndBindingRemainUnified(t *testing.T) {
	application := presentationApplication(t, presentationCatalog(t), nil)
	compiled, err := server.CompileApplicationWithOptions(application, server.CompilationOptions{Locale: presentationPointer("fr-CA")})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Locale != "fr" || compiled.Presentation.Title() != "Titre" {
		t.Fatal("parent locale was not selected")
	}
	discovered, err := compiled.Disclosure.Discover(contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	root := discovered["roles"][0]
	if root["name"] != "ops" || root["ref"] != "ops" || root["title"] != "Operations FR" || root["description"] != "Agir." {
		t.Fatal(root)
	}
	opened, err := compiled.Disclosure.Open("ops", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if opened["pre_process"] != "ops/before" || !strings.Contains(opened["instructions"].(string), "Original program.") {
		t.Fatal(opened)
	}
	roles := opened["roles"].([]map[string]any)
	if roles[0]["name"] != "before" || roles[0]["title"] != "Before label" {
		t.Fatal(roles)
	}
	inspection, err := compiled.Disclosure.Inspect([]string{"ops/read"}, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	normalized := semanticPresentation(t, inspection)
	item := normalized["items"].([]any)[0].(map[string]any)
	node := item["node"].(map[string]any)
	if node["title"] != "Lire" || node["description"] != "Lecture." || node["input_schema"] != nil {
		t.Fatal(node)
	}
	method, err := compiled.Disclosure.Open("ops/method", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	normalized = semanticPresentation(t, method)
	if normalized["instructions"] != "Do not translate." || normalized["uses"].([]any)[0].(map[string]any)["title"] != "Lire" {
		t.Fatal(normalized)
	}
	result, err := compiled.Runtime.InvokeReadOnly(context.Background(), "ops/read", []byte("{}"), contexture.AllRoots())
	if err != nil || result != "ok" {
		t.Fatal(result, err)
	}
	readNode, err := compiled.Index.Find("ops/read")
	if err != nil {
		t.Fatal(err)
	}
	selectedSchema, err := compiled.Index.SchemaOf(readNode)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := server.CompileApplication(presentationApplication(t, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	baseNode, err := baseline.Index.Find("ops/read")
	if err != nil {
		t.Fatal(err)
	}
	baseSchema, err := baseline.Index.SchemaOf(baseNode)
	if err != nil || !reflect.DeepEqual(selectedSchema, baseSchema) {
		t.Fatal("schema drift", err)
	}
}

func TestPresentationIndependentViewsPersonReservationAndLegacyDefaults(t *testing.T) {
	compiled, err := server.CompileApplicationWithOptions(presentationApplication(t, presentationCatalog(t), nil), server.CompilationOptions{Locale: presentationPointer("fr")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.Disclosure.Open("private", contexture.AllRoots()); err == nil {
		t.Fatal("person root became model-visible")
	}
	person, err := compiled.Disclosure.Unrestricted().OpenForPerson("private", contexture.AllRoots())
	if err != nil || person["title"] != "Commande" {
		t.Fatal(person, err)
	}
	english, err := compiled.Disclosure.WithLocale(presentationPointer("en"))
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			one, err := compiled.Disclosure.Discover(contexture.AllRoots())
			if err != nil || one["roles"][0]["title"] != "Operations FR" {
				t.Error("French view drift", err)
			}
			two, err := english.Discover(contexture.AllRoots())
			if err != nil || two["roles"][0]["title"] != "Operations" {
				t.Error("English view drift", err)
			}
		}()
	}
	group.Wait()
	baseline, err := server.CompileApplication(presentationApplication(t, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	one, err := baseline.Disclosure.Discover(contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := one["roles"][0]["title"]; exists {
		t.Fatal("legacy default acquired presentation")
	}
	if _, err := compiled.Disclosure.WithLocale(presentationPointer("")); err == nil {
		t.Fatal("explicit empty locale accepted")
	}
}

func presentationWire(t *testing.T, assembly *server.ApplicationServer) (*mcp.InitializeResult, map[string]*mcp.Tool) {
	t.Helper()
	adapter, err := assembly.Build()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	host, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := adapter.Server.Connect(ctx, host, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "presentation-consumer", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		result[tool.Name] = tool
	}
	return session.InitializeResult(), result
}

func TestPresentationOfficialSDKLabelsRetainNamesSchemasSafetyAndStartupRoster(t *testing.T) {
	assembly, err := server.BuildServerWithOptions(presentationApplication(t, presentationCatalog(t), nil), server.ApplicationServerOptions{Locale: presentationPointer("fr-CA")})
	if err != nil {
		t.Fatal(err)
	}
	initialized, tools := presentationWire(t, assembly)
	if initialized.ServerInfo.Name != "stable-machine-name" || initialized.ServerInfo.Title != "Titre" ||
		initialized.ServerInfo.Description != "Description localisee" {
		t.Fatal(initialized.ServerInfo)
	}
	if !strings.Contains(initialized.Instructions, "- Operations FR (ops): Agir.") {
		t.Fatal(initialized.Instructions)
	}
	if len(tools) != 5 || tools["contexture_discover"].Title != "Explorer" {
		t.Fatal(tools)
	}
	baseline, err := server.BuildServer(presentationApplication(t, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, baseTools := presentationWire(t, baseline)
	for name, tool := range tools {
		copy := *tool
		copy.Title = ""
		if !reflect.DeepEqual(&copy, baseTools[name]) {
			t.Fatalf("gateway changed more than title: %s", name)
		}
	}
	structural, err := server.CompileDisclosureApplicationWithOptions(presentationApplication(t, presentationCatalog(t), nil), server.CompilationOptions{Locale: presentationPointer("fr")})
	if err != nil {
		t.Fatal(err)
	}
	card, err := structural.Disclosure.Open("ops/read", contexture.AllRoots())
	if err != nil || card["title"] != "Lire" || card["input_schema"] != nil {
		t.Fatal(card, err)
	}
	adapter, err := structural.Server()
	if err != nil || len(adapter.GatewayNames) != 3 {
		t.Fatal(adapter, err)
	}
	structural.Presentation = nil
	if _, err := structural.Server(); err != nil {
		t.Fatal("legacy manually composed container", err)
	}
	if _, err := server.NewContextureMCPServerWithPresentation(server.Identity{Name: "invalid-copy", Version: "1"},
		adapter.Gateway, contexture.AllRoots(), &contexture.GatewayPresentation{}); err == nil {
		t.Fatal("unconstructed gateway presentation accepted")
	}
	if _, err := server.NewContextureMCPServerWithPresentation(server.Identity{Name: "invalid", Version: "1"}, nil,
		contexture.AllRoots(), nil); err == nil {
		t.Fatal("nil gateway accepted")
	}
}
