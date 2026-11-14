package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBenchmarkRequestValidation(t *testing.T) {
	const ok = `{"commit_sha":"6e7b16f","scenario":"f1","target_rps":5000,"achieved_rps":1,"p50_ms":1,"p95_ms":2,"p99_ms":3,"error_rate":0}`
	parse := func(body string) BenchmarkRequest {
		var r BenchmarkRequest
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}

	r := parse(ok)
	if errs := Validate(&r); errs != nil {
		t.Fatalf("valid request rejected: %v", errs)
	}
	if b := r.ToDomain(); b.Branch != "main" || b.TargetRPS != 5000 {
		t.Fatalf("defaults not applied: %+v", b)
	}

	bad := map[string]string{
		"non-hex sha":               strings.Replace(ok, "6e7b16f", "zzzzzzz", 1),
		"short sha":                 strings.Replace(ok, "6e7b16f", "6e7b1", 1),
		"error rate > 1":            strings.Replace(ok, `"error_rate":0`, `"error_rate":1.5`, 1),
		"negative p99":              strings.Replace(ok, `"p99_ms":3`, `"p99_ms":-1`, 1),
		"missing p99":               strings.Replace(ok, `"p99_ms":3,`, ``, 1),
		"blank scenario":            strings.Replace(ok, `"f1"`, `"  "`, 1),
		"raw_summary not an object": strings.Replace(ok, `"error_rate":0`, `"error_rate":0,"raw_summary":[1]`, 1),
	}
	for name, body := range bad {
		r := parse(body)
		if Validate(&r) == nil {
			t.Errorf("%s: accepted %s", name, body)
		}
	}
}
