package instance

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PillowPillow/den/v2/internal/registry"
)

func TestLookupStates(t *testing.T) {
	for _, c := range []struct {
		name           string
		record, listed bool
		want           State
	}{
		{"absent", false, false, Absent},
		{"live", true, true, Live},
		{"recorded", true, false, Recorded},
		{"foreign", false, true, Foreign},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.record {
				if err := registry.Write(f.home, registry.Record{Instance: "am", Nest: "am"}); err != nil {
					t.Fatal(err)
				}
			}
			if c.listed {
				f.live("am")
			}
			state, r, err := Lookup(context.Background(), f.deps(), "am")
			if err != nil {
				t.Fatal(err)
			}
			if state != c.want {
				t.Errorf("state %v, want %v", state, c.want)
			}
			if (r != nil) != c.record {
				t.Errorf("record %+v, want one: %t", r, c.record)
			}
		})
	}
}

func TestLookupRefusesARecordFromAnotherSchema(t *testing.T) {
	for _, listed := range []bool{false, true} {
		f := newFixture(t)
		path := writeFile(t, filepath.Join(registry.Dir(f.home), "am.yaml"), "schema: 3\ninstance: am\n")
		if listed {
			f.live("am")
		}
		_, _, err := Lookup(context.Background(), f.deps(), "am")
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("listed=%t: error %v, want a refusal naming %s", listed, err, path)
		}
	}
}
