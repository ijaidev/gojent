package sqlite

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(timeFormat, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Parse(time.DateTime, s)
}

func genID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}
