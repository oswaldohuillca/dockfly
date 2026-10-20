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
		OutputDir: "dist", BuildEnvEnc: []byte("cifrado"), WebhookSecret: "s3cr3t"}
	if err := s.UpdateSiteGit(ctx, site.ID, git); err != nil {
		t.Fatal(err)
	}

	// Editar el sitio desde el formulario (sin campos de git) no debe borrarlos.
	if err := s.UpdateSite(ctx, &Site{ID: site.ID, Name: "web-2", Domains: []string{"web.com"}, CreatedAt: site.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSite(ctx, site.ID)
	if got.Name != "web-2" || got.Repo != "oswa/web" || got.BuildCommand != "bun run build" ||
		string(got.BuildEnvEnc) != "cifrado" || got.WebhookSecret != "s3cr3t" {
		t.Errorf("UpdateSite pisó la configuración de git: %+v", got.SiteGit)
	}

	// Borrar la cuenta desenlaza el sitio, pero no lo borra.
	if err := s.DeleteGitConnection(ctx, conn.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSite(ctx, site.ID)
	if err != nil || got.GitConnectionID != nil {
		t.Errorf("tras borrar la cuenta, el sitio debe quedar sin conexión: %v %v", got.GitConnectionID, err)
	}

	// Un SiteGit vacío desvincula el repositorio.
	if err := s.UpdateSiteGit(ctx, site.ID, &SiteGit{}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSite(ctx, site.ID)
	if got.Repo != "" || got.BuildEnvEnc != nil {
		t.Errorf("desvincular debe vaciar la configuración: %+v", got.SiteGit)
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
		t.Error("un build ya iniciado no puede volver a iniciarse")
	}
	dep := int64(7)
	if err := s.FinishBuild(ctx, b.ID, BuildSuccess, "", &dep); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetBuild(ctx, b.ID)
	if got.Status != BuildSuccess || *got.DeploymentID != 7 || got.StartedAt == nil || got.FinishedAt == nil {
		t.Errorf("build terminado: %+v", got)
	}

	// Un build cancelado no puede terminar después como exitoso.
	c := newBuild()
	s.FinishBuild(ctx, c.ID, BuildCanceled, "cancelado", nil)
	if err := s.FinishBuild(ctx, c.ID, BuildSuccess, "", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("un build cancelado no debe poder marcarse como exitoso: %v", err)
	}
	if err := s.MarkBuildStarted(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Error("un build cancelado no debe poder iniciarse")
	}

	// Varios pushes seguidos: solo sobrevive el último en cola.
	q1, q2, q3 := newBuild(), newBuild(), newBuild()
	ids, err := s.SupersedeQueued(ctx, site.ID, q3.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("SupersedeQueued: %v %v", ids, err)
	}
	for _, id := range []int64{q1.ID, q2.ID} {
		if b, _ := s.GetBuild(ctx, id); b.Status != BuildCanceled {
			t.Errorf("build %d debería estar cancelado: %s", id, b.Status)
		}
	}
	if b, _ := s.GetBuild(ctx, q3.ID); b.Status != BuildQueued {
		t.Errorf("el último build debe seguir en cola: %s", b.Status)
	}
	if list, _ := s.ListBuilds(ctx, site.ID, 3); len(list) != 3 || list[0].ID != q3.ID {
		t.Errorf("ListBuilds debe devolver los más recientes primero, con límite")
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "t.db"))
	if v, err := s.GetSetting(ctx, SettingPublicURL); err != nil || v != "" {
		t.Fatalf("ajuste inexistente: %q %v", v, err)
	}
	s.SetSetting(ctx, SettingPublicURL, "https://a.com")
	s.SetSetting(ctx, SettingPublicURL, "https://b.com")
	if v, _ := s.GetSetting(ctx, SettingPublicURL); v != "https://b.com" {
		t.Errorf("SetSetting debe sobrescribir: %q", v)
	}
}
