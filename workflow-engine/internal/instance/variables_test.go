package instance

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVariablesToMap_EmptyBytes_ReturnsEmptyMap(t *testing.T) {
	m, err := variablesToMap(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatalf("expected an empty map, got nil")
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestVariablesToMap_ValidJSON_ParsesValues(t *testing.T) {
	m, err := variablesToMap(json.RawMessage(`{"a": 1, "b": "two", "c": true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m["a"].(float64); got != 1 {
		t.Errorf("a: got %v, want 1 (numbers should decode as float64)", got)
	}
	if got := m["b"].(string); got != "two" {
		t.Errorf("b: got %q, want %q", got, "two")
	}
	if got := m["c"].(bool); got != true {
		t.Errorf("c: got %v, want true", got)
	}
}

func TestVariablesToMap_CorruptJSON_ReturnsError(t *testing.T) {
	_, err := variablesToMap(json.RawMessage(`{not-json`))
	if err == nil {
		t.Fatalf("expected an error on corrupt JSON")
	}
	if !strings.Contains(err.Error(), "unmarshal instance variables") {
		t.Errorf("expected error to mention 'unmarshal instance variables', got %q", err.Error())
	}
}

func TestVariablesToMap_EmptyObject_ReturnsEmptyMap(t *testing.T) {
	m, err := variablesToMap(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map for {}, got %v", m)
	}
}
