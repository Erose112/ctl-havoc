package assert

import (
	"testing"

	"github.com/Queens-Rocket-Engineering-Team/ctl-havoc/internal/timeline"
)

func TestEqualValues_numberCoercion(t *testing.T) {
	tests := []struct {
		name   string
		actual any
		want   string
		match  bool
	}{
		{"int vs 200", 200, "200", true},
		{"int vs 200.0", 200, "200.0", true},
		{"int vs quoted 200", 200, `"200"`, true},
		{"float64 vs 200", float64(200), "200", true},
		{"float64 200.0 vs 200", 200.0, "200", true},
		{"string 200 vs 200", "200", "200", true},
		{"string 200 vs 200.0", "200", "200.0", true},
		{"int mismatch", 200, "404", false},
		{"string non-numeric", "alive", "alive", true},
		{"string mismatch", "alive", "dead", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := equalValues(tt.actual, tt.want)
			if got != tt.match {
				t.Fatalf("equalValues(%#v, %q) = %v, want %v", tt.actual, tt.want, got, tt.match)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	sample := timeline.Sample{
		Err: timeline.ErrTimeout,
		Fields: map[string]any{
			"status":  200,
			"message": "alive",
		},
	}

	tests := []struct {
		name    string
		pred    string
		want    bool
		wantErr bool
	}{
		{"status int", `status == 200`, true, false},
		{"status float rhs", `status == 200.0`, true, false},
		{"status quoted", `status == "200"`, true, false},
		{"status fail", `status == 404`, false, false},
		{"err timeout", `err == "timeout"`, true, false},
		{"err fail", `err == "none"`, false, false},
		{"message", `message == "alive"`, true, false},
		{"missing field", `connected == true`, false, false},
		{"bad len", `status`, false, true},
		{"bad op", `status != 200`, false, true},
		{"latency deferred", `latency == 1s`, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := match(sample, tt.pred)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("match(%q) err = nil, want error", tt.pred)
				}
				return
			}
			if err != nil {
				t.Fatalf("match(%q) unexpected err: %v", tt.pred, err)
			}
			if got != tt.want {
				t.Fatalf("match(%q) = %v, want %v", tt.pred, got, tt.want)
			}
		})
	}
}

func TestMatch_statusFromJSONFloat(t *testing.T) {
	// encoding/json decodes numbers as float64; body fields may look like this.
	s := timeline.Sample{
		Fields: map[string]any{"count": float64(200)},
	}
	ok, err := match(s, `count == 200`)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal(`count == 200: got false, want true for float64(200)`)
	}
}
