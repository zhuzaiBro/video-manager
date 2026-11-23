package platform

import (
	"net/url"
	"testing"
)

func TestAdminStudyQueryRejectsUnboundedInput(t *testing.T) {
	query, err := ParseAdminStudyQuery(url.Values{})
	if err != nil || query.Page != 1 || query.PageSize != 12 || query.Sort != "created_at" || !query.Desc {
		t.Fatalf("default = %+v %v", query, err)
	}
	if _, err := ParseAdminStudyQuery(url.Values{"pageSize": {"200"}}); err == nil || err.(*Error).Status != 400 {
		t.Fatalf("oversized page accepted: %v", err)
	}
	if _, err := ParseAdminStudyQuery(url.Values{"sort": {"phone"}}); err == nil {
		t.Fatal("phone sort accepted")
	}
	if _, err := ParseAdminStudyQuery(url.Values{"order": {"desc;drop table user_profiles"}}); err == nil {
		t.Fatal("order injection accepted")
	}
	query, err = ParseAdminStudyQuery(url.Values{"q": {"%' OR username ILIKE '%"}, "sort": {"today"}, "order": {"asc"}, "page": {"2"}, "pageSize": {"50"}})
	if err != nil || query.Keyword != `%' OR username ILIKE '%` || query.Sort != "today" || query.Desc || query.Page != 2 || query.PageSize != 50 {
		t.Fatalf("literal keyword = %+v %v", query, err)
	}
	if err := ValidateQuery(Query{Table: "user_profiles", Select: "id", Limit: 287}); err == nil {
		t.Fatal("generic query limit no longer rejects 287")
	}
}
