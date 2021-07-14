package config

import (
	"strings"
)

// Split a string on commas and trim each line
func SplitTrim(s string) []string {
	res := strings.Split(s, ",")
	for i, v := range res {
		res[i] = strings.Trim(v, " \n\t")
	}
	return res
}
