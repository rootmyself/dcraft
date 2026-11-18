//go:build unix

package state

import (
	"strings"
	"testing"
)

func TestRootOwnerErrorIsPureAndActionable(t *testing.T) {
	if err := rootOwnerError("/state", 1000, 1000); err != nil {
		t.Fatalf("same owner rejected: %v", err)
	}
	err := rootOwnerError("/state", 1000, 1001)
	if err == nil {
		t.Fatal("different owner accepted")
	}
	for _, want := range []string{"/state", "1000", "1001"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("owner error missing %q: %v", want, err)
		}
	}
}
