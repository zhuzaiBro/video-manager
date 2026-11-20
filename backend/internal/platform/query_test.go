package platform

import "testing"

func TestSQLKeepsValuesParameterized(t *testing.T) {
	_, err := SQL(Query{
		Table:  "posts",
		Select: "id,title",
		Filters: []Filter{{
			Op:      "or_ilike",
			Columns: []string{"title"},
			Value:   "engineer' OR 1=1",
		}},
	}, false)
	if err == nil {
		t.Fatal("expected unsafe search to be rejected")
	}

	statement, err := SQL(Query{
		Table:   "lesson_progress",
		Select:  "id,user_id",
		Filters: []Filter{{Column: "user_id", Op: "eq", Value: "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"}},
		Limit:   10,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if statement.Args[0] != "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10" {
		t.Fatalf("args = %#v", statement.Args)
	}
	if contains(statement.Text, "8b1b5d4e") {
		t.Fatalf("user id was interpolated into SQL: %s", statement.Text)
	}
}

func TestRejectsUnknownTable(t *testing.T) {
	if err := ValidateQuery(Query{Table: "auth", Select: "id"}); err == nil {
		t.Fatal("expected unknown table to be rejected")
	}
}

func contains(s, part string) bool {
	return len(s) >= len(part) && (s == part || len(part) == 0 || (func() bool {
		for i := 0; i+len(part) <= len(s); i++ {
			if s[i:i+len(part)] == part {
				return true
			}
		}
		return false
	})())
}
