package lens

import (
	"reflect"
	"testing"
)

func TestAllReturnsStableRegistry(t *testing.T) {
	got := All()
	wantIDs := []ID{Defensive, Minimalist, Architect, Performance}

	if len(got) != len(wantIDs) {
		t.Fatalf("All() returned %d definitions, want %d", len(got), len(wantIDs))
	}

	for i, wantID := range wantIDs {
		if got[i].ID != wantID {
			t.Fatalf("All()[%d].ID = %q, want %q", i, got[i].ID, wantID)
		}
		if got[i].Name == "" {
			t.Fatalf("All()[%d].Name is empty", i)
		}
		if got[i].Description == "" {
			t.Fatalf("All()[%d].Description is empty", i)
		}
	}
}

func TestAllReturnsCopy(t *testing.T) {
	definitions := All()
	definitions[0].Name = "mutated"

	got, ok := Lookup(Defensive)
	if !ok {
		t.Fatal("Lookup(Defensive) returned false")
	}
	if got.Name == "mutated" {
		t.Fatal("All() returned registry storage instead of a copy")
	}
}

func TestIDsReturnsStableOrder(t *testing.T) {
	got := IDs()
	want := []ID{Defensive, Minimalist, Architect, Performance}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IDs() = %#v, want %#v", got, want)
	}
}

func TestLookup(t *testing.T) {
	got, ok := Lookup(Minimalist)
	if !ok {
		t.Fatal("Lookup(Minimalist) returned false")
	}
	if got.ID != Minimalist {
		t.Fatalf("Lookup(Minimalist).ID = %q, want %q", got.ID, Minimalist)
	}

	if _, ok := Lookup("unknown"); ok {
		t.Fatal("Lookup(unknown) returned true")
	}
}

func TestParseID(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    ID
		wantErr bool
	}{
		{name: "exact", raw: "defensive", want: Defensive},
		{name: "trim and lowercase", raw: " Performance ", want: Performance},
		{name: "empty", raw: " ", wantErr: true},
		{name: "unknown", raw: "wide", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseID(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ParseID() returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseID() returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseID() = %q, want %q", got, tt.want)
			}
		})
	}
}
