import { useEffect, useMemo, useState } from 'react';
import { Lock, Search } from 'lucide-react';
import { Trans, useLingui } from '@lingui/react/macro';

import { ProviderIcon } from '@/components/brand-icons';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { api, errorMessage, type GitConnection, type Repo } from '@/lib/api';
import { cn } from '@/lib/utils';

export interface RepoValue {
  connection_id: number;
  repo: string;
  branch: string;
}

interface Props {
  connections: GitConnection[];
  value: RepoValue;
  onChange: (v: RepoValue) => void;
  onError?: (msg: string) => void;
}

/** Elegir cuenta, repositorio (con búsqueda) y rama. */
export function RepoPicker({ connections, value, onChange, onError }: Props) {
  const { t } = useLingui();
  const [repos, setRepos] = useState<Repo[] | null>(null);
  const [query, setQuery] = useState('');
  const [branches, setBranches] = useState<string[]>([]);
  const conn = connections.find((c) => c.id === value.connection_id);

  useEffect(() => {
    if (!value.connection_id) return;
    setRepos(null);
    api
      .listRepos(value.connection_id)
      .then(setRepos)
      .catch((e) => {
        setRepos([]);
        onError?.(errorMessage(e));
      });
  }, [value.connection_id, onError]);

  useEffect(() => {
    if (!value.connection_id || !value.repo) return;
    api
      .listBranches(value.connection_id, value.repo)
      .then(setBranches)
      .catch(() => setBranches([]));
  }, [value.connection_id, value.repo]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (repos ?? []).filter((r) => !q || r.full_name.toLowerCase().includes(q));
  }, [repos, query]);

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-[1fr_1.4fr]">
        <div className="space-y-2">
          <Label>
            <Trans>Account</Trans>
          </Label>
          <Select
            value={String(value.connection_id || '')}
            onValueChange={(v) => onChange({ connection_id: Number(v), repo: '', branch: '' })}
          >
            <SelectTrigger className="w-full">
              <SelectValue placeholder={t`Choose an account`} />
            </SelectTrigger>
            <SelectContent>
              {connections.map((c) => (
                <SelectItem key={c.id} value={String(c.id)}>
                  <ProviderIcon provider={c.provider} /> {c.username}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <Label>
            <Trans>Branch</Trans>
          </Label>
          <Select value={value.branch} onValueChange={(v) => onChange({ ...value, branch: v })} disabled={!value.repo}>
            <SelectTrigger className="w-full">
              <SelectValue placeholder={t`Choose a repository`} />
            </SelectTrigger>
            <SelectContent>
              {(branches.includes(value.branch) || !value.branch ? branches : [value.branch, ...branches]).map((b) => (
                <SelectItem key={b} value={b}>
                  {b}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="space-y-2">
        <Label>
          <Trans>Repository</Trans>
        </Label>
        <div className="rounded-md border">
          <div className="relative border-b">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <input
              className="h-9 w-full bg-transparent pr-3 pl-8 text-sm outline-none placeholder:text-muted-foreground"
              placeholder={t`Search…`}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <ul className="max-h-44 overflow-y-auto p-1">
            {repos === null ? (
              <li className="px-2 py-3 text-center text-sm text-muted-foreground">
                <Trans>Loading repositories…</Trans>
              </li>
            ) : visible.length === 0 ? (
              <li className="px-2 py-3 text-center text-sm text-muted-foreground">
                {query ? (
                  <Trans>No repositories match “{query}”. Check which repos the account can access.</Trans>
                ) : (
                  <Trans>No repositories. Check which repos the account can access.</Trans>
                )}
              </li>
            ) : (
              visible.map((r) => (
                <li key={r.full_name}>
                  <button
                    type="button"
                    onClick={() => onChange({ ...value, repo: r.full_name, branch: r.default_branch })}
                    className={cn(
                      'flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-muted',
                      r.full_name === value.repo && 'bg-primary/10 font-medium',
                    )}
                  >
                    {conn && <ProviderIcon provider={conn.provider} className="size-3.5 opacity-70" />}
                    <span className="truncate">{r.full_name}</span>
                    {r.private && <Lock className="size-3 shrink-0 text-muted-foreground" />}
                  </button>
                </li>
              ))
            )}
          </ul>
        </div>
      </div>
    </div>
  );
}
