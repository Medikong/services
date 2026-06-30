package passwordhash

import (
	"encoding/json"
	"net/http"
)

const (
	BenchmarkPassword     = "benchmark-password-1234"
	BenchmarkPasswordHash = "pbkdf2_sha256$210000$bWVkaWtvbmctYXV0aC1iZW5jaG1hcmstc2FsdA==$8tYERV1b/ptbfLi8/TVwUxf46aJ5TxmBowZGazoNn70="
	BenchmarkIterations   = 210000
)

type VerifyRequest struct {
	Password string `json:"password"`
}

type VerifyResponse struct {
	Verified   bool   `json:"verified"`
	Algorithm  string `json:"algorithm"`
	Iterations int    `json:"iterations"`
}

func NewMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /bench/password/verify", verifyPasswordHandler)
	return mux
}

func verifyPasswordHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var request VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	verified, err := VerifyLegacyPBKDF2(request.Password, BenchmarkPasswordHash)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "password verify failed"})
		return
	}

	writeJSON(w, http.StatusOK, VerifyResponse{
		Verified:   verified,
		Algorithm:  LegacyPasswordScheme,
		Iterations: BenchmarkIterations,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
