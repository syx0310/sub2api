package appversion

import "testing"

func TestCompare(t *testing.T) {
	ordered := []string{"0.2.19-rc.1", "v0.2.19", "0.2.19p1-rc.1", "0.2.19p1", "0.2.19p2", "0.2.19p10", "0.2.19p999999999999999999999999", "0.2.20"}
	for i, a := range ordered {
		for j, b := range ordered {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			got, err := Compare(a, b)
			if err != nil || got != want {
				t.Errorf("Compare(%q,%q) = %d,%v; want %d", a, b, got, err, want)
			}
		}
	}
	for _, pair := range [][2]string{{" 0.2.19p1 ", "v0.2.19p1"}, {"0.2.19p1+abc", "0.2.19p1+def"}, {"v1.2", "1.2.0"}} {
		if got, err := Compare(pair[0], pair[1]); err != nil || got != 0 {
			t.Errorf("equivalent versions: %v, %d %v", pair, got, err)
		}
	}
}

func TestRejectInvalidVersions(t *testing.T) {
	for _, v := range []string{"", "dev", "0.2.19p", "0.2.19p0", "0.2.19p01", "0.2.19p-1", "0.2.19p1junk", "0.02.19p1", "0.2.19.1"} {
		if _, err := Normalize(v); err == nil {
			t.Errorf("accepted invalid version %q", v)
		}
		if _, err := Compare("0.2.19", v); err == nil {
			t.Errorf("compared invalid version %q", v)
		}
	}
	if got, err := Normalize(" 0.2.19p1-rc.1+build "); err != nil || got != "v0.2.19p1-rc.1+build" {
		t.Fatalf("Normalize = %q, %v", got, err)
	}
}
