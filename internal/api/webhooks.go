package api

import (
	"context"
	"strconv"

	"github.com/dockfly/dockfly/internal/store"
)

// Webhooks de push de los sitios y las apps enlazados a un repositorio. Con
// una GitHub App los pushes llegan por el webhook único de la app; con un
// token, Dockfly crea un webhook propio en el repositorio.

// appWebhook marca lo que recibe los pushes por el webhook de la GitHub App
// (en vez de uno propio en el repositorio).
const appWebhook = "github-app"

// Motivos por los que un repositorio enlazado no despliega en cada push.
var (
	errWebhookNoPublicURL = newError("webhook_public_url_missing",
		"Set Dockfly's public URL in Integrations to deploy on every push")
	errWebhookCreate = newError("webhook_create_failed", "Couldn't create the webhook: {detail}")
)

// hookTarget es lo que recibe los pushes de un repositorio: un sitio o una app.
type hookTarget struct {
	connectionID *int64
	repo         string
	// hook es el webhook guardado ahora (vacío si no hay).
	hook hookState
	// path es la ruta del webhook propio tras /api/hooks/<proveedor>/.
	path string
}

type hookState struct{ ID, Secret string }

func (t hookTarget) own() bool { return t.hook.ID != "" && t.hook.ID != appWebhook }

// problem es un aviso con código (como los errores) para que el panel lo
// traduzca; nil si no hay aviso.
type problem struct {
	Error  string         `json:"error"`
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

func toProblem(e *apiError) *problem {
	if e == nil {
		return nil
	}
	return &problem{Error: e.Error(), Code: e.Code, Params: e.Params}
}

// ensureWebhook decide qué webhook debe quedar guardado para t (y lo crea en
// el repositorio si hace falta). Si los pushes no van a desplegar, devuelve
// también el motivo. Un webhook propio que ya existe se conserva.
func (s *Server) ensureWebhook(ctx context.Context, t hookTarget) (hookState, *apiError) {
	if t.connectionID == nil {
		return hookState{}, nil
	}
	conn, prov, err := s.connectionProvider(ctx, *t.connectionID)
	if err != nil {
		e := errWebhookCreate.With("detail", err.Error())
		return t.hook, &e
	}
	if conn.AuthType == store.AuthGitHubApp {
		if app, err := s.Store.GetGitHubApp(ctx, *conn.GitHubAppID); err == nil && app.WebhookActive {
			return hookState{ID: appWebhook}, nil
		}
		return hookState{}, &errWebhookNoPublicURL
	}
	if t.own() {
		return t.hook, nil
	}
	publicURL, _ := s.Store.GetSetting(ctx, store.SettingPublicURL)
	if publicURL == "" {
		return hookState{}, &errWebhookNoPublicURL
	}
	secret := randomHex(32)
	id, err := prov.CreateWebhook(ctx, t.repo, publicURL+"/api/hooks/"+conn.Provider+"/"+t.path, secret)
	if err != nil {
		e := errWebhookCreate.With("detail", err.Error())
		return hookState{}, &e
	}
	return hookState{ID: id, Secret: secret}, nil
}

// removeWebhook intenta borrar el webhook propio de t. Si falla (token
// revocado, repo borrado…) solo se registra: no debe impedir desenlazar.
func (s *Server) removeWebhook(ctx context.Context, t hookTarget) {
	if !t.own() || t.connectionID == nil {
		return
	}
	_, p, err := s.connectionProvider(ctx, *t.connectionID)
	if err == nil {
		err = p.DeleteWebhook(ctx, t.repo, t.hook.ID)
	}
	if err != nil {
		s.Log.Warn("no se pudo borrar el webhook", "repo", t.repo, "error", err)
	}
}

func siteHook(site *store.Site) hookTarget {
	return hookTarget{
		connectionID: site.GitConnectionID, repo: site.Repo,
		hook: hookState{site.WebhookID, site.WebhookSecret}, path: itoa(site.ID),
	}
}

func appHook(a *store.App) hookTarget {
	t := hookTarget{
		connectionID: a.GitConnectionID, repo: a.Repo,
		hook: hookState{a.WebhookID, a.WebhookSecret}, path: "apps/" + itoa(a.ID),
	}
	if a.Source == store.AppSourceImage {
		t.connectionID = nil
	}
	return t
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
