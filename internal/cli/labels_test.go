package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestReconcileLabelIDs(t *testing.T) {
	tests := []struct {
		name    string
		current []string
		valid   []string
		desired []string
		want    labelReconciliation
		wantErr string
	}{
		{
			name:    "deduplicates and orders mutations",
			current: []string{"B", "A", "A"},
			valid:   []string{"A", "B", "C", "D"},
			desired: []string{"D", "C", "C"},
			want:    labelReconciliation{Additions: []string{"C", "D"}, Removals: []string{"A", "B"}},
		},
		{
			name:    "no op",
			current: []string{"B", "A"},
			valid:   []string{"A", "B"},
			desired: []string{"A", "B", "B"},
			want:    labelReconciliation{},
		},
		{
			name:    "empty desired clears current labels",
			current: []string{"B", "A"},
			valid:   []string{"A", "B"},
			want:    labelReconciliation{Removals: []string{"A", "B"}},
		},
		{
			name:    "invalid labels are rejected before a plan",
			current: []string{"A"},
			valid:   []string{"A"},
			desired: []string{"A", "missing"},
			wantErr: `label ID "missing" is not defined on this card's board`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := reconcileLabelIDs(tt.current, tt.valid, tt.desired)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("reconciliation = %#v, want %#v", got, tt.want)
			}
		})
	}
}
