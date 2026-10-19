package postgres

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ijaidev/gojent/core/llm"
)

const timeFormat = "2006-01-02T15:04:05.000000000Z"

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

type timeScanner struct {
	dest *time.Time
}

func (s *timeScanner) Scan(src any) error {
	if src == nil {
		return nil
	}
	switch v := src.(type) {
	case time.Time:
		*s.dest = v.UTC()
		return nil
	case *time.Time:
		if v != nil {
			*s.dest = v.UTC()
		}
		return nil
	case string:
		t, err := parseTime(v)
		if err != nil {
			return err
		}
		*s.dest = t
		return nil
	case []byte:
		t, err := parseTime(string(v))
		if err != nil {
			return err
		}
		*s.dest = t
		return nil
	default:
		return fmt.Errorf("postgres: cannot scan %T into time.Time", src)
	}
}

func scanTime(dest *time.Time) *timeScanner {
	return &timeScanner{dest: dest}
}

type jsonScanner struct {
	dest any
}

func (s *jsonScanner) Scan(src any) error {
	if src == nil {
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("postgres: cannot scan %T into JSON", src)
	}
	trimmed := strings.TrimSpace(string(b))
	if len(trimmed) == 0 || trimmed == "null" || trimmed == `""` {
		return nil
	}
	return json.Unmarshal([]byte(trimmed), s.dest)
}

func scanJSON(dest any) *jsonScanner {
	return &jsonScanner{dest: dest}
}

type contentScanner struct {
	dest *[]llm.Part
}

func (s *contentScanner) Scan(src any) error {
	if src == nil {
		return nil
	}
	var str string
	switch v := src.(type) {
	case []byte:
		str = string(v)
	case string:
		str = v
	default:
		return fmt.Errorf("postgres: cannot scan %T into content", src)
	}
	trimmed := strings.TrimSpace(str)
	if len(trimmed) == 0 || trimmed == "null" {
		*s.dest = nil
		return nil
	}
	var parts []llm.Part
	if err := json.Unmarshal([]byte(trimmed), &parts); err == nil {
		*s.dest = parts
		return nil
	}
	*s.dest = []llm.Part{llm.TextPart(str)}
	return nil
}

func scanContent(dest *[]llm.Part) *contentScanner {
	return &contentScanner{dest: dest}
}
