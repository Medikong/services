package readmodel

import (
	"strings"
	"testing"
)

func TestValidUserIDCountsUnicodeCodePoints(t *testing.T) {
	if !validUserID(strings.Repeat("가", 128)) {
		t.Fatal("128 Korean characters were rejected")
	}
	if validUserID(strings.Repeat("가", 129)) {
		t.Fatal("129 Korean characters were accepted")
	}
}
