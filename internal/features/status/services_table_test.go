package status_test

import (
	"context"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/features/status"
)

// The interactive scope table shows image and port columns, so the read-only
// catalog must publish them. The values are the same presentation strings the
// status document uses, so the two screens cannot disagree about one service.
func TestServicesPublishesImageAndPortsForTheScopeTable(t *testing.T) {
	backend := &fakeBackend{snap: sampleSnapshot()}
	f := status.New(backend)
	services, err := f.Services(context.Background(), core.Invocation{
		Target: core.Target{ProjectDir: "/work/hello-world"},
	})
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	byName := map[string]core.ServiceStatus{}
	for _, svc := range services {
		byName[svc.Name] = svc
	}

	web := byName["web"]
	if web.Image != "nginx:alpine" {
		t.Fatalf("web image = %q, want the image reference", web.Image)
	}
	if web.Ports != "8080→80" {
		t.Fatalf("web ports = %q, want the runtime binding", web.Ports)
	}

	api := byName["api"]
	if api.Image != "api:local (로컬 빌드)" {
		t.Fatalf("api image = %q, want the local-build marker", api.Image)
	}
	if api.Ports != "-" {
		t.Fatalf("api ports = %q, want the approved dash", api.Ports)
	}

	db := byName["db"]
	if db.Image != "postgres:16" || db.Ports != "55432→5432" {
		t.Fatalf("db fact = image %q ports %q", db.Image, db.Ports)
	}
}
