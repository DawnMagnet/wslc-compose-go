package wslc

import "testing"

func TestParseCapabilities(t *testing.T) {
	tests := []struct {
		raw   string
		known bool
		floor bool // >= 3.0.3.0
	}{
		{"wslc version 3.0.3.0", true, true},
		{"wslc version 3.0.3.0\n", true, true},
		{"WSLc Version 3.0.3.0", true, true},
		{"wslc 3.0.1\n", true, false},
		{"wslc.exe version 3.0.1.0", true, false},
		{"wslc version 3.0.10.1", true, true},
		{"wslc version 3.1", true, true},
		{"wslc version 3.0.2.9", true, false},
		{"wslc version 3.0.3.0-rc1", true, false},
		{"wslc version 3.0.3.0-rc.1", true, false},
		{"wslc version 3.0.4.0-rc1", true, true},
		// WSL's own version and the Go toolchain must not win.
		{"WSL version 2.4.11.0\nwslc version 3.0.3.0\n", true, true},
		{"go version go1.24.0\nwslc version 3.0.1.0\n", true, false},
		{"version 1.2.3 but wslc version 3.0.3.0", true, true},
		{"3.0.3.0", false, false},
		{"", false, false},
		{"not a version", false, false},
		{"dev", false, false},
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
	if ParseCapabilities("wslc version 3.0.3.0-rc1").AtLeast("3.0.3.0-rc1") != true {
		t.Fatal("a pre-release is at least itself")
	}
	rc10 := ParseCapabilities("wslc version 3.0.3.0-rc10")
	rc2 := ParseCapabilities("wslc version 3.0.3.0-rc2")
	if !rc10.AtLeast("3.0.3.0-rc2") || rc2.AtLeast("3.0.3.0-rc10") {
		t.Fatal("rc10 must be newer than rc2")
	}
	if rc10.AtLeast(VerEndpoints) || rc2.AtLeast(VerEndpoints) {
		t.Fatal("pre-releases of 3.0.3.0 are below the release")
	}
}
