package platform

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	identPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	selectPattern = regexp.MustCompile(`^[a-zA-Z0-9_*,:!.()\s]+$`)
	uuidPattern   = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// Tables the platform API is allowed to touch. There is no generic SQL endpoint.
var allowedTables = map[string]struct{}{
	"posts":                            {},
	"post_cates":                       {},
	"post_cate_relations":              {},
	"post_comments":                    {},
	"user_profiles":                    {},
	"courses":                          {},
	"chapters":                         {},
	"lessons":                          {},
	"course_enrollments":               {},
	"course_purchase_requests":         {},
	"membership_consultation_requests": {},
	"lesson_progress":                  {},
	"video_access_logs":                {},
	"videos":                           {},
	"job_listings":                     {},
	"job_contact_unlocks":              {},
	"interview_tags":                   {},
	"interview_collections":            {},
	"interview_collection_tags":        {},
	"interview_collection_roles":       {},
	"interview_question":               {},
	"interview_question_favorites":     {},
	"interview_question_submissions":   {},
	"resume_optimization_records":      {},
	"messages":                         {},
	"faucet_claims":                    {},
}

// Contact columns are never part of the public job representation.
var jobContactColumns = map[string]struct{}{
	"email": {}, "phone": {}, "wechat": {}, "telegram": {},
}

type Filter struct {
	Column  string
	Op      string
	Value   string
	Values  []string
	Columns []string
}

type Order struct {
	Column string
	Desc   bool
}

type Query struct {
	Table      string
	Select     string
	Filters    []Filter
	Orders     []Order
	Limit      int
	Offset     int
	Count      bool
	OnConflict string
}

type Caller struct {
	UserID string
	Email  string
	Token  string
}

func (c Caller) SignedIn() bool {
	return c.UserID != "" && c.Token != ""
}

func ValidateQuery(q Query) error {
	if _, ok := allowedTables[q.Table]; !ok {
		return invalid("不支持的数据资源")
	}
	if q.Select == "" || !selectPattern.MatchString(q.Select) {
		return invalid("查询字段不合法")
	}
	if strings.Contains(q.Select, "*") && q.Table == "job_listings" {
		return invalid("岗位查询不能使用通配字段")
	}
	for _, filter := range q.Filters {
		if err := validateFilter(filter); err != nil {
			return err
		}
	}
	for _, order := range q.Orders {
		if !identPattern.MatchString(order.Column) {
			return invalid("排序字段不合法")
		}
	}
	if q.Limit < 0 || q.Limit > 200 {
		return invalid("分页超出范围")
	}
	if q.Offset < 0 || q.Offset > 10000 {
		return invalid("分页超出范围")
	}
	if q.OnConflict != "" {
		for _, part := range strings.Split(q.OnConflict, ",") {
			if !identPattern.MatchString(strings.TrimSpace(part)) {
				return invalid("冲突字段不合法")
			}
		}
	}
	return nil
}

func validateFilter(filter Filter) error {
	switch filter.Op {
	case "eq", "neq", "gte", "lt", "ilike", "is":
		if !identPattern.MatchString(filter.Column) {
			return invalid("过滤字段不合法")
		}
	case "in":
		if !identPattern.MatchString(filter.Column) || len(filter.Values) == 0 || len(filter.Values) > 200 {
			return invalid("过滤字段不合法")
		}
		for _, value := range filter.Values {
			if !safeScalar(value) {
				return invalid("过滤值不合法")
			}
		}
	case "or_ilike":
		if len(filter.Columns) == 0 || len(filter.Columns) > 6 {
			return invalid("过滤字段不合法")
		}
		for _, column := range filter.Columns {
			if !identPattern.MatchString(column) {
				return invalid("过滤字段不合法")
			}
		}
		if _, err := cleanIlike(filter.Value); err != nil {
			return err
		}
	default:
		return invalid("不支持的过滤条件")
	}
	return nil
}

func safeFilterValue(value string) bool {
	if value == "" || len(value) > 500 || strings.ContainsAny(value, ",()*\\\"'") {
		return false
	}
	for _, r := range value {
		if r < 32 {
			return false
		}
	}
	return true
}

func safeScalar(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':' || r == '+' || r == '@' {
			continue
		}
		return false
	}
	return true
}

func cleanIlike(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 100 {
		return "", invalid("搜索关键词不合法")
	}
	if strings.ContainsAny(value, ",()*\\\"';") {
		return "", invalid("搜索关键词包含不支持的字符")
	}
	value = strings.NewReplacer("%", "", "_", "", "*", "").Replace(value)
	if strings.TrimSpace(value) == "" {
		return "", invalid("搜索关键词不合法")
	}
	return value, nil
}

func IsUUID(value string) bool {
	return uuidPattern.MatchString(value)
}

// SupabaseParams renders a fixed PostgREST query. Values are escaped and
// identifiers are allowlisted by ValidateQuery.
func SupabaseParams(q Query) (url.Values, error) {
	if err := ValidateQuery(q); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("select", q.Select)
	for _, filter := range q.Filters {
		key, value, err := renderSupabaseFilter(filter)
		if err != nil {
			return nil, err
		}
		params.Add(key, value)
	}
	if len(q.Orders) > 0 {
		parts := make([]string, 0, len(q.Orders))
		for _, order := range q.Orders {
			direction := "asc"
			if order.Desc {
				direction = "desc"
			}
			parts = append(parts, order.Column+"."+direction)
		}
		params.Set("order", strings.Join(parts, ","))
	}
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		params.Set("offset", strconv.Itoa(q.Offset))
	}
	if q.OnConflict != "" {
		params.Set("on_conflict", q.OnConflict)
	}
	return params, nil
}

func renderSupabaseFilter(filter Filter) (string, string, error) {
	switch filter.Op {
	case "eq", "neq", "gte", "lt":
		if !safeFilterValue(filter.Value) {
			return "", "", invalid("过滤值不合法")
		}
		return filter.Column, filter.Op + "." + filter.Value, nil
	case "is":
		if filter.Value != "null" && filter.Value != "true" && filter.Value != "false" {
			return "", "", invalid("过滤值不合法")
		}
		return filter.Column, "is." + filter.Value, nil
	case "ilike":
		term, err := cleanIlike(filter.Value)
		if err != nil {
			return "", "", err
		}
		return filter.Column, "ilike.*" + term + "*", nil
	case "in":
		return filter.Column, "in.(" + strings.Join(filter.Values, ",") + ")", nil
	case "or_ilike":
		term, err := cleanIlike(filter.Value)
		if err != nil {
			return "", "", err
		}
		parts := make([]string, 0, len(filter.Columns))
		for _, column := range filter.Columns {
			parts = append(parts, column+".ilike.*"+term+"*")
		}
		return "or", "(" + strings.Join(parts, ",") + ")", nil
	default:
		return "", "", invalid("不支持的过滤条件")
	}
}

type Embed struct {
	Key     string
	Table   string
	Local   string
	Remote  string
	Many    bool
	Columns []string
	Nested  []Embed
}

type SQLQuery struct {
	Text string
	Args []any
}

// PrepareSelect separates allowlisted resource embeds from ordinary columns.
// Unknown nesting stays rejected. There is no arbitrary join syntax.
func PrepareSelect(q Query) (Query, []Embed, error) {
	parts := splitSelect(q.Select)
	columns := make([]string, 0, len(parts))
	embeds := make([]Embed, 0)
	for _, part := range parts {
		if !strings.Contains(part, "(") {
			columns = append(columns, part)
			continue
		}
		embed, err := parseEmbed(q.Table, part)
		if err != nil {
			return Query{}, nil, err
		}
		embeds = append(embeds, embed)
		if !containsColumn(columns, embed.Local) {
			columns = append(columns, embed.Local)
		}
	}
	q.Select = strings.Join(columns, ",")
	return q, embeds, nil
}

func splitSelect(input string) []string {
	parts := []string{}
	depth := 0
	start := 0
	for i, r := range input {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				if part := strings.TrimSpace(input[start:i]); part != "" {
					parts = append(parts, part)
				}
				start = i + 1
			}
		}
	}
	if part := strings.TrimSpace(input[start:]); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func parseEmbed(parent, token string) (Embed, error) {
	alias, table, hint, inner, ok := embedParts(token)
	if !ok {
		return Embed{}, invalid("自建数据库暂不支持嵌套资源查询")
	}
	spec, ok := embedSpec(parent, alias, table, hint)
	if !ok {
		return Embed{}, invalid("自建数据库暂不支持嵌套资源查询")
	}
	columns, nested, err := parseEmbedFields(table, inner, spec.columns)
	if err != nil {
		return Embed{}, err
	}
	for _, child := range nested {
		if !containsColumn(columns, child.Local) {
			columns = append(columns, child.Local)
		}
	}
	spec.embed.Columns = columns
	spec.embed.Nested = nested
	return spec.embed, nil
}

type resolvedEmbed struct {
	embed   Embed
	columns map[string]struct{}
}

func embedParts(token string) (alias, table, hint, inner string, ok bool) {
	open := strings.Index(token, "(")
	if open < 0 || !strings.HasSuffix(token, ")") {
		return "", "", "", "", false
	}
	head := token[:open]
	inner = token[open+1 : len(token)-1]
	if name, rest, found := strings.Cut(head, ":"); found {
		alias = name
		head = rest
	}
	if name, rest, found := strings.Cut(head, "!"); found {
		table = name
		hint = rest
	} else {
		table = head
	}
	if alias == "" {
		alias = table
	}
	if table == "" || strings.ContainsAny(alias+table+hint, " ,)*") {
		return "", "", "", "", false
	}
	return alias, table, hint, inner, true
}

func embedSpec(parent, alias, table, hint string) (resolvedEmbed, bool) {
	type key struct{ parent, alias, table, hint string }
	specs := map[key]resolvedEmbed{
		{"posts", "author", "user_profiles", "fk_user_profiles_posts"}:                           one("author", "user_profiles", "author_id", "id", "username", "display_name", "avatar_url", "bio"),
		{"post_cates", "post_cate_relations", "post_cate_relations", ""}:                         many("post_cate_relations", "post_cate_relations", "id", "cate_id", "post_id", "cate_id"),
		{"post_cate_relations", "posts", "posts", ""}:                                            one("posts", "posts", "post_id", "id", "id", "title", "slug", "excerpt", "banner", "published", "is_public", "created_at", "author_id"),
		{"post_cate_relations", "post_cates", "post_cates", ""}:                                  one("post_cates", "post_cates", "cate_id", "id", "id", "name", "slug", "description"),
		{"post_comments", "user_profiles", "user_profiles", "fk_post_comments_user"}:             one("user_profiles", "user_profiles", "user_id", "id", "id", "username", "display_name", "avatar_url"),
		{"interview_question", "interview_question_tags", "interview_question_tags", ""}:         many("interview_question_tags", "interview_question_tags", "id", "question_id", "question_id", "tag_id"),
		{"interview_question_tags", "interview_tags", "interview_tags", ""}:                      one("interview_tags", "interview_tags", "tag_id", "id", "id", "name"),
		{"interview_question", "interview_collections", "interview_collections", ""}:             one("interview_collections", "interview_collections", "collection_id", "id", "id", "title"),
		{"course_enrollments", "courses", "courses", ""}:                                         one("courses", "courses", "course_id", "id", "id", "title", "description", "cover_image_url", "price", "is_free"),
		{"lesson_progress", "lessons", "lessons", ""}:                                            one("lessons", "lessons", "lesson_id", "id", "id", "title"),
		{"lesson_progress", "courses", "courses", ""}:                                            one("courses", "courses", "course_id", "id", "id", "title"),
		{"interview_question_favorites", "interview_question", "interview_question", ""}:         one("interview_question", "interview_question", "question_id", "id", "id", "title", "content", "difficulty", "collection_id"),
		{"interview_question_submissions", "user_profiles", "user_profiles", ""}:                 one("user_profiles", "user_profiles", "user_id", "id", "id", "username", "display_name", "avatar_url"),
		{"interview_question_submissions", "interview_collections", "interview_collections", ""}: one("interview_collections", "interview_collections", "collection_id", "id", "id", "title"),
	}
	spec, ok := specs[key{parent, alias, table, hint}]
	return spec, ok
}

func one(key, table, local, remote string, columns ...string) resolvedEmbed {
	return relation(key, table, local, remote, false, columns...)
}

func many(key, table, local, remote string, columns ...string) resolvedEmbed {
	return relation(key, table, local, remote, true, columns...)
}

func relation(key, table, local, remote string, isMany bool, columns ...string) resolvedEmbed {
	allowed := map[string]struct{}{}
	for _, column := range columns {
		allowed[column] = struct{}{}
	}
	return resolvedEmbed{embed: Embed{Key: key, Table: table, Local: local, Remote: remote, Many: isMany}, columns: allowed}
}

func parseEmbedFields(table, inner string, allowed map[string]struct{}) ([]string, []Embed, error) {
	columns := []string{}
	nested := []Embed{}
	for _, part := range splitSelect(inner) {
		if strings.Contains(part, "(") {
			child, err := parseEmbed(table, part)
			if err != nil {
				return nil, nil, err
			}
			nested = append(nested, child)
			continue
		}
		if _, ok := allowed[part]; !ok || !identPattern.MatchString(part) {
			return nil, nil, invalid("自建数据库暂不支持嵌套资源查询")
		}
		columns = append(columns, part)
	}
	return columns, nested, nil
}

func containsColumn(columns []string, column string) bool {
	for _, item := range columns {
		if item == column {
			return true
		}
	}
	return false
}

// SQL renders a parameterized statement. Table and column names are allowlisted
// identifiers; user values are only passed as arguments.
func SQL(q Query, countOnly bool) (SQLQuery, error) {
	if err := ValidateQuery(q); err != nil {
		return SQLQuery{}, err
	}
	if strings.Contains(q.Select, "(") || strings.Contains(q.Select, ":") {
		return SQLQuery{}, invalid("自建数据库暂不支持嵌套资源查询")
	}
	columns := splitColumns(q.Select)
	for i, column := range columns {
		if column == "*" {
			return SQLQuery{}, invalid("自建数据库查询必须显式列出字段")
		}
		if !identPattern.MatchString(column) {
			return SQLQuery{}, invalid("查询字段不合法")
		}
		// pgx's database/sql driver otherwise returns text[] as a PostgreSQL array literal.
		if q.Table == "interview_question_submissions" && column == "tags" {
			columns[i] = "to_jsonb(tags) AS tags"
		}
		if q.Table == "job_listings" {
			if _, secret := jobContactColumns[column]; secret && !queryExplicitlyAllowsContact(q) {
				return SQLQuery{}, forbidden("岗位联系方式不能出现在公开查询中")
			}
		}
	}
	args := make([]any, 0)
	where := make([]string, 0)
	for _, filter := range q.Filters {
		clause, clauseArgs, err := renderSQLFilter(filter, &args)
		if err != nil {
			return SQLQuery{}, err
		}
		where = append(where, clause)
		args = append(args, clauseArgs...)
	}
	var b strings.Builder
	if countOnly {
		b.WriteString("SELECT count(*) FROM ")
	} else {
		b.WriteString("SELECT ")
		b.WriteString(strings.Join(columns, ", "))
		b.WriteString(" FROM ")
	}
	b.WriteString(q.Table)
	if len(where) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(where, " AND "))
	}
	if !countOnly && len(q.Orders) > 0 {
		b.WriteString(" ORDER BY ")
		parts := make([]string, 0, len(q.Orders))
		for _, order := range q.Orders {
			direction := "ASC"
			if order.Desc {
				direction = "DESC"
			}
			parts = append(parts, order.Column+" "+direction)
		}
		b.WriteString(strings.Join(parts, ", "))
	}
	if !countOnly && q.Limit > 0 {
		args = append(args, q.Limit)
		b.WriteString(" LIMIT $")
		b.WriteString(strconv.Itoa(len(args)))
	}
	if !countOnly && q.Offset > 0 {
		args = append(args, q.Offset)
		b.WriteString(" OFFSET $")
		b.WriteString(strconv.Itoa(len(args)))
	}
	return SQLQuery{Text: b.String(), Args: args}, nil
}

func sqlBound(value string) any {
	switch value {
	case "true":
		return true
	case "false":
		return false
	default:
		return value
	}
}

func queryExplicitlyAllowsContact(q Query) bool {
	for _, filter := range q.Filters {
		if filter.Column == "id" && filter.Op == "eq" && IsUUID(filter.Value) {
			return true
		}
	}
	return false
}

func renderSQLFilter(filter Filter, args *[]any) (string, []any, error) {
	switch filter.Op {
	case "eq", "neq", "gte", "lt":
		if !safeFilterValue(filter.Value) {
			return "", nil, invalid("过滤值不合法")
		}
		op := map[string]string{"eq": "=", "neq": "<>", "gte": ">=", "lt": "<"}[filter.Op]
		placeholder := "$" + strconv.Itoa(len(*args)+1)
		return filter.Column + " " + op + " " + placeholder, []any{sqlBound(filter.Value)}, nil
	case "is":
		if filter.Value == "null" {
			return filter.Column + " IS NULL", nil, nil
		}
		if filter.Value != "true" && filter.Value != "false" {
			return "", nil, invalid("过滤值不合法")
		}
		return filter.Column + " IS " + strings.ToUpper(filter.Value), nil, nil
	case "ilike", "or_ilike":
		term, err := cleanIlike(filter.Value)
		if err != nil {
			return "", nil, err
		}
		columns := filter.Columns
		if filter.Op == "ilike" {
			columns = []string{filter.Column}
		}
		parts := make([]string, 0, len(columns))
		values := make([]any, 0, len(columns))
		for _, column := range columns {
			placeholder := "$" + strconv.Itoa(len(*args)+len(values)+1)
			parts = append(parts, column+" ILIKE "+placeholder)
			values = append(values, "%"+term+"%")
		}
		return "(" + strings.Join(parts, " OR ") + ")", values, nil
	case "in":
		parts := make([]string, 0, len(filter.Values))
		values := make([]any, 0, len(filter.Values))
		for _, value := range filter.Values {
			placeholder := "$" + strconv.Itoa(len(*args)+len(values)+1)
			parts = append(parts, placeholder)
			values = append(values, value)
		}
		return filter.Column + " IN (" + strings.Join(parts, ", ") + ")", values, nil
	default:
		return "", nil, invalid("不支持的过滤条件")
	}
}

func splitColumns(selectList string) []string {
	raw := strings.Split(selectList, ",")
	columns := make([]string, 0, len(raw))
	for _, column := range raw {
		column = strings.TrimSpace(column)
		if column != "" {
			columns = append(columns, column)
		}
	}
	return columns
}

func clampLimit(value, fallback, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
