package identity

import "testing"

func TestNormalizeAndMaskPhone(t *testing.T) {
	phone, err := NormalizePhone(" +82 10-1234-5678 ")
	if err != nil {
		t.Fatal(err)
	}
	if phone != "+821012345678" {
		t.Fatalf("NormalizePhone() = %q", phone)
	}
	if masked := MaskPhone(phone); masked != "+82********78" {
		t.Fatalf("MaskPhone() = %q", masked)
	}
	if _, err := NormalizePhone("010-abcd"); err != ErrInvalidPhone {
		t.Fatalf("NormalizePhone() error = %v", err)
	}
}
