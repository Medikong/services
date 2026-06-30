package passwordhash

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordVerifyAPIContract(t *testing.T) {
	server := httptest.NewServer(NewMux())
	t.Cleanup(server.Close)

	response := postVerify(t, server.URL, BenchmarkPassword)

	assert.True(t, response.Verified)
	assert.Equal(t, LegacyPasswordScheme, response.Algorithm)
	assert.Equal(t, BenchmarkIterations, response.Iterations)
}

func TestPasswordVerifyAPIRejectsWrongPassword(t *testing.T) {
	server := httptest.NewServer(NewMux())
	t.Cleanup(server.Close)

	response := postVerify(t, server.URL, fixtureWrongPassword)

	assert.False(t, response.Verified)
	assert.Equal(t, LegacyPasswordScheme, response.Algorithm)
	assert.Equal(t, BenchmarkIterations, response.Iterations)
}

func postVerify(t *testing.T, baseURL string, password string) VerifyResponse {
	t.Helper()

	body, err := json.Marshal(VerifyRequest{Password: password})
	require.NoError(t, err)

	httpResponse, err := http.Post(baseURL+"/bench/password/verify", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer httpResponse.Body.Close()
	require.Equal(t, http.StatusOK, httpResponse.StatusCode)

	var response VerifyResponse
	require.NoError(t, json.NewDecoder(httpResponse.Body).Decode(&response))
	return response
}
