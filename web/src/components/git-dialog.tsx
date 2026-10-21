import { useEffect, useState } from 'react';
import { Link } from '@tanstack/react-router';
import { ChevronDown, Loader2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { EnvEditor, envToRows, rowsToEnv, type EnvRow } from '@/components/env-editor';
import { RepoPicker } from '@/components/repo-picker';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { api, errorMessage, type GitConnection, type Site, type SiteGitInput } from '@/lib/api';
import { DEFAULT_IMAGE, matchPreset, presets } from '@/lib/presets';
import { cn } from '@/lib/utils';

interface Props {
  site: Site;
  onClose: () => void;
  onSaved: (webhookError: string) => void;
}


export function GitDialog({ site, onClose, onSaved }: Props) {
  const { t, i18n } = useLingui();
  const linked = site.repo !== '';
  const [connections, setConnections] = useState<GitConnection[] | null>(null);
  const [form, setForm] = useState<Omit<SiteGitInput, 'env'>>(() => ({
    connection_id: site.git_connection_id ?? 0,
    repo: site.repo,
    branch: site.branch,
    root_dir: site.root_dir,
    // Un sitio nuevo empieza con el preset de Vite.
    install_command: linked ? site.install_command : presets[0].install,
    build_command: linked ? site.build_command : presets[0].build,
    output_dir: linked ? site.output_dir : presets[0].output,
    build_image: site.build_image || DEFAULT_IMAGE,
  }));
  const [env, setEnv] = useState<EnvRow[]>([]);
  const [envTouched, setEnvTouched] = useState(false);
  const [advanced, setAdvanced] = useState(linked && (site.root_dir !== '' || site.build_image !== DEFAULT_IMAGE));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const set = <K extends keyof typeof form>(key: K, value: (typeof form)[K]) => setForm((f) => ({ ...f, [key]: value }));

  useEffect(() => {
    api.listConnections().then((list) => {
      setConnections(list);
      setForm((f) => (f.connection_id === 0 && list.length > 0 ? { ...f, connection_id: list[0].id } : f));
    });
    if (linked) {
      api.getSiteEnv(site.id).then((e) => setEnv(envToRows(e)));
    }
  }, [linked, site.id]);

  const preset = matchPreset(form.install_command, form.build_command, form.output_dir);

  function applyPreset(id: string) {
    const p = presets.find((p) => p.id === id);
    if (p) setForm((f) => ({ ...f, install_command: p.install, build_command: p.build, output_dir: p.output }));
  }

  async function clearCache() {
    try {
      await api.clearBuildCache(site.id);
      toast.success(t`Cache cleared: the next build will download dependencies again`);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  function editEnv(rows: EnvRow[]) {
    setEnv(rows);
    setEnvTouched(true);
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const envMap = rowsToEnv(env);
      const res = await api.putSiteGit(site.id, { ...form, env: envTouched || !linked ? envMap : null });
      const repo = form.repo;
      toast.success(linked ? t`Settings saved` : t`${repo} connected`);
      onSaved(res.webhook_error);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (connections && connections.length === 0) {
    return (
      <Dialog open onOpenChange={(open) => !open && onClose()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              <Trans>Connect an account first</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>To build from a repository, connect your GitHub or GitLab account.</Trans>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button asChild>
              <Link to="/integrations">
                <Trans>Go to Integrations</Trans>
              </Link>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>{linked ? <Trans>Build settings</Trans> : <Trans>Connect a repository</Trans>}</DialogTitle>
            <DialogDescription>
              <Trans>
                Every push to the selected branch builds the site in an isolated container and publishes the result.
              </Trans>
            </DialogDescription>
          </DialogHeader>

          {connections && (
            <RepoPicker
              connections={connections}
              value={{ connection_id: form.connection_id, repo: form.repo, branch: form.branch }}
              onChange={(v) => setForm((f) => ({ ...f, ...v }))}
              onError={setError}
            />
          )}

          <div className="space-y-2">
            <Label>
              <Trans>Framework</Trans>
            </Label>
            <Select value={preset?.id ?? 'custom'} onValueChange={applyPreset}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {presets.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {typeof p.label === 'string' ? p.label : i18n._(p.label)}
                  </SelectItem>
                ))}
                <SelectItem value="custom" disabled>
                  <Trans>Custom</Trans>
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <Field label={t`Install`} value={form.install_command} onChange={(v) => set('install_command', v)} placeholder="bun install" mono />
            <Field label={t`Build`} value={form.build_command} onChange={(v) => set('build_command', v)} placeholder="bun run build" mono />
            <Field label={t`Output directory`} value={form.output_dir} onChange={(v) => set('output_dir', v)} placeholder="dist" mono />
          </div>
          {!form.install_command && !form.build_command && (
            <p className="-mt-2 text-xs text-muted-foreground">
              <Trans>Without commands, the repository files (or its output directory) are published as is.</Trans>
            </p>
          )}

          <button
            type="button"
            onClick={() => setAdvanced((a) => !a)}
            className="flex w-fit items-center gap-1 text-sm font-medium text-muted-foreground hover:text-foreground"
          >
            <ChevronDown className={cn('size-4 transition-transform', advanced && 'rotate-180')} /> <Trans>Advanced options</Trans>
          </button>
          {advanced && linked && (
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
              <span className="text-muted-foreground">
                <Trans>Dependencies are cached between builds. Clear the cache if you see odd install errors.</Trans>
              </span>
              <Button type="button" variant="outline" size="sm" onClick={clearCache}>
                <Trans>Clear cache</Trans>
              </Button>
            </div>
          )}
          {advanced && (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                label={t`Root directory`}
                value={form.root_dir}
                onChange={(v) => set('root_dir', v)}
                placeholder={t`(repo root)`}
                hint={t`For monorepos, e.g. apps/web.`}
                mono
              />
              <Field
                label={t`Build image`}
                value={form.build_image}
                onChange={(v) => set('build_image', v)}
                placeholder={DEFAULT_IMAGE}
                hint={t`Use node:22 with npm ci / npm run build if your project doesn't work with Bun.`}
                mono
              />
            </div>
          )}

          <div className="space-y-2">
            <Label>
              <Trans>Build environment variables</Trans>
            </Label>
            <p className="text-xs text-muted-foreground">
              <Trans>
                Available during the build (e.g. <code className="font-mono">VITE_API_URL</code>). Stored encrypted.
              </Trans>
            </p>
            <EnvEditor rows={env} onChange={editEnv} />
          </div>

          {error && (
            <Alert variant="destructive">
              <TriangleAlert />
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={busy || !form.repo || !form.branch}>
              {busy && <Loader2 className="animate-spin" />}
              {linked ? <Trans>Save</Trans> : <Trans>Connect</Trans>}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

interface FieldProps {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  hint?: string;
  mono?: boolean;
}

function Field({ label, value, onChange, placeholder, hint, mono }: FieldProps) {
  return (
    <div className="space-y-2">
      <Label>{label}</Label>
      <Input value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} className={cn(mono && 'font-mono text-sm')} />
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}
