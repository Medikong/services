package principal

import "testing"

func TestEncodeDecodeHeaderPreservesAuthContract(t *testing.T) {
	original := Principal{
		Type:        TypeUser,
		UserID:      "user-1",
		Roles:       []string{"customer", "operator"},
		AuthMethods: []string{"password"},
		AuthLevel:   "normal",
		SessionID:   "session-1",
		ClientType:  "api",
	}
	header, err := EncodeHeader(original)
	if err != nil {
		t.Fatalf("EncodeHeader() error = %v", err)
	}
	decoded, err := DecodeHeader(header)
	if err != nil {
		t.Fatalf("DecodeHeader() error = %v", err)
	}
	if decoded.UserID != original.UserID || decoded.SessionID != original.SessionID || decoded.AuthLevel != original.AuthLevel || decoded.ClientType != original.ClientType {
		t.Fatalf("decoded = %+v, want %+v", decoded, original)
	}
	if !decoded.HasRole("operator") || len(decoded.AuthMethods) != 1 || decoded.AuthMethods[0] != "password" {
		t.Fatalf("decoded auth fields = %+v", decoded)
	}
}

func TestDecodeHeaderRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "not-base64", "e30"} {
		if _, err := DecodeHeader(value); err == nil {
			t.Fatalf("DecodeHeader(%q) succeeded, want error", value)
		}
	}
}
