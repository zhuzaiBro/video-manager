package platform

import (
	"context"
	"fmt"
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

func TestInterviewHomeUsesExplicitRoleAssignment(t *testing.T) {
	hotID := "11111111-1111-4111-8111-111111111111"
	extraID := "33333333-3333-4333-8333-333333333333"
	contractID := "f1a2b3c4-d5e6-4f78-9abc-def010000001"
	designID := "22222222-2222-4222-8222-222222222222"
	source := &fakeSource{tables: map[string][]map[string]any{
		"interview_tags": {
			{"id": "tag-hot", "name": "热门", "slug": "hot", "sort": 0},
			{"id": "tag-web3", "name": "Web3", "slug": "web3", "sort": 1},
		},
		"interview_collection_tags": {
			{"collection_id": hotID, "tag_id": "tag-hot"},
			{"collection_id": contractID, "tag_id": "tag-web3"},
		},
		"interview_collections": {
			{"id": hotID, "title": "Go 热门", "sort": 1},
			{"id": extraID, "title": "程序员加选题", "sort": 2},
			{"id": contractID, "title": "智能合约面试题", "sort": 3},
			{"id": designID, "title": "设计作品集", "sort": 4},
		},
		"interview_question": {
			{"id": "q-hot", "collection_id": hotID},
			{"id": "q-extra", "collection_id": extraID},
			{"id": "q-contract", "collection_id": contractID},
			{"id": "q-design", "collection_id": designID},
		},
		"interview_collection_roles": {
			{"collection_id": extraID, "role_slug": "programmer"},
			{"collection_id": designID, "role_slug": "design"},
		},
	}}
	service := NewService(source, nil, "postgres")

	home, err := service.InterviewHome(context.Background(), Caller{}, "", "programmer")
	if err != nil {
		t.Fatal(err)
	}
	if home["effectiveTag"] != "hot" {
		t.Fatalf("effectiveTag = %#v", home["effectiveTag"])
	}
	if got := collectionTitles(home["collections"]); !sameTitles(got, []string{"Go 热门", "程序员加选题"}) {
		t.Fatalf("programmer default = %#v", got)
	}
	counts := home["questionCounts"].(map[string]int)
	if counts[hotID] != 1 || counts[extraID] != 1 || counts[contractID] != 0 || counts[designID] != 0 {
		t.Fatalf("programmer counts = %#v", counts)
	}

	beforeWeb3 := len(source.queries)
	web3, err := service.InterviewHome(context.Background(), Caller{}, "web3", "programmer")
	if err != nil {
		t.Fatal(err)
	}
	if web3["effectiveTag"] != "web3" {
		t.Fatalf("web3 effectiveTag = %#v", web3["effectiveTag"])
	}
	if got := collectionTitles(web3["collections"]); !sameTitles(got, []string{"智能合约面试题"}) {
		t.Fatalf("programmer web3 = %#v", got)
	}
	for _, query := range source.queries[beforeWeb3:] {
		if query.Table == "interview_collection_roles" {
			t.Fatal("web3 tag lookup used a job assignment")
		}
	}

	beforeDesign := len(source.queries)
	design, err := service.InterviewHome(context.Background(), Caller{}, "web3", "design")
	if err != nil {
		t.Fatal(err)
	}
	if got := collectionTitles(design["collections"]); !sameTitles(got, []string{"设计作品集"}) {
		t.Fatalf("design = %#v", got)
	}
	if design["effectiveTag"] != "" || len(design["tags"].([]map[string]any)) != 0 {
		t.Fatalf("design reused technical tags: %#v", design)
	}
	designCounts := design["questionCounts"].(map[string]int)
	if designCounts[designID] != 1 || len(designCounts) != 1 {
		t.Fatalf("design counts = %#v", designCounts)
	}
	queriedRoles := false
	for _, query := range source.queries[beforeDesign:] {
		if query.Table == "interview_tags" || query.Table == "interview_collection_tags" {
			t.Fatalf("design read technical tags: %#v", query)
		}
		if query.Table == "interview_collection_roles" {
			queriedRoles = true
		}
	}
	if !queriedRoles {
		t.Fatal("design returned without reading interview_collection_roles")
	}

	for _, role := range []string{"cross-border-ops", "crypto-bd", "cross-border-bd", "crypto-ops"} {
		other, err := service.InterviewHome(context.Background(), Caller{}, "hot", role)
		if err != nil {
			t.Fatal(err)
		}
		if got := collectionTitles(other["collections"]); len(got) != 0 {
			t.Fatalf("%s leaked collections: %#v", role, got)
		}
		if len(other["questionCounts"].(map[string]int)) != 0 || other["effectiveTag"] != "" {
			t.Fatalf("%s leaked metadata: %#v", role, other)
		}
	}
}

func TestInterviewHomeKeepsProgrammerWhenRoleTableIsMissing(t *testing.T) {
	hotID := "11111111-1111-4111-8111-111111111111"
	source := &missingRoleTable{fakeSource: &fakeSource{tables: map[string][]map[string]any{
		"interview_tags":            {{"id": "tag-hot", "name": "热门", "slug": "hot"}},
		"interview_collection_tags": {{"collection_id": hotID, "tag_id": "tag-hot"}},
		"interview_collections":     {{"id": hotID, "title": "Go 热门"}},
		"interview_question":        {{"id": "q-hot", "collection_id": hotID}},
	}}}
	service := NewService(source, nil, "postgres")
	home, err := service.InterviewHome(context.Background(), Caller{}, "", "programmer")
	if err != nil {
		t.Fatal(err)
	}
	if got := collectionTitles(home["collections"]); !sameTitles(got, []string{"Go 热门"}) {
		t.Fatalf("programmer default = %#v", got)
	}
	design, err := service.InterviewHome(context.Background(), Caller{}, "", "design")
	if err != nil {
		t.Fatal(err)
	}
	if len(collectionTitles(design["collections"])) != 0 {
		t.Fatalf("missing table did not stay empty: %#v", design["collections"])
	}
}

type missingRoleTable struct {
	fakeSource *fakeSource
}

func (m missingRoleTable) Select(ctx context.Context, caller Caller, query Query) (Result, error) {
	if query.Table == "interview_collection_roles" {
		return Result{}, fmt.Errorf(`ERROR: relation "interview_collection_roles" does not exist (SQLSTATE 42P01)`)
	}
	return m.fakeSource.Select(ctx, caller, query)
}

func (m missingRoleTable) Write(ctx context.Context, caller Caller, method string, query Query, payload any) (Result, error) {
	return m.fakeSource.Write(ctx, caller, method, query, payload)
}

func collectionTitles(value any) []string {
	rows, _ := value.([]map[string]any)
	titles := make([]string, 0, len(rows))
	for _, row := range rows {
		titles = append(titles, stringField(row, "title"))
	}
	return titles
}

func sameTitles(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, title := range got {
		seen[title]++
	}
	for _, title := range want {
		seen[title]--
		if seen[title] < 0 {
			return false
		}
	}
	return true
}
