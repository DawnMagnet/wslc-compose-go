package wslc

import "testing"

func TestParseCapabilities(t *testing.T) {
	tests := []struct {
		raw   string
		known bool
		floor bool // >= 3.0.3.0
	}{
		{"wslc version 3.0.3.0", true, true},
		{"wslc 3.0.1\n", true, false},
		{"3.0.10.1", true, true},
		{"3.1", true, true},
		{"3.0.2.9", true, false},
		{"", false, false},
		{"not a version", false, false},
	}
	for _, tt := range tests {
		c := ParseCapabilities(tt.raw)
		if c.Known != tt.known {
			t.Errorf("%q known=%v want %v", tt.raw, c.Known, tt.known)
		}
		if got := c.AtLeast(VerEndpoints); got != tt.floor {
			t.Errorf("%q AtLeast(%s)=%v want %v", tt.raw, VerEndpoints, got, tt.floor)
		}
	}
	assumed := AssumeCurrent()
	if !assumed.Assumed || !assumed.AtLeast(VerBuildSecret) || !assumed.AtLeast(VerHostGateway) {
		t.Fatalf("assumed capabilities: %+v", assumed)
	}
	if got := ParseCapabilities("dev").AtLeast(VerHostGateway); got {
		t.Fatal("unparseable version must not unlock features")
	}
}
