package plan

import (
	"reflect"
	"testing"
)

func names(ops []Op) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.String()
	}
	return out
}

func TestService(t *testing.T) {
	d := Desired{Service: "web", Hash: "h2", Names: []string{"p-web-1", "p-web-2"}}
	tests := []struct {
		name   string
		actual []Actual
		opts   Options
		want   []string
	}{
		{"fresh project", nil, Options{}, []string{"create p-web-1", "create p-web-2"}},
		{"up to date", []Actual{
			{Name: "p-web-1", Service: "web", Number: 1, Hash: "h2", Running: true},
			{Name: "p-web-2", Service: "web", Number: 2, Hash: "h2", Running: true},
		}, Options{}, []string{"keep p-web-1", "keep p-web-2"}},
		{"hash changed", []Actual{
			{Name: "p-web-1", Service: "web", Number: 1, Hash: "h1", Running: true},
		}, Options{}, []string{"recreate p-web-1", "create p-web-2"}},
		{"hash changed but no-recreate", []Actual{
			{Name: "p-web-1", Service: "web", Number: 1, Hash: "h1", Running: false},
		}, Options{NoRecreate: true}, []string{"start p-web-1", "create p-web-2"}},
		{"force recreate", []Actual{
			{Name: "p-web-1", Service: "web", Number: 1, Hash: "h2", Running: true},
			{Name: "p-web-2", Service: "web", Number: 2, Hash: "h2", Running: true},
		}, Options{ForceRecreate: true}, []string{"recreate p-web-1", "recreate p-web-2"}},
		{"stopped container started", []Actual{
			{Name: "p-web-1", Service: "web", Number: 1, Hash: "h2"},
		}, Options{}, []string{"start p-web-1", "create p-web-2"}},
		{"scale down and ignore other services", []Actual{
			{Name: "p-web-3", Service: "web", Number: 3, Hash: "h2", Running: true},
			{Name: "p-web-1", Service: "web", Number: 1, Hash: "h2", Running: true},
			{Name: "p-web-2", Service: "web", Number: 2, Hash: "h2", Running: true},
			{Name: "p-db-1", Service: "db", Number: 1, Hash: "x", Running: true},
		}, Options{}, []string{"keep p-web-1", "keep p-web-2", "remove p-web-3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := names(Service(d, tt.actual, tt.opts)); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestRecreateCarriesRunningState(t *testing.T) {
	ops := Service(Desired{Service: "a", Hash: "new", Names: []string{"a1"}},
		[]Actual{{Name: "a1", Service: "a", Hash: "old", Running: true}}, Options{})
	if ops[0].Action != Recreate || !ops[0].Running {
		t.Fatalf("unexpected op %+v", ops[0])
	}
	if !Changed(ops) || Changed([]Op{{Action: Keep}}) {
		t.Fatal("Changed misreports")
	}
}

func TestOrphans(t *testing.T) {
	got := Orphans([]string{"web"}, []Actual{{Name: "a", Service: "web"}, {Name: "b", Service: "old"}})
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestOrder(t *testing.T) {
	deps := map[string][]string{
		"web":    {"api", "cache"},
		"api":    {"db"},
		"db":     nil,
		"cache":  nil,
		"worker": {"db", "missing"},
	}
	got, err := Order(deps, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cache", "db", "api", "web", "worker"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	rev, _ := Order(deps, true)
	if rev[0] != "worker" || rev[len(rev)-1] != "cache" {
		t.Fatalf("reverse order wrong: %v", rev)
	}
	if _, err := Order(map[string][]string{"a": {"b"}, "b": {"a"}}, false); err == nil {
		t.Fatal("expected cycle error")
	}
}
