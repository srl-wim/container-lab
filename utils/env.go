// Copyright 2020 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

package utils

import "reflect"

// convertEnvs convert env variables passed as a map to a list of them
func ConvertEnvs(m map[string]string) []string {
	s := make([]string, 0, len(m))
	for k, v := range m {
		s = append(s, k+"="+v)
	}
	return s
}

func mapify(i interface{}) (map[string]interface{}, bool) {
	value := reflect.ValueOf(i)
	if value.Kind() == reflect.Map {
		m := map[string]interface{}{}
		for _, k := range value.MapKeys() {
			m[k.String()] = value.MapIndex(k).Interface()
		}
		return m, true
	}
	return map[string]interface{}{}, false
}

// merge all dictionaries and return a new dictionary
// recursively if matching keys are both dictionaries
func MergeMaps(dicts ...map[string]interface{}) map[string]interface{} {
	res := make(map[string]interface{})
	for _, m := range dicts {
		if m == nil {
			continue
		}
		for k, v := range m {

			if v0, ok := res[k]; ok {
				// Recursive merging if res[k] exists (and both are dicts)
				t0, ok0 := mapify(v0)
				t1, ok1 := mapify(v)
				if ok0 && ok1 {
					res[k] = MergeMaps(t0, t1)
					continue
				}
			}
			res[k] = v
		}
	}
	return res
}

// merge all string maps and return a new map
// maps that are passed for merging will not be changed
func MergeStringMaps(maps ...map[string]string) map[string]string {
	res := make(map[string]string)
	for _, m := range maps {
		if m == nil {
			continue
		}
		for k, v := range m {
			res[k] = v
		}
	}
	if len(res) == 0 {
		return nil
	}
	return res
}

// does a slice contain a string
func StringInSlice(slice []string, val string) (int, bool) {
	for i, item := range slice {
		if item == val {
			return i, true
		}
	}
	return -1, false
}
