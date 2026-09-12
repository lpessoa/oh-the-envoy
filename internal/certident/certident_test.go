package certident

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		xfcc   string
		want   Identity
		wantOK bool
	}{
		{
			name:   "well-formed header",
			xfcc:   `Hash=ab12cd34;Subject="CN=alice"`,
			want:   Identity{CN: "alice", Fingerprint: "ab12cd34"},
			wantOK: true,
		},
		{
			name:   "empty header",
			xfcc:   "",
			want:   Identity{},
			wantOK: false,
		},
		{
			name:   "subject missing CN",
			xfcc:   `Hash=ab12cd34;Subject="O=demo"`,
			want:   Identity{},
			wantOK: false,
		},
		{
			name:   "subject with multiple DN attributes",
			xfcc:   `Hash=ab12cd34;Subject="CN=bob,O=demo,C=US"`,
			want:   Identity{CN: "bob", Fingerprint: "ab12cd34"},
			wantOK: true,
		},
		{
			name:   "no subject field at all",
			xfcc:   `Hash=ab12cd34`,
			want:   Identity{},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.xfcc)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
