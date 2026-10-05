package extract

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	j := Check(Job{Role: "Dev", ApplyEmail: "jobs@acme.io."}, "send CV to jobs@acme.io.")
	if j.ApplyEmail != "jobs@acme.io" || !j.EmailVerified {
		t.Fatalf("%+v", j)
	}
	j = Check(Job{Role: "Dev"}, "apply: careers@acme.io")
	if j.ApplyEmail != "careers@acme.io" {
		t.Fatalf("missed email in text: %+v", j)
	}
	j = Check(Job{Role: "Dev", ApplyEmail: "ceo@other.com"}, "apply: careers@acme.io")
	if j.EmailVerified || !strings.Contains(strings.Join(j.Warnings, ";"), "not written in the pasted text") {
		t.Fatalf("invented email not flagged: %+v", j)
	}
	j = Check(Job{Role: "Dev", ApplyEmail: "not-an-email"}, "")
	if j.ApplyEmail != "" {
		t.Fatalf("invalid email kept: %+v", j)
	}
	j = Check(Job{Role: "Dev"}, "a@x.io or b@y.io")
	if j.ApplyEmail != "" || !strings.Contains(strings.Join(j.Warnings, ";"), "several emails") {
		t.Fatalf("ambiguous emails: %+v", j)
	}
}
