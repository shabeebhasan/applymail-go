// Package cvpick chooses the CV whose focus best matches a job. Each PDF's focus comes from
// its file name (Name_Resume_agentic-rag.pdf -> agentic, rag); words in the job are mapped onto
// those focus tags, and the CV with the most overlap wins. Plain code, so the choice is explainable.
package cvpick

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type CV struct {
	Path string
	Tags []string
}

type Pick struct {
	CV      CV
	Score   int
	Matched []string
}

// tag -> words in a job post that point to it
var synonyms = map[string][]string{
	"ai":          {"ai", "artificial intelligence", "llm", "gpt", "openai", "claude", "genai", "generative"},
	"rag":         {"rag", "retrieval", "vector", "embedding", "pgvector", "pinecone", "knowledge base", "semantic search"},
	"agentic":     {"agentic", "agent", "agents", "multi-agent", "langgraph", "crewai", "autonomous"},
	"agents":      {"agent", "agents", "agentic", "multi-agent", "langgraph", "crewai", "tool calling"},
	"mcp":         {"mcp", "model context protocol", "tool calling"},
	"automation":  {"automation", "automate", "workflow", "n8n", "zapier", "make.com", "integration"},
	"backend":     {"backend", "back-end", "api", "python", "django", "fastapi", "node", "go", "golang", "microservices", "postgres"},
	"saas":        {"saas", "multi-tenant", "subscription", "b2b", "platform", "startup"},
	"payments":    {"payments", "stripe", "billing", "checkout", "subscription", "fintech"},
	"healthcare":  {"healthcare", "health", "hipaa", "clinical", "medical", "patient", "ehr", "hl7", "fhir"},
	"mobile":      {"mobile", "react native", "ios", "android", "flutter", "expo", "swift", "kotlin"},
	"ocr":         {"ocr", "document", "extraction", "invoice", "pdf", "document ai", "idp"},
	"integration": {"integration", "integrations", "api", "webhook", "crm", "hubspot", "zoho", "salesforce"},
	"api":         {"api", "rest", "graphql", "integration", "webhook"},
	"fullstack":   {"full stack", "full-stack", "fullstack", "react", "next.js", "nextjs", "typescript", "frontend"},
	"prod":        {"production", "senior", "scale", "lead"},
	"coaching":    {"coaching", "coach", "education", "edtech", "learning"},
	"data":        {"data engineer", "data engineering", "etl", "elt", "pipeline", "pipelines", "spark", "pyspark", "databricks", "airflow", "dbt", "warehouse", "snowflake", "redshift", "bigquery", "sql", "kafka", "analytics engineer"},
	"aws":         {"aws", "lambda", "s3", "redshift", "emr", "glue", "athena", "cloud"},
	"cv":          {"computer vision", "vision", "image", "opencv", "yolo"},
}

var tagRE = regexp.MustCompile(`(?i)_resume_(.+)\.pdf$`)

// Load lists the PDFs in dir. CVs without focus tags (the general resume) get no tags.
func Load(dir string) ([]CV, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.pdf"))
	if err != nil {
		return nil, err
	}
	var out []CV
	for _, f := range files {
		cv := CV{Path: f}
		if m := tagRE.FindStringSubmatch(filepath.Base(f)); m != nil {
			cv.Tags = strings.Split(strings.ToLower(m[1]), "-")
		}
		out = append(out, cv)
	}
	if len(out) == 0 {
		return nil, os.ErrNotExist
	}
	return out, nil
}

func contains(text, word string) bool {
	re := regexp.MustCompile(`(?i)(^|[^a-z0-9])` + regexp.QuoteMeta(word) + `($|[^a-z0-9])`)
	return re.MatchString(text)
}

func hits(text string) map[string]bool {
	text = strings.ToLower(text)
	hit := map[string]bool{}
	for tag, words := range synonyms {
		for _, w := range words {
			if contains(text, w) {
				hit[tag] = true
				break
			}
		}
	}
	return hit
}

// Best returns the best CV for the job. A focus named in the role title counts double, so a
// "React Native Developer" gets the mobile CV even if the post also mentions APIs. Ties go to the
// higher share of matched tags; with no overlap at all it returns the general resume.
func Best(cvs []CV, title, jobText string) Pick {
	hit, inTitle := hits(title+" "+jobText), hits(title)
	var picks []Pick
	for _, cv := range cvs {
		p := Pick{CV: cv}
		for _, t := range cv.Tags {
			if hit[t] {
				p.Score++
				if inTitle[t] {
					p.Score++
				}
				p.Matched = append(p.Matched, t)
			}
		}
		picks = append(picks, p)
	}
	sort.SliceStable(picks, func(i, j int) bool {
		a, b := picks[i], picks[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		// prefer the higher share of matched tags, then the general resume over the master CV
		ra := float64(a.Score) / float64(max(1, len(a.CV.Tags)))
		rb := float64(b.Score) / float64(max(1, len(b.CV.Tags)))
		if ra != rb {
			return ra > rb
		}
		return generalRank(a.CV) < generalRank(b.CV)
	})
	return picks[0]
}

func generalRank(cv CV) int {
	name := strings.ToLower(filepath.Base(cv.Path))
	switch {
	case strings.HasSuffix(name, "_resume.pdf"):
		return 0
	case strings.Contains(name, "master"):
		return 2
	}
	return 1
}
