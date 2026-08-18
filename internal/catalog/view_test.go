package catalog

import (
	"strings"
	"testing"
)

func viewWithParams(params ...Param) View {
	return View{Group: "argo", Name: "syncstatus", Params: params}
}

// Both modes bind through the same code, so a required parameter cannot be
// missing in one and silently defaulted in the other.
func TestBindRejectsAMissingRequiredParam(t *testing.T) {
	v := viewWithParams(Param{Name: "app", Required: true})

	if _, err := v.Bind(nil); err == nil {
		t.Fatal("want an error")
	} else if !strings.Contains(err.Error(), "APP") {
		t.Errorf("error does not name the parameter: %v", err)
	}
}

func TestBindTakesPositionalsAndDefaults(t *testing.T) {
	v := viewWithParams(
		Param{Name: "app", Required: true},
		Param{Name: "component", Default: "application-controller"},
	)

	got, err := v.Bind([]string{"root"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("app") != "root" {
		t.Errorf("app = %q", got.Get("app"))
	}
	if got.Get("component") != "application-controller" {
		t.Errorf("component = %q", got.Get("component"))
	}
}

func TestBindAcceptsAnOptionalParamBeingAbsent(t *testing.T) {
	v := viewWithParams(Param{Name: "app"})

	got, err := v.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("app") != "" {
		t.Errorf("app = %q, want empty", got.Get("app"))
	}
}

func TestLookupAndActionNames(t *testing.T) {
	v := View{Actions: []Action{{Name: "pause"}, {Name: "unpause"}}}

	if _, ok := v.Lookup("pause"); !ok {
		t.Error("pause not found")
	}
	if _, ok := v.Lookup("nope"); ok {
		t.Error("found an action that does not exist")
	}
	if got := strings.Join(v.ActionNames(), ","); got != "pause,unpause" {
		t.Errorf("names = %q, want declaration order", got)
	}
}

// Actions operate on the objects behind the rows, never on the rendered cells,
// which are truncated for display.
func TestSourcesSkipsRowsWithoutOne(t *testing.T) {
	var tbl Table
	tbl.Add("a", "row one")
	tbl.Add(nil, "row two")
	tbl.Add("c", "row three")

	if got := tbl.Sources(); len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("sources = %v", got)
	}
}

func TestMergeKeepsGroupOrderAndDescriptions(t *testing.T) {
	a := NewRegistry()
	a.Describe("argo", "ArgoCD")
	a.Add(View{Group: "argo", Name: "apps"})

	b := NewRegistry()
	b.Describe("pods", "Pods")
	b.Add(View{Group: "pods", Name: "failed"})

	a.Merge(b)

	if got := strings.Join(a.Groups(), ","); got != "argo,pods" {
		t.Errorf("groups = %q", got)
	}
	if a.GroupShort("pods") != "Pods" {
		t.Errorf("lost the merged description")
	}
	if len(a.All()) != 2 {
		t.Errorf("got %d views", len(a.All()))
	}
}

// Registration order is what the user sees in the TUI list and in help, so it
// has to survive.
func TestRegistryKeepsRegistrationOrder(t *testing.T) {
	r := NewRegistry()
	r.Describe("argo", "ArgoCD")
	for _, name := range []string{"apps", "syncstatus", "logs"} {
		r.Add(View{Group: "argo", Name: name})
	}

	var got []string
	for _, v := range r.InGroup("argo") {
		got = append(got, v.Name)
	}
	if strings.Join(got, ",") != "apps,syncstatus,logs" {
		t.Errorf("order = %v", got)
	}
}

// Hidden views stay out of the list but remain reachable by id, which is how
// Navigate finds them.
func TestListedSkipsHiddenButGetFindsThem(t *testing.T) {
	r := NewRegistry()
	r.Describe("argo", "ArgoCD")
	r.Add(View{Group: "argo", Name: "apps"})
	r.Add(View{Group: "argo", Name: "syncstatus", Hidden: true})

	if got := r.Listed("argo"); len(got) != 1 || got[0].Name != "apps" {
		t.Errorf("listed = %v", got)
	}
	if _, ok := r.Get("argo.syncstatus"); !ok {
		t.Error("a hidden view is not reachable by id")
	}
	if _, ok := r.Get("argo.nope"); ok {
		t.Error("found a view that does not exist")
	}
}
