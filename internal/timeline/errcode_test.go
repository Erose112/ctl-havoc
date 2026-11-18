package timeline

import "testing"

func TestErrCodeString(t *testing.T) {
	if ErrTimeout.String() != "timeout" {
		t.Fatalf("got %q", ErrTimeout.String())
	}
}
