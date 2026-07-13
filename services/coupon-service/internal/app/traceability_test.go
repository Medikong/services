package app

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Medikong/services/services/coupon-service/internal/application/commandworker"
	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	"github.com/Medikong/services/services/coupon-service/internal/application/projection"
	"github.com/Medikong/services/services/coupon-service/internal/domain/catalog"
	couponhttp "github.com/Medikong/services/services/coupon-service/internal/transport/http"
)

type commandSource struct {
	kind   string
	path   string
	marker string
}

var durableCommandSources = map[string]commandSource{
	"CMD.A.19-07": {kind: "worker", path: "internal/platform/workerstore/postgres.go", marker: "CMD.A.19-07"},
	"CMD.A.19-14": {kind: "failure_sink", path: "internal/platform/commandfailure/postgres.go", marker: "CMD.A.19-14"},
	"CMD.A.19-16": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-16"},
	"CMD.A.19-17": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-17"},
	"CMD.A.19-18": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-18"},
	"CMD.A.19-19": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-19"},
	"CMD.A.19-22": {kind: "operations_ingress", path: "internal/application/commanding/ingress.go", marker: "CMD.A.19-22"},
	"CMD.A.19-23": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-23"},
	"CMD.A.19-24": {kind: "worker", path: "internal/platform/workerstore/postgres.go", marker: "CMD.A.19-24"},
	"CMD.A.19-26": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-26"},
	"CMD.A.19-27": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-27"},
	"CMD.A.19-28": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-28"},
	"CMD.A.19-29": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-29"},
	"CMD.A.19-30": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-30"},
	"CMD.A.19-32": {kind: "recovery_worker", path: "internal/application/redemption/service.go", marker: "CMD.A.19-32"},
	"CMD.A.19-33": {kind: "policy", path: "internal/application/policy/registry.go", marker: "CMD.A.19-33"},
	"CMD.A.19-34": {kind: "failure_ingress", path: "internal/application/commanding/ingress.go", marker: "CMD.A.19-34"},
}

func TestBCDocumentCoverage(t *testing.T) {
	if len(catalog.Aggregates) != 8 || len(catalog.Commands) != 34 || len(catalog.Events) != 41 || len(catalog.Policies) != 22 {
		t.Fatalf("catalog counts = aggregates:%d commands:%d events:%d policies:%d", len(catalog.Aggregates), len(catalog.Commands), len(catalog.Events), len(catalog.Policies))
	}
	if len(policy.Definitions()) != 22 {
		t.Fatalf("policy definitions = %d, want 22", len(policy.Definitions()))
	}
	if len(projection.Coverage()) != 41 {
		t.Fatalf("projection event coverage = %d, want 41", len(projection.Coverage()))
	}
	dispatcherIDs := commandDispatcherDocumentIDs()
	if len(dispatcherIDs) != len(commandworker.SupportedDocumentIDs) {
		t.Fatalf("concrete dispatcher handlers = %d, durable catalog = %d", len(dispatcherIDs), len(commandworker.SupportedDocumentIDs))
	}
	for index, id := range dispatcherIDs {
		if id != commandworker.SupportedDocumentIDs[index] {
			t.Fatalf("concrete dispatcher handler %d = %s, durable catalog = %s", index, id, commandworker.SupportedDocumentIDs[index])
		}
	}

	implemented := make(map[string]struct{}, 34)
	for _, operation := range couponhttp.Operations() {
		if operation.CommandID != "" {
			implemented[operation.CommandID] = struct{}{}
		}
	}
	for _, id := range dispatcherIDs {
		implemented[id] = struct{}{}
	}
	for _, command := range catalog.Commands {
		if _, ok := implemented[command.ID]; !ok {
			t.Errorf("command %s (%s) has no synchronous or durable dispatcher location", command.ID, command.Name)
		}
	}
	if len(implemented) != 34 {
		t.Fatalf("implemented command union = %d, want 34", len(implemented))
	}
}

func TestEveryDurableOnlyCommandHasConcreteProductionSource(t *testing.T) {
	httpCommands := make(map[string]struct{})
	for _, operation := range couponhttp.Operations() {
		if operation.CommandID != "" {
			httpCommands[operation.CommandID] = struct{}{}
		}
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate traceability test")
	}
	serviceRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	wantSources := 0
	for _, commandID := range commandworker.SupportedDocumentIDs {
		if _, synchronous := httpCommands[commandID]; synchronous {
			continue
		}
		wantSources++
		source, exists := durableCommandSources[commandID]
		if !exists {
			t.Errorf("durable-only command %s has no production source", commandID)
			continue
		}
		if source.kind == "" || source.path == "" || source.marker == "" {
			t.Errorf("durable-only command %s has an incomplete source record", commandID)
			continue
		}
		contents, err := os.ReadFile(filepath.Join(serviceRoot, source.path))
		if err != nil {
			t.Errorf("read production source for %s: %v", commandID, err)
			continue
		}
		if !bytes.Contains(contents, []byte(source.marker)) {
			t.Errorf("production source %s for %s does not contain %q", source.path, commandID, source.marker)
		}
	}
	if wantSources != 17 || len(durableCommandSources) != wantSources {
		t.Fatalf("durable-only production sources = %d/%d, want 17", len(durableCommandSources), wantSources)
	}
}

func TestOperationsCommandIngressIsWiredIntoServerRuntime(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate traceability test")
	}
	serverSource, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "server.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte("runOperationsCommandSource(runCtx, s.commandSource, s.commandIngress)"),
		[]byte("coupon.operations_command_source_required"),
	} {
		if !bytes.Contains(serverSource, marker) {
			t.Fatalf("server runtime does not contain operations ingress marker %q", marker)
		}
	}
}
