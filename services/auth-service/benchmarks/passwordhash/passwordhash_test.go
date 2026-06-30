package passwordhash

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fixturePassword      = "benchmark-password-1234"
	fixtureWrongPassword = "wrong-password-1234"
	fixtureIterations    = 210000
	fixtureSalt          = "medikong-auth-benchmark-salt"
	fixtureDigestB64     = "8tYERV1b/ptbfLi8/TVwUxf46aJ5TxmBowZGazoNn70="
	fixturePasswordHash  = "pbkdf2_sha256$210000$bWVkaWtvbmctYXV0aC1iZW5jaG1hcmstc2FsdA==$8tYERV1b/ptbfLi8/TVwUxf46aJ5TxmBowZGazoNn70="
)

func TestPBKDF2SHA256FixtureMatchesPythonContract(t *testing.T) {
	digest, err := DeriveLegacyPBKDF2(fixturePassword, []byte(fixtureSalt), fixtureIterations)
	require.NoError(t, err)

	assert.Equal(t, fixtureDigestB64, base64.StdEncoding.EncodeToString(digest))

	verified, err := VerifyLegacyPBKDF2(fixturePassword, fixturePasswordHash)
	require.NoError(t, err)
	assert.True(t, verified)

	verified, err = VerifyLegacyPBKDF2(fixtureWrongPassword, fixturePasswordHash)
	require.NoError(t, err)
	assert.False(t, verified)
}

func TestParseLegacyPBKDF2RejectsInvalidHash(t *testing.T) {
	_, err := VerifyLegacyPBKDF2(fixturePassword, "pbkdf2_sha256$not-a-number$salt$digest")
	require.Error(t, err)
}

func BenchmarkVerifyLegacyPBKDF2(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		verified, err := VerifyLegacyPBKDF2(fixturePassword, fixturePasswordHash)
		if err != nil {
			b.Fatal(err)
		}
		if !verified {
			b.Fatal("PBKDF2 benchmark password verification failed")
		}
	}
}
