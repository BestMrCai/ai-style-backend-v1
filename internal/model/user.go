package model

import "time"

// User 表示系统中的一个用户。
type User struct {
	ID        int64     `json:"id"`
	OpenID    string    `json:"-"` //-表示不会暴露
	Nickname  *string   `json:"nickname"`
	AvatarURL *string   `json:"avatar_url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
