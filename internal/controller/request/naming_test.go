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

package request

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

func TestReplicationName(t *testing.T) {
	a := ReplicationName("Secret", "db-creds")
	if !strings.HasPrefix(a, "secret-db-creds-") {
		t.Errorf("unexpected name %q", a)
	}
	if a != ReplicationName("Secret", "db-creds") {
		t.Error("name must be deterministic")
	}
	if a == ReplicationName("ConfigMap", "db-creds") {
		t.Error("different kinds must yield different names")
	}

	long := strings.Repeat("a", 260)
	n := ReplicationName("Secret", long)
	if len(n) > 253 {
		t.Errorf("name exceeds DNS subdomain limit: %d", len(n))
	}
	if msgs := validation.IsDNS1123Subdomain(n); len(msgs) > 0 {
		t.Errorf("invalid name %q: %v", n, msgs)
	}
	if n == ReplicationName("Secret", long+"b"[:1]) {
		t.Error("truncated names must stay distinct via hash suffix")
	}
}

func TestLabelSafeValue(t *testing.T) {
	if got := labelSafeValue("short"); got != "short" {
		t.Errorf("short names must pass through, got %q", got)
	}
	long := strings.Repeat("x", 100)
	v := labelSafeValue(long)
	if len(v) > 63 {
		t.Errorf("label value too long: %d", len(v))
	}
	if msgs := validation.IsValidLabelValue(v); len(msgs) > 0 {
		t.Errorf("invalid label value %q: %v", v, msgs)
	}
	if v == labelSafeValue(long+"y") {
		t.Error("distinct long names must yield distinct label values")
	}
}
