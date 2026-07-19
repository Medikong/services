package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/samber/oops"
	"golang.org/x/crypto/argon2"
)

const (
	DefaultPasswordMinimumLength = 12
	DefaultPasswordMaximumLength = 256
	DefaultPasswordMaximumBytes  = 1024

	argon2idSaltLength = 16
	argon2idKeyLength  = 32
	maxArgon2idPHCLen  = 256

	minimumArgon2idMemory             = 19 * 1024
	minimumArgon2idOneIterationMemory = 46 * 1024
	maximumArgon2idMemory             = 64 * 1024
	maximumArgon2idIterations         = 3
	maximumArgon2idParallelism        = 4
)

var strictRawStandardEncoding = base64.RawStdEncoding.Strict()

type argon2idParameters struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

var passwordArgon2idParameters = argon2idParameters{
	memory:      32 * 1024,
	iterations:  3,
	parallelism: 1,
}

var (
	dummyArgon2idSalt = [argon2idSaltLength]byte{0x4d, 0x65, 0x64, 0x69, 0x6b, 0x6f, 0x6e, 0x67, 0x2d, 0x41, 0x75, 0x74, 0x68, 0x2d, 0x32, 0x36}
	dummyArgon2idHash = [argon2idKeyLength]byte{}
)

type PasswordPolicy struct {
	MinimumLength int
	MaximumLength int
	MaximumBytes  int
}

func (p PasswordPolicy) Validate(password string) error {
	p = p.withDefaults()
	if len(password) > p.MaximumBytes {
		return oops.In("password_policy").Code("password.too_many_bytes").
			New("비밀번호는 UTF-8 기준 " + strconv.Itoa(p.MaximumBytes) + "바이트 이하여야 합니다.")
	}
	if !utf8.ValidString(password) {
		return oops.In("password_policy").Code("password.invalid_utf8").
			New("비밀번호는 올바른 UTF-8 문자열이어야 합니다.")
	}
	length := utf8.RuneCountInString(password)
	if length < p.MinimumLength {
		return oops.In("password_policy").Code("password.too_short").
			New("비밀번호는 Unicode code point 기준 " + strconv.Itoa(p.MinimumLength) + "자 이상이어야 합니다.")
	}
	if length > p.MaximumLength {
		return oops.In("password_policy").Code("password.too_long").
			New("비밀번호는 Unicode code point 기준 " + strconv.Itoa(p.MaximumLength) + "자 이하여야 합니다.")
	}
	return nil
}

func (p PasswordPolicy) withDefaults() PasswordPolicy {
	if p.MinimumLength <= 0 {
		p.MinimumLength = DefaultPasswordMinimumLength
	}
	if p.MaximumLength <= 0 {
		p.MaximumLength = DefaultPasswordMaximumLength
	}
	if p.MaximumBytes <= 0 {
		p.MaximumBytes = DefaultPasswordMaximumBytes
	}
	return p
}

func HashPassword(password string) (string, error) {
	return hashPassword(password, passwordArgon2idParameters)
}

func hashPassword(password string, parameters argon2idParameters) (string, error) {
	if !passwordInputWithinBounds(password) {
		return "", oops.In("password_security").Code("password.input_out_of_bounds").
			New("password input is outside the supported bounds")
	}
	if err := validateArgon2idParameters(parameters); err != nil {
		return "", err
	}
	salt := make([]byte, argon2idSaltLength)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", oops.In("password_security").Code("password.salt_failed").Wrap(err)
	}
	hash := argon2.IDKey(
		[]byte(password),
		salt,
		parameters.iterations,
		parameters.memory,
		parameters.parallelism,
		argon2idKeyLength,
	)
	return encodeArgon2idPHC(parameters, salt, hash), nil
}

func VerifyPassword(encodedHash, password string) bool {
	if !passwordInputWithinBounds(password) {
		return false
	}
	parameters, salt, expected, err := decodeArgon2idPHC(encodedHash)
	if err != nil {
		performDummyArgon2id(password)
		return false
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		parameters.iterations,
		parameters.memory,
		parameters.parallelism,
		uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func performDummyArgon2id(password string) {
	actual := argon2.IDKey(
		[]byte(password),
		dummyArgon2idSalt[:],
		passwordArgon2idParameters.iterations,
		passwordArgon2idParameters.memory,
		passwordArgon2idParameters.parallelism,
		argon2idKeyLength,
	)
	_ = subtle.ConstantTimeCompare(actual, dummyArgon2idHash[:])
}

func passwordInputWithinBounds(password string) bool {
	return len(password) <= DefaultPasswordMaximumBytes &&
		utf8.ValidString(password) &&
		utf8.RuneCountInString(password) <= DefaultPasswordMaximumLength
}

func encodeArgon2idPHC(parameters argon2idParameters, salt, hash []byte) string {
	return "$argon2id$v=" + strconv.Itoa(argon2.Version) +
		"$m=" + strconv.FormatUint(uint64(parameters.memory), 10) +
		",t=" + strconv.FormatUint(uint64(parameters.iterations), 10) +
		",p=" + strconv.FormatUint(uint64(parameters.parallelism), 10) +
		"$" + base64.RawStdEncoding.EncodeToString(salt) +
		"$" + base64.RawStdEncoding.EncodeToString(hash)
}

func decodeArgon2idPHC(encoded string) (argon2idParameters, []byte, []byte, error) {
	if len(encoded) > maxArgon2idPHCLen {
		return argon2idParameters{}, nil, nil, invalidPasswordHash()
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return argon2idParameters{}, nil, nil, invalidPasswordHash()
	}
	parameterParts := strings.Split(parts[3], ",")
	if len(parameterParts) != 3 ||
		!strings.HasPrefix(parameterParts[0], "m=") ||
		!strings.HasPrefix(parameterParts[1], "t=") ||
		!strings.HasPrefix(parameterParts[2], "p=") {
		return argon2idParameters{}, nil, nil, invalidPasswordHash()
	}
	memory, err := strconv.ParseUint(strings.TrimPrefix(parameterParts[0], "m="), 10, 32)
	if err != nil {
		return argon2idParameters{}, nil, nil, invalidPasswordHash()
	}
	iterations, err := strconv.ParseUint(strings.TrimPrefix(parameterParts[1], "t="), 10, 32)
	if err != nil {
		return argon2idParameters{}, nil, nil, invalidPasswordHash()
	}
	parallelism, err := strconv.ParseUint(strings.TrimPrefix(parameterParts[2], "p="), 10, 8)
	if err != nil {
		return argon2idParameters{}, nil, nil, invalidPasswordHash()
	}
	parameters := argon2idParameters{
		memory:      uint32(memory),
		iterations:  uint32(iterations),
		parallelism: uint8(parallelism),
	}
	if err := validateArgon2idParameters(parameters); err != nil {
		return argon2idParameters{}, nil, nil, err
	}
	if len(parts[4]) != base64.RawStdEncoding.EncodedLen(argon2idSaltLength) ||
		len(parts[5]) != base64.RawStdEncoding.EncodedLen(argon2idKeyLength) {
		return argon2idParameters{}, nil, nil, invalidPasswordHash()
	}
	salt, err := strictRawStandardEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != argon2idSaltLength {
		return argon2idParameters{}, nil, nil, invalidPasswordHash()
	}
	hash, err := strictRawStandardEncoding.DecodeString(parts[5])
	if err != nil || len(hash) != argon2idKeyLength {
		return argon2idParameters{}, nil, nil, invalidPasswordHash()
	}
	return parameters, salt, hash, nil
}

func validateArgon2idParameters(parameters argon2idParameters) error {
	lanes := uint32(parameters.parallelism)
	if parameters.iterations == 0 || parameters.iterations > maximumArgon2idIterations ||
		lanes == 0 || lanes > maximumArgon2idParallelism ||
		parameters.memory < minimumArgon2idMemory || parameters.memory > maximumArgon2idMemory ||
		parameters.iterations == 1 && parameters.memory < minimumArgon2idOneIterationMemory ||
		parameters.memory < 8*lanes || parameters.memory%(4*lanes) != 0 {
		return invalidPasswordHash()
	}
	return nil
}

func invalidPasswordHash() error {
	return oops.In("password_security").Code("password.hash_invalid").New("invalid Argon2id password hash")
}
