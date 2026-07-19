package security

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	password := "correct horse battery staple"
	first, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash first password: %v", err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash second password: %v", err)
	}
	if !strings.HasPrefix(first, "$argon2id$v=19$m=") {
		t.Fatal("password hash does not use an Argon2id PHC string")
	}
	if first == second {
		t.Fatal("password hashes unexpectedly reused a salt")
	}
	if !VerifyPassword(first, password) {
		t.Fatal("correct password did not verify")
	}
	if VerifyPassword(first, "wrong horse battery staple") {
		t.Fatal("wrong password unexpectedly verified")
	}
}

func TestVerifyPasswordUsesExactInput(t *testing.T) {
	withSpaces := "  exact password value  "
	hash, err := HashPassword(withSpaces)
	if err != nil {
		t.Fatalf("hash password with spaces: %v", err)
	}
	if !VerifyPassword(hash, withSpaces) || VerifyPassword(hash, strings.TrimSpace(withSpaces)) {
		t.Fatal("password input was trimmed during verification")
	}

	composed := strings.Repeat("é", 12)
	decomposed := strings.Repeat("e\u0301", 12)
	hash, err = HashPassword(composed)
	if err != nil {
		t.Fatalf("hash composed password: %v", err)
	}
	if !VerifyPassword(hash, composed) || VerifyPassword(hash, decomposed) {
		t.Fatal("password input was Unicode-normalized during verification")
	}
}

func TestVerifyPasswordRejectsLegacyBcryptCredential(t *testing.T) {
	const legacyCost10Hash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	if VerifyPassword(legacyCost10Hash, "password") {
		t.Fatal("legacy bcrypt credential unexpectedly verified")
	}
}

func TestVerifyPasswordRejectsMalformedPHC(t *testing.T) {
	valid, err := HashPassword("malformed hash test password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	parts := strings.Split(valid, "$")
	tests := map[string]string{
		"empty":                "",
		"wrong algorithm":      strings.Replace(valid, "$argon2id$", "$argon2i$", 1),
		"wrong version":        strings.Replace(valid, "$v=19$", "$v=16$", 1),
		"missing field":        strings.Join(parts[:5], "$"),
		"extra field":          valid + "$extra",
		"reordered parameters": strings.Replace(valid, "m=32768,t=3,p=1", "t=3,m=32768,p=1", 1),
		"duplicate parameter":  strings.Replace(valid, "m=32768,t=3,p=1", "m=32768,t=3,m=32768", 1),
		"invalid salt base64":  strings.Replace(valid, parts[4], strings.Repeat("!", len(parts[4])), 1),
		"padded salt base64":   strings.Replace(valid, parts[4], parts[4]+"=", 1),
		"short salt":           strings.Replace(valid, parts[4], parts[4][1:], 1),
		"short hash":           strings.Replace(valid, parts[5], parts[5][1:], 1),
		"excessive PHC length": valid + strings.Repeat("x", maxArgon2idPHCLen),
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			if VerifyPassword(encoded, "malformed hash test password") {
				t.Fatal("malformed PHC string unexpectedly verified")
			}
		})
	}
}

func TestVerifyPasswordRejectsUnsafeArgon2idParameters(t *testing.T) {
	valid, err := HashPassword("unsafe parameter test password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	tests := map[string]string{
		"zero memory":          "m=0,t=3,p=1",
		"memory below floor":   "m=8192,t=3,p=1",
		"memory above ceiling": "m=65540,t=3,p=1",
		"one pass below floor": "m=38912,t=1,p=1",
		"zero iterations":      "m=32768,t=0,p=1",
		"too many iterations":  "m=32768,t=4,p=1",
		"zero parallelism":     "m=32768,t=3,p=0",
		"too much parallelism": "m=32768,t=3,p=5",
		"misaligned memory":    "m=19457,t=2,p=1",
	}
	for name, parameters := range tests {
		t.Run(name, func(t *testing.T) {
			encoded := strings.Replace(valid, "m=32768,t=3,p=1", parameters, 1)
			if VerifyPassword(encoded, "unsafe parameter test password") {
				t.Fatal("unsafe Argon2id parameters unexpectedly verified")
			}
		})
	}
}

func TestArgon2idParameterPolicyAcceptsApprovedBoundaries(t *testing.T) {
	for _, parameters := range []argon2idParameters{
		{memory: 19 * 1024, iterations: 2, parallelism: 1},
		{memory: 46 * 1024, iterations: 1, parallelism: 1},
	} {
		if err := validateArgon2idParameters(parameters); err != nil {
			t.Fatalf("approved parameters %#v rejected: %v", parameters, err)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	policy := PasswordPolicy{}
	tests := []struct {
		name      string
		password  string
		wantError string
	}{
		{name: "minimum ASCII", password: strings.Repeat("a", 12)},
		{name: "minimum multibyte", password: strings.Repeat("가", 12)},
		{name: "spaces are not trimmed", password: strings.Repeat(" ", 12)},
		{name: "too short", password: strings.Repeat("a", 11), wantError: "Unicode code point 기준 12자 이상"},
		{name: "too many code points", password: strings.Repeat("a", 257), wantError: "Unicode code point 기준 256자 이하"},
		{name: "invalid UTF-8", password: string([]byte{0xff}), wantError: "올바른 UTF-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := policy.Validate(test.password)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("validate password: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validation error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestPasswordPolicyEnforcesUTF8ByteCap(t *testing.T) {
	policy := PasswordPolicy{MinimumLength: 1, MaximumLength: 300, MaximumBytes: 1024}
	err := policy.Validate(strings.Repeat("😀", 257))
	if err == nil || !strings.Contains(err.Error(), "UTF-8 기준 1024바이트 이하") {
		t.Fatalf("validation error = %v, want UTF-8 byte cap", err)
	}
}

func TestHashPasswordRejectsOversizedInput(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("a", DefaultPasswordMaximumLength+1)); err == nil {
		t.Fatal("oversized password was hashed")
	}
}
