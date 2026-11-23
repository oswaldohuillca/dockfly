package api

import (
	"context"
	"strconv"

	"github.com/dockfly/dockfly/internal/store"
)

// Push webhooks for the sites and apps linked to a repository. With a GitHub
// App the pushes arrive through the app's single webhook; with a token,
// Dockfly creates a webhook of its own in the repository.

// appWebhook marks whatever receives its pushes through the GitHub App's
// webhook (instead of one of its own in the repository).
const appWebhook = "github-app"

// Reasons why a linked repository doesn't deploy on every push.
var (
	errWebhookNoPublicURL = newError("webhook_public_url_missing",
		"Set Dockfly's public URL in Integrations to deploy on every push")
	errWebhookCreate = newError("webhook_create_failed", "Couldn't create the webhook: {detail}")
)

// hookTarget is what receives a repository's pushes: a site or an app.
type hookTarget struct {
	connectionID *int64
	repo         string
	// hook is the webhook stored right now (empty when there is none).
	hook hookState
	// path is the path of its own webhook after /api/hooks/<provider>/.
	path string
}

type hookState struct{ ID, Secret string }

func (t hookTarget) own() bool { return t.hook.ID != "" && t.hook.ID != appWebhook }

// problem is a warning with a code (like the errors) so that the panel can
// translate it; nil when there is no warning.
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

// ensureWebhook decides which webhook should end up stored for t (and
// creates it in the repository when needed). If the pushes aren't going to
// deploy, it also returns the reason. An existing webhook of its own is
// kept.
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

// removeWebhook tries to delete t's own webhook. If that fails (token
// revoked, repo deleted…) it is only logged: it must not block unlinking.
func (s *Server) removeWebhook(ctx context.Context, t hookTarget) {
	if !t.own() || t.connectionID == nil {
		return
	}
	_, p, err := s.connectionProvider(ctx, *t.connectionID)
	if err == nil {
		err = p.DeleteWebhook(ctx, t.repo, t.hook.ID)
	}
	if err != nil {
		s.Log.Warn("couldn't delete the webhook", "repo", t.repo, "error", err)
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
