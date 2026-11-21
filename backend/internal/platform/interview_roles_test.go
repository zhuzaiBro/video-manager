package platform

import (
	"context"
	"strings"
	"testing"
)

func TestInterviewJobsUseTheRequestedNames(t *testing.T) {
	names := []string{"程序员", "设计", "跨境电商运营", "加密行业BD", "跨境电商BD", "加密行业运营"}
	if len(interviewJobs) != len(names) {
		t.Fatalf("jobs = %d", len(interviewJobs))
	}
	for i, name := range names {
		if interviewJobs[i]["name"] != name {
			t.Fatalf("job %d = %s", i, interviewJobs[i]["name"])
		}
	}
	job, ok := interviewJob("")
	if !ok || job["slug"] != "programmer" {
		t.Fatal("empty role should stay on programmer")
	}
	if _, ok := interviewJob("unknown"); ok {
		t.Fatal("unknown role was accepted")
	}
}

func TestInterviewHomeKeepsTechnicalCollectionsUnderProgrammer(t *testing.T) {
	source := &fakeSource{tables: map[string][]map[string]any{
		"interview_tags":            {{"id": "tag-1", "name": "Go", "slug": "go"}},
		"interview_collection_tags": {{"collection_id": "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10", "tag_id": "tag-1"}},
		"interview_collections":     {{"id": "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10", "title": "Go 基础"}},
		"interview_question":        {{"id": "q-1", "collection_id": "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"}},
	}}
	service := NewService(source, nil, "postgres")
	home, err := service.InterviewHome(context.Background(), Caller{}, "go", "programmer")
	if err != nil {
		t.Fatal(err)
	}
	collections := home["collections"].([]map[string]any)
	if len(collections) != 1 || collections[0]["title"] != "Go 基础" {
		t.Fatalf("programmer collections: %#v", collections)
	}
	counts := home["questionCounts"].(map[string]int)
	if counts["8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"] != 1 {
		t.Fatalf("count = %#v", counts)
	}
	for _, query := range source.queries {
		if query.Table == "interview_collections" && strings.Contains(query.Select, "cover_image") {
			t.Fatalf("collection select uses a missing column: %s", query.Select)
		}
	}

	design, err := service.InterviewHome(context.Background(), Caller{}, "go", "design")
	if err != nil {
		t.Fatal(err)
	}
	if len(design["collections"].([]map[string]any)) != 0 {
		t.Fatalf("design leaked collections: %#v", design["collections"])
	}
	if len(design["questionCounts"].(map[string]int)) != 0 {
		t.Fatalf("design leaked counts: %#v", design["questionCounts"])
	}
	jobs := design["jobs"].([]map[string]string)
	if jobs[2]["name"] != "跨境电商运营" {
		t.Fatalf("jobs = %#v", jobs)
	}

	if _, err := service.InterviewHome(context.Background(), Caller{}, "", "not-a-job"); err == nil {
		t.Fatal("unknown job was accepted")
	}
}
