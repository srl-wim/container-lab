package utils

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func expectMaps(t *testing.T, v map[string]interface{}, m ...map[string]interface{}) {
	d := MergeMaps(m...)
	if !cmp.Equal(d, v) {
		t.Errorf("err %v, expected %v", d, v)
	}
}

func TestMergeMaps(t *testing.T) {
	MergeMaps(nil, nil)
	d1 := map[string]interface{}{
		"t": "1",
	}
	d2 := map[string]interface{}{
		"t":  "2",
		"t2": "1",
	}
	expectMaps(t, d1, nil, d1)
	expectMaps(t, d1, d1, d1)
	expectMaps(t, d1, d1, nil)
	expectMaps(t, d2, d1, d2)
	expectMaps(t, map[string]interface{}{
		"t":  "1",
		"t2": "1",
	}, d2, d1)
}

func TestMergeMapsRecursive(t *testing.T) {
	d0 := map[string]interface{}{
		"a": "1",
	}
	d1 := map[string]interface{}{
		"a": "11",
		"b": "2",
	}
	r0 := map[string]interface{}{
		"r":  d0,
		"r1": "1",
	}
	r1 := map[string]interface{}{
		"r":  d1,
		"r2": "2",
	}
	r3 := map[string]interface{}{
		"r":  "00",
		"r2": "0",
	}

	exp0 := map[string]interface{}{
		"a": "1",
		"b": "2",
	}
	exp1 := d1

	// all simple vars... second overwrites
	expectMaps(t, exp0, d1, d0)
	expectMaps(t, exp1, d0, d1)

	// r are both dicts... recursive on r... same inner result as the previous
	expectMaps(t, map[string]interface{}{"r": exp0, "r1": "1", "r2": "2"}, r1, r0)
	expectMaps(t, map[string]interface{}{"r": exp1, "r1": "1", "r2": "2"}, r0, r1)

	// one is NOT a dict... second overwrites
	expectMaps(t, map[string]interface{}{"r": "00", "r2": "0"}, r1, r3)
	expectMaps(t, map[string]interface{}{"r": exp1, "r2": "2"}, r3, r1)
}

func TestMergeStringMaps(t *testing.T) {
	d0 := map[string]string{
		"a": "1",
	}
	d1 := map[string]string{
		"a": "11",
		"b": "2",
	}

	expect := func(m1, m2 map[string]string, v string) {
		d := MergeStringMaps(m1, m2)
		if fmt.Sprintf("%v", d) != v {
			t.Errorf("err %v, expected %s", d, v)
		}
	}
	// all simple vars... second overwrites
	expect(d1, d0, "map[a:1 b:2]")
	expect(d0, d1, "map[a:11 b:2]")
}
