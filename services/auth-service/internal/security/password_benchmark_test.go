package security

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

var (
	benchmarkPasswordHash string
	benchmarkVerified     bool
)

func BenchmarkArgon2idCandidates(b *testing.B) {
	candidates := []struct {
		name       string
		parameters argon2idParameters
	}{
		{name: "m19456_t2_p1", parameters: argon2idParameters{memory: 19 * 1024, iterations: 2, parallelism: 1}},
		{name: "m32768_t3_p1", parameters: argon2idParameters{memory: 32 * 1024, iterations: 3, parallelism: 1}},
		{name: "m47104_t1_p1", parameters: argon2idParameters{memory: 46 * 1024, iterations: 1, parallelism: 1}},
		{name: "m65536_t3_p1", parameters: argon2idParameters{memory: 64 * 1024, iterations: 3, parallelism: 1}},
		{name: "m65536_t3_p4", parameters: argon2idParameters{memory: 64 * 1024, iterations: 3, parallelism: 4}},
	}
	for _, candidate := range candidates {
		b.Run(candidate.name, func(b *testing.B) {
			benchmarkArgon2idCandidate(b, candidate.parameters)
		})
	}
}

func benchmarkArgon2idCandidate(b *testing.B, parameters argon2idParameters) {
	const password = "benchmark-password-input-2026"
	hash, err := hashPassword(password, parameters)
	if err != nil {
		b.Fatalf("prepare Argon2id hash: %v", err)
	}
	b.Run("Hash", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkPasswordHash, err = hashPassword(password, parameters)
			if err != nil {
				b.Fatalf("hash password: %v", err)
			}
		}
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/s")
	})
	b.Run("VerifyOK", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkVerified = VerifyPassword(hash, password)
		}
		if !benchmarkVerified {
			b.Fatal("correct password did not verify")
		}
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/s")
	})
	b.Run("VerifyWrong", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkVerified = VerifyPassword(hash, "wrong----password-input-2026")
		}
		if benchmarkVerified {
			b.Fatal("wrong password verified")
		}
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/s")
	})
	b.Run("ParallelVerifyOK", func(b *testing.B) {
		b.ReportAllocs()
		b.SetParallelism(1)
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if !VerifyPassword(hash, password) {
					b.Error("correct password did not verify")
					return
				}
			}
		})
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/s")
	})
}

func BenchmarkPasswordBcryptCost10Baseline(b *testing.B) {
	const password = "benchmark-password-input-2026"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		b.Fatalf("prepare bcrypt hash: %v", err)
	}
	b.Run("Hash", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			generated, err := bcrypt.GenerateFromPassword([]byte(password), 10)
			if err != nil {
				b.Fatalf("hash password: %v", err)
			}
			benchmarkPasswordHash = string(generated)
		}
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/s")
	})
	b.Run("VerifyOK", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkVerified = bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
		}
		if !benchmarkVerified {
			b.Fatal("correct password did not verify")
		}
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/s")
	})
	b.Run("VerifyWrong", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkVerified = bcrypt.CompareHashAndPassword(hash, []byte("wrong----password-input-2026")) == nil
		}
		if benchmarkVerified {
			b.Fatal("wrong password verified")
		}
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/s")
	})
	b.Run("ParallelVerifyOK", func(b *testing.B) {
		b.ReportAllocs()
		b.SetParallelism(1)
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
					b.Error("correct password did not verify")
					return
				}
			}
		})
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/s")
	})
}
