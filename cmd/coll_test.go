package cmd

import "testing"

// Two actions of one view must not claim the same flag; pflag would panic
// with a message that does not say which view is at fault.
func TestNoFlagCollisionsInTheRealRegistry(t *testing.T) {
	for _, v := range registry().All() {
		claimed := map[string]string{}
		for _, a := range v.Actions {
			for _, p := range a.Params {
				if owner, taken := claimed[p.Name]; taken {
					t.Errorf("%s: actions %q and %q both define --%s", v.ID(), owner, a.Name, p.Name)
				}
				claimed[p.Name] = a.Name
			}
		}
	}
}
