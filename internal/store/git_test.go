package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestUpdateSiteKeepsGitConfig(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "t.db"))

	conn := &GitConnection{Provider: "github", BaseURL: "https://api.github.com", Username: "oswa", TokenEnc: []byte{1}}
	if err := s.CreateGitConnection(ctx, conn); err != nil {
		t.Fatal(err)
	}
	site := &Site{Name: "web", Domains: []string{"web.com"}}
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	git := &SiteGit{GitConnectionID: &conn.ID, Repo: "oswa/web", Branch: "main", BuildCommand: "bun run build",
		OutputDir: "dist", BuildEnvEnc: []byte("encrypted"), WebhookSecret: "s3cr3t"}
	if err := s.UpdateSiteGit(ctx, site.ID, git); err != nil {
		t.Fatal(err)
	}

	// Editing the site from the form (with no git fields) must not wipe them.
	if err := s.UpdateSite(ctx, &Site{ID: site.ID, Name: "web-2", Domains: []string{"web.com"}, CreatedAt: site.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSite(ctx, site.ID)
	if got.Name != "web-2" || got.Repo != "oswa/web" || got.BuildCommand != "bun run build" ||
		string(got.BuildEnvEnc) != "encrypted" || got.WebhookSecret != "s3cr3t" {
		t.Errorf("UpdateSite clobbered the git configuration: %+v", got.SiteGit)
	}

	// Deleting the account unlinks the site, but does not delete it.
	if err := s.DeleteGitConnection(ctx, conn.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSite(ctx, site.ID)
	if err != nil || got.GitConnectionID != nil {
		t.Errorf("after deleting the account, the site must be left without a connection: %v %v", got.GitConnectionID, err)
	}

	// An empty SiteGit unlinks the repository.
	if err := s.UpdateSiteGit(ctx, site.ID, &SiteGit{}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSite(ctx, site.ID)
	if got.Repo != "" || got.BuildEnvEnc != nil {
		t.Errorf("unlinking must clear the configuration: %+v", got.SiteGit)
	}
}

func TestBuildLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "t.db"))
	site := &Site{Name: "web", Domains: []string{"web.com"}}
	s.CreateSite(ctx, site)

	newBuild := func() *Build {
		b := &Build{SiteID: site.ID, Status: BuildQueued, Trigger: TriggerPush, Branch: "main"}
		if err := s.CreateBuild(ctx, b); err != nil {
			t.Fatal(err)
		}
		return b
	}

	b := newBuild()
	if err := s.MarkBuildStarted(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBuildStarted(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Error("a build that already started cannot start again")
	}
	dep := int64(7)
	if err := s.FinishBuild(ctx, b.ID, BuildSuccess, "", &dep); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetBuild(ctx, b.ID)
	if got.Status != BuildSuccess || *got.DeploymentID != 7 || got.StartedAt == nil || got.FinishedAt == nil {
		t.Errorf("finished build: %+v", got)
	}

	// A canceled build cannot later finish as a successful one.
	c := newBuild()
	s.FinishBuild(ctx, c.ID, BuildCanceled, "canceled", nil)
	if err := s.FinishBuild(ctx, c.ID, BuildSuccess, "", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("a canceled build must not be markable as successful: %v", err)
	}
	if err := s.MarkBuildStarted(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Error("a canceled build must not be startable")
	}

	// Several pushes in a row: only the last queued build survives.
	q1, q2, q3 := newBuild(), newBuild(), newBuild()
	ids, err := s.SupersedeQueued(ctx, site.ID, q3.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("SupersedeQueued: %v %v", ids, err)
	}
	for _, id := range []int64{q1.ID, q2.ID} {
		if b, _ := s.GetBuild(ctx, id); b.Status != BuildCanceled {
			t.Errorf("build %d should be canceled: %s", id, b.Status)
		}
	}
	if b, _ := s.GetBuild(ctx, q3.ID); b.Status != BuildQueued {
		t.Errorf("the last build must stay queued: %s", b.Status)
	}
	if list, _ := s.ListBuilds(ctx, site.ID, 3); len(list) != 3 || list[0].ID != q3.ID {
		t.Errorf("ListBuilds must return the most recent ones first, honoring the limit")
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "t.db"))
	if v, err := s.GetSetting(ctx, SettingPublicURL); err != nil || v != "" {
		t.Fatalf("missing setting: %q %v", v, err)
	}
	s.SetSetting(ctx, SettingPublicURL, "https://a.com")
	s.SetSetting(ctx, SettingPublicURL, "https://b.com")
	if v, _ := s.GetSetting(ctx, SettingPublicURL); v != "https://b.com" {
		t.Errorf("SetSetting must overwrite: %q", v)
	}
}
