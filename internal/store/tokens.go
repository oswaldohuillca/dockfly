package store

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// APIToken autentica al CLI y a la integración continua en nombre de un usuario.
type APIToken struct {
	bun.BaseModel `bun:"table:api_tokens"`

	ID         int64      `bun:",pk,autoincrement" json:"id"`
	UserID     int64      `json:"user_id"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	Prefix     string     `json:"prefix"`
	Role       string     `json:"role"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (t *APIToken) AfterScanRow(context.Context) error {
	t.CreatedAt = t.CreatedAt.UTC()
	for _, p := range []**time.Time{&t.LastUsedAt, &t.ExpiresAt} {
		if *p != nil {
			u := (*p).UTC()
			*p = &u
		}
	}
	return nil
}

// Expired indica si el token ya caducó.
func (t *APIToken) Expired(now time.Time) bool {
	return t.ExpiresAt != nil && now.After(*t.ExpiresAt)
}

func (s *Store) CreateAPIToken(ctx context.Context, t *APIToken) error {
	t.CreatedAt = now()
	_, err := s.db.NewInsert().Model(t).Exec(ctx)
	return err
}

func (s *Store) ListAPITokens(ctx context.Context, userID int64) ([]*APIToken, error) {
	list := []*APIToken{}
	err := s.db.NewSelect().Model(&list).Where("user_id = ?", userID).Order("id DESC").Scan(ctx)
	return list, err
}

func (s *Store) APITokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	t := new(APIToken)
	if err := s.db.NewSelect().Model(t).Where("token_hash = ?", hash).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return t, nil
}

// TouchAPIToken guarda cuándo se usó el token por última vez.
func (s *Store) TouchAPIToken(ctx context.Context, id int64) error {
	_, err := s.db.NewUpdate().Model((*APIToken)(nil)).Set("last_used_at = ?", now()).Where("id = ?", id).Exec(ctx)
	return err
}

func (s *Store) DeleteAPIToken(ctx context.Context, userID, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*APIToken)(nil)).Where("id = ? AND user_id = ?", id, userID).Exec(ctx))
}
