package http

import (
	"strings"
	"testing"
)

func TestStringRangeCountsUnicodeCodePoints(t *testing.T) {
	if problem := stringRange("displayName", strings.Repeat("가", 120), 1, 120); problem != nil {
		t.Fatalf("120 Korean characters were rejected: %#v", problem)
	}
	if problem := stringRange("displayName", strings.Repeat("가", 121), 1, 120); problem == nil {
		t.Fatal("121 Korean characters were accepted")
	}
	if problem := stringRange("displayName", " "+strings.Repeat("가", 120)+" ", 1, 120); problem == nil {
		t.Fatal("surrounding spaces bypassed the raw OpenAPI maxLength")
	}
	if problem := stringRange("displayName", string([]byte{0xff}), 1, 120); problem == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
}
