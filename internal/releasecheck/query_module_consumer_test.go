package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalModuleConsumerManagedNativeReads(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	consumer := t.TempDir()
	mod := "module native-read-consumer\n\ngo 1.25.0\n\nrequire github.com/CarterShi01/contexture-mcp-go v0.0.0\n\nreplace github.com/CarterShi01/contexture-mcp-go => " + filepath.ToSlash(root) + "\n"
	source := `package main

import (
    "context"
    "fmt"
    "strings"
    "time"
    m "github.com/CarterShi01/contexture-mcp-go/managed"
)

// An explicit trusted-port contract fixture, not a production durable owner.
type releaseFixture struct { value m.CapturedRelease }
func (f releaseFixture) Capture() (*m.CapturedRelease, error) { v := f.value; return &v, nil }
func (f releaseFixture) Fence(a m.ServingAttempt, g *m.ReadyGeneration, adopt func() error) (bool, error) {
    if a.Release.ReleaseEpoch != f.value.ReleaseEpoch { return false, nil }
    return true, adopt()
}
func must(err error) { if err != nil { panic(err) } }
func object(v m.FrozenJSON) map[string]any { return v.JSON().(map[string]any) }

func main() {
    ctx, err := m.WithIdentity(context.Background(), m.Identity{ActorID:"actor", TenantID:"tenant", AuthorityRevision:"authority"})
    must(err)
    d, err := m.NewGraphDefinition(map[string]any{
        "schema":"contexture.graph/v1", "semantic_version":"1", "graph_id":"graph",
        "application_name":"Native", "mode":"disclosure_only",
        "nodes":[]any{map[string]any{"node_id":"owner","kind":"role","name":"Owner","description":"Native owner","instructions":"Program"}},
        "roots":[]any{map[string]any{"node_id":"owner","plane":"model"}},
        "presentations":map[string]any{"default_locale":"en","catalogs":[]any{}},
    }, nil)
    must(err)
    state, err := m.NewRevisionState(d,"1",nil); must(err)
    plan, err := m.Compile(d,m.CompilerOptions{NoExecutionRegistryDigest:"sha256:"+strings.Repeat("a",64),
        Locale:"en",CompilerContractVersion:"1",CompilerBuild:"consumer",DependencyContractID:"app",DependencyBuild:"consumer"})
    must(err)
    coordinator, err := m.NewLocalServingCoordinator(m.ServingOptions{GraphID:"graph",TargetID:"target",InstanceID:"consumer",
        Authority:releaseFixture{m.CapturedRelease{GraphID:"graph",TargetID:"target",ReleaseEpoch:"1",JournalSeq:"1",Snapshot:state,ManifestDigest:plan.Manifest().Digest()}},
        Provider:m.ServingPreparationFunc(func(ctx context.Context,a m.ServingAttempt)(*m.ReadyGeneration,error){
            return m.PrepareGeneration(ctx,plan,m.PreparationContext{GraphID:"graph",Revision:"1",GenerationUUID:a.GenerationUUID},nil)
        })})
    must(err)
    defer func(){must(coordinator.Shutdown(context.Background()))}()
    adopted := coordinator.Prepare(ctx); must(adopted.Error)
    if adopted.Status != "serving" { panic(adopted.Status) }
    runtime, err := m.NewManagedRuntime(coordinator,m.RequestAuthorityFunc(func(_ context.Context,r m.RequestAuthorization)([]string,error){
        return r.RequestedIDs,nil
    }),nil); must(err)
    serving := m.ServingReadPort{Runtime:runtime,StoreOrigin:func(context.Context)(string,error){return "consumer-store",nil},
        Fence:func(_ context.Context,dispatch func()(m.FrozenJSON,error))(m.FrozenJSON,error){return dispatch()}}
    signer, err := m.NewCursorSigner([]byte(strings.Repeat("k",32))); must(err)
    definition := m.DefinitionReadFunc(func(ctx context.Context,r m.DefinitionReadRequest,dispatch func(m.DefinitionReadView)(m.FrozenJSON,error))(m.FrozenJSON,error){
        id, err := m.IdentityFromContext(ctx); if err != nil{return m.FrozenJSON{},err}
        return dispatch(m.DefinitionReadView{Identity:id,StoreOrigin:"consumer-store",Index:plan.Index(),CurrentIndex:plan.Index(),Revision:"1",ScopeIDs:[]string{"owner"}})
    })
    catalog, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions:m.PageStoreOptions{Signer:signer},Definition:definition,Serving:&serving}); must(err)
    defer catalog.Shutdown()
    profile, err := m.NewCatalogProfile(m.PageProfileOptions{Limit:1}); must(err)
    request := m.CatalogRequest{GraphID:"graph",Source:m.CatalogHead,Profile:&profile}
    first, err := catalog.Start(ctx,request,time.Minute); must(err)
    token := object(first)["streams"].(map[string]any)["nodes"].(string)
    page, err := catalog.Continuation(ctx,token,request,m.CatalogNodes); must(err)
    if object(page)["complete"] != true || len(object(page)["items"].([]any)) != 1 {panic("Catalog completeness")}
    paging, err := m.NewManagedPagingService(m.PagingOptions{PageStoreOptions:m.PageStoreOptions{Signer:signer},Serving:serving}); must(err)
    defer paging.Shutdown()
    current, _ := coordinator.Current()
    ref, err := m.FormatReference("graph",current.Attempt.GenerationUUID,"owner"); must(err)
    opened, err := paging.Start(ctx,m.PagingRequest{Operation:m.PageOpen,Reference:ref},time.Minute); must(err)
    if object(opened)["owner_active"].(map[string]any)["instructions"] != "Program" {panic("Atomic native program")}
    reader := m.CommittedBatchReadFunc(func(ctx context.Context,id string,dispatch func(m.CommittedReadCut)(m.FrozenJSON,error))(m.FrozenJSON,error){
        identity,err := m.IdentityFromContext(ctx);if err != nil{return m.FrozenJSON{},err}
        return dispatch(m.CommittedReadCut{Identity:identity,StoreOrigin:"consumer-store",ScopeIDs:[]string{"owner"},
            Snapshot:state,Watermark:"1",RetainedAfter:"0",
            ReadBatches:func(context.Context,string,m.CommittedBatchBounds)(m.CommittedBatchWindow,error){
                return m.CommittedBatchWindow{Before:state,Through:"1"},nil
            }})
    })
    watch, err := m.NewManagedWatchService(m.WatchOptions{Reader:reader,Signer:signer}); must(err)
    defer watch.Shutdown()
    listed, err := watch.Start(ctx,m.WatchRequest{GraphID:"graph"},time.Minute); must(err)
    polled, err := watch.Poll(ctx,object(listed)["cursor"].(string),m.WatchRequest{GraphID:"graph"}); must(err)
    if len(object(polled)["batches"].([]any)) != 0 {panic("Committed cut")}
    fmt.Println("native managed reads consumer passed")
}
`
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte(mod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "-mod=mod", "-race", ".")
	command.Dir = consumer
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("external native read consumer: %v\n%s", err, output)
	}
	t.Log(string(output))
}
