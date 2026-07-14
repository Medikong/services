package http

import "testing"

func TestOperationCatalogHasDocumentTraceability(t *testing.T) {
	operations := Operations()
	if len(operations) != 25 {
		t.Fatalf("operation count = %d, want 25", len(operations))
	}
	commands := 0
	seenAPIs := make(map[string]struct{}, len(operations))
	seenCommands := make(map[string]struct{})
	for _, operation := range operations {
		if _, exists := seenAPIs[operation.ID]; exists {
			t.Fatalf("duplicate API document ID %s", operation.ID)
		}
		seenAPIs[operation.ID] = struct{}{}
		if operation.Command {
			commands++
			if operation.CommandID == "" {
				t.Fatalf("command operation %s has no command document ID", operation.ID)
			}
			if _, exists := seenCommands[operation.CommandID]; exists {
				t.Fatalf("duplicate synchronous command document ID %s", operation.CommandID)
			}
			seenCommands[operation.CommandID] = struct{}{}
		} else if operation.CommandID != "" {
			t.Fatalf("query operation %s unexpectedly maps to %s", operation.ID, operation.CommandID)
		}
	}
	if commands != 17 {
		t.Fatalf("HTTP command count = %d, want 17", commands)
	}
}
