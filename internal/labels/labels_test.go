package labels

import (
	"reflect"
	"testing"
)

func TestContainerLabels(t *testing.T) {
	m := Meta{Project: "demo", WorkingDir: "C:/src", ConfigFiles: []string{"a.yaml", "b.yaml"}, Version: "1.0"}
	got := m.Container("web", 2, "abc")
	want := map[string]string{
		Project: "demo", Service: "web", Number: "2", OneOff: "False", ConfigHash: "abc",
		WorkingDir: "C:/src", ConfigFiles: "a.yaml,b.yaml", Version: "1.0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if oneoff := (Meta{Project: "p"}).Container("web", 0, "h"); oneoff[OneOff] != "True" || oneoff[Number] != "" {
		t.Fatalf("one-off labels wrong: %v", oneoff)
	}
}

func TestFlagsSortedAndMerge(t *testing.T) {
	got := Flags(Merge(map[string]string{"b": "1", "a": "x"}, map[string]string{"b": "2"}))
	want := []string{"-l", "a=x", "-l", "b=2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if ProjectFilter("demo") != "label=com.docker.compose.project=demo" {
		t.Fatal("unexpected filter")
	}
}
