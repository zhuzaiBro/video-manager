package content

import "time"

type Post struct {
	ID        string     `gorm:"column:id"`
	AuthorID  string     `gorm:"column:author_id"`
	Title     string     `gorm:"column:title"`
	Slug      string     `gorm:"column:slug"`
	Excerpt   *string    `gorm:"column:excerpt"`
	Banner    *string    `gorm:"column:banner"`
	Content   string     `gorm:"column:content"`
	Published bool       `gorm:"column:published"`
	IsPublic  bool       `gorm:"column:is_public"`
	CreatedAt *time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt *time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (Post) TableName() string { return "posts" }

type Profile struct {
	ID          string  `gorm:"column:id"`
	Username    string  `gorm:"column:username"`
	DisplayName *string `gorm:"column:display_name"`
	AvatarURL   *string `gorm:"column:avatar_url"`
	Bio         *string `gorm:"column:bio"`
}

func (Profile) TableName() string { return "user_profiles" }

type Category struct {
	ID          string     `gorm:"column:id"`
	Name        string     `gorm:"column:name"`
	Slug        string     `gorm:"column:slug"`
	Description *string    `gorm:"column:description"`
	CreatedAt   *time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (Category) TableName() string { return "post_cates" }

type Relation struct {
	PostID string `gorm:"column:post_id"`
	CateID string `gorm:"column:cate_id"`
}

func (Relation) TableName() string { return "post_cate_relations" }

type Job struct {
	ID              string     `gorm:"column:id"`
	Title           *string    `gorm:"column:title"`
	CompanyName     *string    `gorm:"column:company_name"`
	CompanyLogo     *string    `gorm:"column:company_logo"`
	Location        *string    `gorm:"column:location"`
	OfficeModeName  *string    `gorm:"column:office_mode_name"`
	WorkTypeName    *string    `gorm:"column:work_type_name"`
	MinSalary       *float64   `gorm:"column:min_salary"`
	MaxSalary       *float64   `gorm:"column:max_salary"`
	SourceCreatedAt *time.Time `gorm:"column:source_created_at;autoCreateTime:false"`
	LastSyncedAt    *time.Time `gorm:"column:last_synced_at;autoUpdateTime:false"`
	Description     *string    `gorm:"column:description"`
	Tags            []byte     `gorm:"column:tags"`
	Status          int        `gorm:"column:status"`
}

func (Job) TableName() string { return "job_listings" }
