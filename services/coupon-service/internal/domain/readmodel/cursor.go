package readmodel

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxCursorLength = 512

type pageCursor struct {
	Version int    `json:"v"`
	Kind    string `json:"k"`
	Time    string `json:"t"`
	ID      string `json:"id"`
}

func encodeCursor(kind string, at time.Time, id string) (string, error) {
	if strings.TrimSpace(kind) == "" || at.IsZero() || strings.TrimSpace(id) == "" || len(id) > 200 {
		return "", ErrInvalidCursor
	}
	payload, err := json.Marshal(pageCursor{Version: 1, Kind: kind, Time: at.UTC().Format(time.RFC3339Nano), ID: id})
	if err != nil {
		return "", ErrInvalidCursor
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	if len(encoded) > maxCursorLength {
		return "", ErrInvalidCursor
	}
	return encoded, nil
}

func decodeCursor(value, kind string) (time.Time, string, error) {
	if value == "" {
		return time.Time{}, "", nil
	}
	if len(value) > maxCursorLength || strings.TrimSpace(kind) == "" {
		return time.Time{}, "", ErrInvalidCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var cursor pageCursor
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.Kind != kind || strings.TrimSpace(cursor.ID) == "" || len(cursor.ID) > 200 {
		return time.Time{}, "", ErrInvalidCursor
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return time.Time{}, "", ErrInvalidCursor
	}
	if kind == "timeline" || kind == "cost" {
		if _, err := uuid.Parse(cursor.ID); err != nil {
			return time.Time{}, "", ErrInvalidCursor
		}
	}
	at, err := time.Parse(time.RFC3339Nano, cursor.Time)
	if err != nil || at.IsZero() {
		return time.Time{}, "", ErrInvalidCursor
	}
	return at, cursor.ID, nil
}
