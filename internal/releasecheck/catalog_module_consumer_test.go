package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalModuleConsumerStaticNodeCatalogAndDebug(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	consumer := t.TempDir()
	mod := "module static-catalog-consumer\n\ngo 1.25.0\n\nrequire github.com/CarterShi01/contexture-mcp-go v0.0.0\n\nreplace github.com/CarterShi01/contexture-mcp-go => " + filepath.ToSlash(root) + "\n"
	source := `package main
import (
    "bytes"
    "context"
    "io"
    "math/big"
    "net/http"
    "strings"
    "time"
    c "github.com/CarterShi01/contexture-mcp-go"
    "github.com/CarterShi01/contexture-mcp-go/cli"
    "github.com/CarterShi01/contexture-mcp-go/debug"
    "github.com/CarterShi01/contexture-mcp-go/introspection"
    "github.com/CarterShi01/contexture-mcp-go/server"
)
func must(err error) { if err != nil { panic(err) } }
func main() {
    app,err:=c.DeclareApplication(c.ApplicationDeclaration{Name:"consumer",Roots:[]c.Factory{func()c.Node{
        return &c.Role{Name:"root",Description:"Straße",Instructions:"native\n",Skills:[]c.Factory{func()c.Node{return &c.Skill{Name:"guide",Description:"guide",Instructions:"guide"}}}}
    }}});must(err)
    compiled,err:=server.CompileApplication(app);must(err)
    catalog,err:=compiled.Catalog(introspection.CatalogOptions{View:introspection.ApplicationView,Detail:introspection.DefinitionDetail});must(err)
    var snapshot *introspection.NodeCatalogSnapshot=catalog.Snapshot()
    var node introspection.NodeDescriptor
    node,err=catalog.Get("root");must(err)
    var role *introspection.RoleDefinition=node.Role
    if role.AuthoredInstructions!="native\n" {panic("program")}
    node.Role.AuthoredInstructions="caller mutation"
    node,err=catalog.Get("root");must(err)
    if node.Role.AuthoredInstructions!="native\n" {panic("alias")}
    var members []introspection.MemberDescriptor=node.Members
    if members[0].Slot!=introspection.SkillSlot {panic("slot")}
    children,err:=catalog.Children("root");must(err)
    if len(children)!=1 {panic("children")}
    found,err:=catalog.List(introspection.ListOptions{Kind:c.RoleKind,Query:"STRASSE"});must(err)
    if len(found)!=1 {panic("full fold")}
    text,err:=snapshot.ToJSON(nil);must(err)
    parsed,err:=introspection.SnapshotFromJSON(text);must(err)
    if parsed.ContentDigest()!=snapshot.ContentDigest(){panic("digest")}
    exact,err:=introspection.CanonicalDigest(map[string]any{"integer":new(big.Int).Lsh(big.NewInt(1),200)});must(err)
    if !strings.HasPrefix(exact,"sha256:"){panic("integer")}
    adapter,err:=debug.New(catalog);must(err)
    ctx,cancel:=context.WithCancel(context.Background())
    address,err:=adapter.Start(ctx,debug.StartOptions{Host:"localhost",Port:0});must(err)
    response,err:=(&http.Client{Timeout:5*time.Second}).Get(address.URL+"/api/catalog");must(err)
    body,err:=io.ReadAll(response.Body);must(err);must(response.Body.Close())
    if response.StatusCode!=200 || string(body)!=text {panic("real HTTP snapshot")}
    cancel();must(address.Close())
    out,stderr:=&bytes.Buffer{},&bytes.Buffer{}
    if cli.RunApplication(context.Background(),app,[]string{"catalog","root","--json"},out,stderr)!=0 || !strings.HasPrefix(out.String(),"["){panic(stderr.String())}
    structural,err:=server.CompileDisclosureApplication(app);must(err)
    _,err=structural.Catalog(introspection.CatalogOptions{View:introspection.ModelView});must(err)
}`
	for name, body := range map[string]string{"go.mod": mod, "main.go": source} {
		if err := os.WriteFile(filepath.Join(consumer, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "-mod=mod", ".")
	command.Dir = consumer
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native external Catalog consumer: %v\n%s", err, output)
	}
}
