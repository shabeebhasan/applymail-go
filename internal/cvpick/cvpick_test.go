package cvpick

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dir(t *testing.T, names ...string) []CV {
	d := t.TempDir()
	for _, n := range names {
		os.WriteFile(filepath.Join(d, n), []byte("%PDF"), 0o644)
	}
	cvs, err := Load(d)
	if err != nil {
		t.Fatal(err)
	}
	return cvs
}

var all = []string{"Me_Resume.pdf", "Me_Master_CV.pdf", "Me_Resume_agentic-rag.pdf", "Me_Resume_healthcare-saas.pdf",
	"Me_Resume_mobile-saas-ai-payments.pdf", "Me_Resume_ocr-backend-automation-ai.pdf", "Me_Resume_fullstack-prod.pdf"}

func TestPicksByFocus(t *testing.T) {
	cvs := dir(t, all...)
	cases := map[string]string{
		"Senior AI engineer to build RAG and agentic workflows with LangGraph and pgvector": "agentic-rag",
		"React Native developer for iOS and Android app with Stripe subscriptions":          "mobile-saas-ai-payments",
		"HIPAA compliant patient portal for a healthcare SaaS":                              "healthcare-saas",
		"Invoice OCR and document extraction pipeline in Python":                            "ocr-backend-automation-ai",
		"Senior full-stack engineer, React and Next.js, production scale":                   "fullstack-prod",
	}
	for job, want := range cases {
		got := Best(cvs, "", job)
		if !strings.Contains(got.CV.Path, want) {
			t.Errorf("%q -> %s (score %d %v), want %s", job, filepath.Base(got.CV.Path), got.Score, got.Matched, want)
		}
	}
}

func TestRoleTitleWins(t *testing.T) {
	cvs := dir(t, append(all, "Me_Resume_api-integration.pdf")...)
	got := Best(cvs, "React Native Developer (contract)",
		"Build our patient app for iOS and Android: appointments, Stripe payments, API integration")
	if !strings.Contains(got.CV.Path, "mobile") {
		t.Fatalf("got %s %v", filepath.Base(got.CV.Path), got.Matched)
	}
}

func TestNoOverlapGivesGeneralResume(t *testing.T) {
	got := Best(dir(t, all...), "Barista", "Barista needed for weekend shifts")
	if filepath.Base(got.CV.Path) != "Me_Resume.pdf" {
		t.Fatalf("got %s", got.CV.Path)
	}
}

func TestWholeWordsOnly(t *testing.T) {
	if contains("we sell headsets", "ads") || contains("magic tricks", "ai") {
		t.Fatal("matched inside a word")
	}
	if !contains("ai/ml engineer", "ai") {
		t.Fatal("missed a whole word")
	}
}
