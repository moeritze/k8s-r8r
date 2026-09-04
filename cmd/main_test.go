/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"maps"
	"testing"
)

// TestStringMapFlagAccumulates covers the two equivalent spellings of
// --discovery-setting (repeated and comma-separated) and the value shapes the
// providers care about.
func TestStringMapFlagAccumulates(t *testing.T) {
	cases := []struct {
		name string
		set  []string
		want map[string]string
	}{
		{
			name: "repeated flag",
			set:  []string{"namespace=capi-system", "extra=on"},
			want: map[string]string{"namespace": "capi-system", "extra": "on"},
		},
		{
			name: "comma separated",
			set:  []string{"namespace=capi-system,extra=on"},
			want: map[string]string{"namespace": "capi-system", "extra": "on"},
		},
		{
			name: "last entry wins for a repeated key",
			set:  []string{"namespace=first", "namespace=second"},
			want: map[string]string{"namespace": "second"},
		},
		{
			name: "empty value is a real value, not a malformed entry",
			set:  []string{"namespace="},
			want: map[string]string{"namespace": ""},
		},
		{
			name: "surrounding whitespace and empty entries are ignored",
			set:  []string{" namespace = capi-system ,, "},
			want: map[string]string{"namespace": "capi-system"},
		},
		{
			name: "only the first = separates",
			set:  []string{"key=a=b"},
			want: map[string]string{"key": "a=b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got stringMapFlag
			for _, v := range tc.set {
				if err := got.Set(v); err != nil {
					t.Fatalf("Set(%q) returned an unexpected error: %v", v, err)
				}
			}
			if !maps.Equal(got, tc.want) {
				t.Errorf("settings = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestStringMapFlagRejectsMalformed pins the deliberate difference from
// stringListFlag: a setting that cannot be parsed fails startup instead of
// being dropped into an indistinguishable "unset".
func TestStringMapFlagRejectsMalformed(t *testing.T) {
	for _, v := range []string{"namespace", "=capi-system", " =x", "ok=1,broken"} {
		t.Run(v, func(t *testing.T) {
			var m stringMapFlag
			if err := m.Set(v); err == nil {
				t.Fatalf("Set(%q) succeeded, want an error; got %v", v, m)
			}
		})
	}
}

// TestStringMapFlagString checks the flag renders deterministically, which is
// what --help and the flag package's default-value detection read.
func TestStringMapFlagString(t *testing.T) {
	var empty stringMapFlag
	if s := empty.String(); s != "" {
		t.Errorf("empty flag String() = %q, want \"\"", s)
	}

	m := stringMapFlag{"b": "2", "a": "1"}
	if s := m.String(); s != "a=1,b=2" {
		t.Errorf("String() = %q, want \"a=1,b=2\"", s)
	}
}
