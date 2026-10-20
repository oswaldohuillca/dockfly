package store

import (
	"context"
	"time"
)

type DNSProvider struct {
	ID        int64     `bun:",pk,autoincrement" json:"id"`
	Provider  string    `json:"provider"`
	Zones     []string  `json:"zones"`
	EnvEnc    []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

type Certificate struct {
	ID        int64     `bun:",pk,autoincrement" json:"id"`
	Name      string    `json:"name"`
	Domains   []string  `json:"domains"`
	Issuer    string    `json:"issuer"`
	NotAfter  time.Time `json:"not_after"`
	CertPEM   string    `bun:"cert_pem" json:"-"`
	KeyEnc    []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

func (p *DNSProvider) AfterScanRow(context.Context) error {
	p.CreatedAt = p.CreatedAt.UTC()
	return nil
}

func (c *Certificate) AfterScanRow(context.Context) error {
	c.CreatedAt, c.NotAfter = c.CreatedAt.UTC(), c.NotAfter.UTC()
	return nil
}

func (s *Store) ListDNSProviders(ctx context.Context) ([]*DNSProvider, error) {
	out := []*DNSProvider{}
	err := s.db.NewSelect().Model(&out).Order("id").Scan(ctx)
	return out, err
}

func (s *Store) GetDNSProvider(ctx context.Context, id int64) (*DNSProvider, error) {
	p := &DNSProvider{ID: id}
	if err := s.db.NewSelect().Model(p).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return p, nil
}

func (s *Store) CreateDNSProvider(ctx context.Context, p *DNSProvider) error {
	p.CreatedAt = now()
	_, err := s.db.NewInsert().Model(p).Exec(ctx)
	return err
}

func (s *Store) UpdateDNSProvider(ctx context.Context, p *DNSProvider) error {
	return mustAffect(s.db.NewUpdate().Model(p).WherePK().Column("zones", "env_enc").Exec(ctx))
}

func (s *Store) DeleteDNSProvider(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*DNSProvider)(nil)).Where("id = ?", id).Exec(ctx))
}

func (s *Store) ListCertificates(ctx context.Context) ([]*Certificate, error) {
	out := []*Certificate{}
	err := s.db.NewSelect().Model(&out).Order("id").Scan(ctx)
	return out, err
}

func (s *Store) CreateCertificate(ctx context.Context, c *Certificate) error {
	c.CreatedAt = now()
	_, err := s.db.NewInsert().Model(c).Exec(ctx)
	return err
}

func (s *Store) DeleteCertificate(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*Certificate)(nil)).Where("id = ?", id).Exec(ctx))
}
