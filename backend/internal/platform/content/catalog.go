package content

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zood/video-manager/internal/gormplus"
	"gorm.io/gorm"
)

// Catalog holds the post, category, and job repositories.
// Callers cannot name a table or pass a SQL condition through it.
type Catalog struct {
	posts      gormplus.Repo[Post]
	categories gormplus.Repo[Category]
	jobs       gormplus.Repo[Job]
	db         *gormplus.DB
}

func New(db *gormplus.DB) (Catalog, error) {
	posts, err := gormplus.NewRepo[Post](db,
		[]string{"published", "is_public", "created_at", "slug", "id", "author_id"},
		[]string{"title", "slug", "excerpt", "banner", "content", "published", "is_public"})
	if err != nil {
		return Catalog{}, err
	}
	categories, err := gormplus.NewRepo[Category](db, []string{"slug", "name", "id"}, nil)
	if err != nil {
		return Catalog{}, err
	}
	jobs, err := gormplus.NewRepo[Job](db,
		[]string{"title", "company_name", "description", "office_mode_name", "location", "source_created_at", "last_synced_at", "id"},
		nil)
	if err != nil {
		return Catalog{}, err
	}
	return Catalog{posts: posts, categories: categories, jobs: jobs, db: db}, nil
}

func (c Catalog) ListPosts(ctx context.Context, userID string, limit int) ([]map[string]any, int, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	var rows []map[string]any
	var total int
	err := c.db.Do(ctx, userID, func(tx *gorm.DB) error {
		page, err := c.posts.PageTx(tx, 1, limit, func(q *gormplus.Query) {
			q.Eq("published", true).Eq("is_public", true).Order("created_at", true)
		})
		if err != nil {
			return err
		}
		authors, err := profilesByID(tx, authorIDs(page.Records))
		if err != nil {
			return err
		}
		rows = postsToMaps(page.Records, authors)
		total = int(page.Total)
		return nil
	})
	return rows, total, err
}

func (c Catalog) FindPublishedPost(ctx context.Context, userID, column, value string) (map[string]any, error) {
	if column != "slug" && column != "id" {
		return nil, gormplus.ErrColumn
	}
	var row map[string]any
	err := c.db.Do(ctx, userID, func(tx *gorm.DB) error {
		page, err := c.posts.PageTx(tx, 1, 1, func(q *gormplus.Query) {
			q.Eq("published", true).Eq("is_public", true).Eq(column, value)
		})
		if err != nil || len(page.Records) == 0 {
			return err
		}
		authors, err := profilesByID(tx, authorIDs(page.Records))
		if err != nil {
			return err
		}
		row = postsToMaps(page.Records, authors)[0]
		return nil
	})
	return row, err
}

func (c Catalog) GetCategory(ctx context.Context, userID, slug string) (map[string]any, error) {
	var out map[string]any
	err := c.db.Do(ctx, userID, func(tx *gorm.DB) error {
		var categories []Category
		if err := tx.Where("slug = ?", slug).Limit(1).Find(&categories).Error; err != nil {
			return err
		}
		if len(categories) == 0 {
			return nil
		}
		category := categories[0]
		var relations []Relation
		if err := tx.Where("cate_id = ?", category.ID).Limit(100).Find(&relations).Error; err != nil {
			return err
		}
		ids := make([]string, 0, len(relations))
		for _, relation := range relations {
			ids = append(ids, relation.PostID)
		}
		var posts []Post
		if len(ids) > 0 {
			if err := tx.Where("id IN ?", ids).Find(&posts).Error; err != nil {
				return err
			}
		}
		authors, err := profilesByID(tx, authorIDs(posts))
		if err != nil {
			return err
		}
		byID := map[string]Post{}
		for _, post := range posts {
			byID[post.ID] = post
		}
		rows := make([]map[string]any, 0, len(relations))
		for _, relation := range relations {
			post, ok := byID[relation.PostID]
			if !ok {
				continue
			}
			rows = append(rows, map[string]any{
				"post_id": relation.PostID,
				"posts":   postsToMaps([]Post{post}, authors)[0],
			})
		}
		out = map[string]any{
			"category": map[string]any{
				"id": category.ID, "name": category.Name, "slug": category.Slug, "description": category.Description,
			},
			"relations": rows,
		}
		return nil
	})
	return out, err
}

func (c Catalog) ListCategories(ctx context.Context, userID string) ([]map[string]any, error) {
	var rows []map[string]any
	err := c.db.Do(ctx, userID, func(tx *gorm.DB) error {
		page, err := c.categories.PageTx(tx, 1, 50, func(q *gormplus.Query) {
			q.Order("name", false)
		})
		if err != nil {
			return err
		}
		relations, err := relationsByCategory(tx, categoryIDs(page.Records))
		if err != nil {
			return err
		}
		rows = categoriesToMaps(page.Records, relations)
		return nil
	})
	return rows, err
}

func (c Catalog) ListJobs(ctx context.Context, userID, keyword string, remoteOnly bool, page, size int) ([]map[string]any, int, error) {
	var rows []map[string]any
	var total int
	err := c.db.Do(ctx, userID, func(tx *gorm.DB) error {
		result, err := c.jobs.PageTx(tx, page, size, func(q *gormplus.Query) {
			if strings.TrimSpace(keyword) != "" {
				q.ILike([]string{"title", "company_name", "description"}, keyword)
			}
			if remoteOnly {
				q.ILike([]string{"office_mode_name", "location"}, "remote")
			}
			q.Order("source_created_at", true).Order("last_synced_at", true)
		})
		if err != nil {
			return err
		}
		rows = jobsToMaps(result.Records)
		total = int(result.Total)
		return nil
	})
	return rows, total, err
}

func (c Catalog) UpdatePost(ctx context.Context, userID, id string, values map[string]any) (int64, error) {
	return c.posts.UpdateByID(ctx, userID, id, values)
}

func authorIDs(posts []Post) []string {
	ids := make([]string, 0, len(posts))
	seen := map[string]struct{}{}
	for _, post := range posts {
		if post.AuthorID == "" {
			continue
		}
		if _, ok := seen[post.AuthorID]; ok {
			continue
		}
		seen[post.AuthorID] = struct{}{}
		ids = append(ids, post.AuthorID)
	}
	return ids
}

func profilesByID(tx *gorm.DB, ids []string) (map[string]Profile, error) {
	out := map[string]Profile{}
	if len(ids) == 0 {
		return out, nil
	}
	var profiles []Profile
	if err := tx.Model(&Profile{}).Where("id IN ?", ids).Find(&profiles).Error; err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		out[profile.ID] = profile
	}
	return out, nil
}

func categoryIDs(rows []Category) []string {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func relationsByCategory(tx *gorm.DB, ids []string) (map[string][]map[string]any, error) {
	out := map[string][]map[string]any{}
	if len(ids) == 0 {
		return out, nil
	}
	var relations []Relation
	if err := tx.Model(&Relation{}).Where("cate_id IN ?", ids).Find(&relations).Error; err != nil {
		return nil, err
	}
	for _, relation := range relations {
		out[relation.CateID] = append(out[relation.CateID], map[string]any{"post_id": relation.PostID})
	}
	return out, nil
}

func postsToMaps(posts []Post, authors map[string]Profile) []map[string]any {
	rows := make([]map[string]any, len(posts))
	for i, post := range posts {
		var author any
		if profile, ok := authors[post.AuthorID]; ok {
			author = map[string]any{
				"username":     profile.Username,
				"display_name": profile.DisplayName,
				"avatar_url":   profile.AvatarURL,
				"bio":          profile.Bio,
			}
		}
		rows[i] = map[string]any{
			"id": post.ID, "author_id": post.AuthorID, "title": post.Title, "slug": post.Slug,
			"excerpt": post.Excerpt, "banner": post.Banner, "content": post.Content,
			"published": post.Published, "is_public": post.IsPublic,
			"created_at": post.CreatedAt, "updated_at": post.UpdatedAt, "author": author,
		}
	}
	return rows
}

func categoriesToMaps(rows []Category, relations map[string][]map[string]any) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		linked := relations[row.ID]
		if linked == nil {
			linked = []map[string]any{}
		}
		out[i] = map[string]any{
			"id": row.ID, "name": row.Name, "slug": row.Slug, "description": row.Description,
			"created_at": row.CreatedAt, "post_cate_relations": linked,
		}
	}
	return out
}

func jobsToMaps(rows []Job) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		var tags any
		if len(row.Tags) > 0 {
			_ = json.Unmarshal(row.Tags, &tags)
		}
		out[i] = map[string]any{
			"id": row.ID, "title": row.Title, "company_name": row.CompanyName,
			"company_logo": row.CompanyLogo, "location": row.Location,
			"office_mode_name": row.OfficeModeName, "work_type_name": row.WorkTypeName,
			"min_salary": row.MinSalary, "max_salary": row.MaxSalary,
			"source_created_at": row.SourceCreatedAt, "last_synced_at": row.LastSyncedAt,
			"description": row.Description, "tags": tags, "status": row.Status,
		}
	}
	return out
}
