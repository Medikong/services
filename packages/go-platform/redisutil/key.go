package redisutil

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/samber/oops"
)

const MaxKeyLength = 512

var (
	keyErr            = oops.In("platform_redis_key").Code("redis.key_invalid")
	structuralSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// KeyBuilder creates namespaced Redis keys without owning a Redis client.
//
// Example:
//
//	builder, err := NewKeyBuilder("local", "product-service", 1)
//	key, err := builder.Build("catalog", "product/42")
//	// key == "local:product-service:v1:catalog:product%2F42"
//
// Use BuildWithHashTag only when related Redis Cluster keys must share a slot:
//
//	key, err = builder.BuildWithHashTag("category:7", "catalog", "product/42")
//	// key == "local:product-service:v1:{category%3A7}:catalog:product%2F42"
type KeyBuilder struct {
	prefix string
}

func NewKeyBuilder(environment, service string, schemaVersion int) (KeyBuilder, error) {
	if err := validateStructuralSegment("environment", environment); err != nil {
		return KeyBuilder{}, err
	}
	if err := validateStructuralSegment("service", service); err != nil {
		return KeyBuilder{}, err
	}
	if schemaVersion < 1 {
		return KeyBuilder{}, keyErr.With("segment", "schema_version").New("schema version must be positive")
	}
	return KeyBuilder{
		prefix: environment + ":" + service + ":v" + strconv.Itoa(schemaVersion),
	}, nil
}

func (b KeyBuilder) Build(identifiers ...string) (string, error) {
	if len(identifiers) == 0 {
		return "", keyErr.With("segment", "identifier").New("at least one identifier is required")
	}
	return b.build("", identifiers)
}

func (b KeyBuilder) BuildWithHashTag(
	hashTag string,
	identifiers ...string,
) (string, error) {
	encodedHashTag, err := encodeIdentifier("hash_tag", hashTag)
	if err != nil {
		return "", err
	}
	return b.build("{"+encodedHashTag+"}", identifiers)
}

func (b KeyBuilder) build(
	hashTag string,
	identifiers []string,
) (string, error) {
	if b.prefix == "" {
		return "", keyErr.With("segment", "prefix").New("key builder is not initialized")
	}
	segments := []string{b.prefix}
	if hashTag != "" {
		segments = append(segments, hashTag)
	}
	for index, identifier := range identifiers {
		encoded, err := encodeIdentifier("identifier_"+strconv.Itoa(index), identifier)
		if err != nil {
			return "", err
		}
		segments = append(segments, encoded)
	}
	key := strings.Join(segments, ":")
	if len(key) > MaxKeyLength {
		return "", keyErr.With("length", len(key), "max_length", MaxKeyLength).New("key is too long")
	}
	return key, nil
}

func validateStructuralSegment(name, value string) error {
	if !structuralSegment.MatchString(value) {
		return keyErr.With("segment", name).New("segment must contain only letters, numbers, dot, underscore, or hyphen")
	}
	return nil
}

func encodeIdentifier(name, value string) (string, error) {
	if value == "" {
		return "", keyErr.With("segment", name).New("identifier is required")
	}
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20"), nil
}
