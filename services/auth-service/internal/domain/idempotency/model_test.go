package idempotency

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewRecordBuildsDomainModel(t *testing.T) {
	resourceID := uuid.New()
	expiresAt := time.Date(2026, 7, 20, 3, 4, 5, 0, time.UTC)
	record := NewRecord("operation", []byte("scope"), []byte("key"), []byte("request"), &resourceID, nil, expiresAt)
	if record.ID == uuid.Nil || record.Operation != "operation" || record.ResourceID == nil || *record.ResourceID != resourceID || !record.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("NewRecord() = %#v", record)
	}
}
