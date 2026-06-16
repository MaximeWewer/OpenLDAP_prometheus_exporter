package csn

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	wantUnix := time.Date(2026, 6, 12, 9, 2, 49, 0, time.UTC).Unix()

	tests := []struct {
		name    string
		in      string
		wantSID string
		wantErr bool
	}{
		{name: "microsecond precision", in: "20260612090249.123456Z#000000#000#000000", wantSID: "000"},
		{name: "single fractional digit", in: "20260612090249.0Z#000000#001#000000", wantSID: "001"},
		{name: "no fractional part", in: "20260612090249Z#000000#002#000000", wantSID: "002"},
		{name: "too few components", in: "20260612090249.0Z#000000", wantErr: true},
		{name: "missing Z", in: "20260612090249.0#000000#000#000000", wantErr: true},
		{name: "garbage timestamp", in: "notatimestampZ#000000#000#000000", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, sid, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sid != tt.wantSID {
				t.Errorf("server ID = %q, want %q", sid, tt.wantSID)
			}
			// Whole-second value must match regardless of fractional precision.
			if ts.Unix() != wantUnix {
				t.Errorf("unix = %d, want %d", ts.Unix(), wantUnix)
			}
		})
	}
}
