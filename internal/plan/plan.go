// Package plan computes the reconciliation steps that bring a service's
// actual containers in line with the desired Compose configuration. It is
// pure: inputs are plain values, outputs are ordered operations.
package plan

import (
	"fmt"
	"slices"
)

// Action is a single reconciliation step kind.
type Action int

// Actions, in the order they are typically applied.
const (
	Keep     Action = iota // container is up to date and running
	Start                  // container is up to date but stopped
	Create                 // no container yet
	Recreate               // configuration drifted (or forced): remove then create
	Remove                 // surplus replica or orphan
)

var actionNames = [...]string{"keep", "start", "create", "recreate", "remove"}

func (a Action) String() string { return actionNames[a] }

// Actual is the observed state of one container.
type Actual struct {
	Name    string
	Service string
	Number  int
	Hash    string
	Running bool
}

// Desired describes what one service should look like.
type Desired struct {
	Service string
	Hash    string
	Names   []string // container name per replica; index i is number i+1
}

// Options tunes recreation behavior.
type Options struct {
	ForceRecreate bool // recreate even when hashes match
	NoRecreate    bool // never recreate, even when hashes differ
}

// Op is one step for one container.
type Op struct {
	Action  Action
	Service string
	Name    string
	Number  int
	Running bool // whether the existing container is running (Recreate/Remove)
}

func (o Op) String() string { return fmt.Sprintf("%s %s", o.Action, o.Name) }

// Service plans one service. actual may contain containers of other services;
// they are ignored. Surplus replicas are removed after the kept ones.
func Service(d Desired, actual []Actual, o Options) []Op {
	byName := map[string]Actual{}
	var mine []Actual
	for _, a := range actual {
		if a.Service == d.Service {
			byName[a.Name] = a
			mine = append(mine, a)
		}
	}
	ops := make([]Op, 0, len(d.Names))
	for i, name := range d.Names {
		op := Op{Service: d.Service, Name: name, Number: i + 1}
		cur, ok := byName[name]
		delete(byName, name)
		switch {
		case !ok:
			op.Action = Create
		case o.ForceRecreate || (cur.Hash != d.Hash && !o.NoRecreate):
			op.Action, op.Running = Recreate, cur.Running
		case cur.Running:
			op.Action, op.Running = Keep, true
		default:
			op.Action = Start
		}
		ops = append(ops, op)
	}
	slices.SortFunc(mine, func(a, b Actual) int { return a.Number - b.Number })
	for _, a := range mine {
		if _, surplus := byName[a.Name]; surplus {
			ops = append(ops, Op{Action: Remove, Service: a.Service, Name: a.Name, Number: a.Number, Running: a.Running})
		}
	}
	return ops
}

// Orphans returns containers whose service is not part of services.
func Orphans(services []string, actual []Actual) []Actual {
	var out []Actual
	for _, a := range actual {
		if !slices.Contains(services, a.Service) {
			out = append(out, a)
		}
	}
	return out
}

// Changed reports whether any op mutates state.
func Changed(ops []Op) bool {
	return slices.ContainsFunc(ops, func(o Op) bool { return o.Action != Keep })
}
