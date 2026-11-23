import { Trans, useLingui } from '@lingui/react/macro';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Play } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage } from '@/lib/api';

interface Run {
  command: string;
  output: string;
  /** No exit code: the command never ran at all (an API error). */
  exitCode?: number;
  truncated?: boolean;
  noShell?: boolean;
}

/** Runs one-off commands in the app's container. Not a terminal: every command starts from scratch. */
export function AppConsole({ appId }: { appId: number }) {
  const { t } = useLingui();
  const [command, setCommand] = useState('');
  const [running, setRunning] = useState(false);
  const [runs, setRuns] = useState<Run[]>([]);
  const end = useRef<HTMLDivElement>(null);

  // Braces on purpose: scrollIntoView returns a promise in Chrome and React would take it for the effect's cleanup.
  useEffect(() => {
    end.current?.scrollIntoView({ block: 'nearest' });
  }, [runs]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const cmd = command.trim();
    if (!cmd || running) return;
    setRunning(true);
    try {
      const res = await api.appExec(appId, cmd);
      setRuns((r) => [...r, { command: cmd, output: res.output, exitCode: res.exit_code, truncated: res.truncated, noShell: res.no_shell }]);
      setCommand('');
    } catch (err) {
      setRuns((r) => [...r, { command: cmd, output: errorMessage(err) }]);
    } finally {
      setRunning(false);
    }
  }

  return (
    <div className="space-y-3">
      <div
        role="log"
        tabIndex={0}
        aria-label={t`Command output`}
        className="h-[50svh] overflow-auto bg-zinc-950 p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-zinc-100"
      >
        {runs.length === 0 && (
          <p className="text-zinc-400">
            <Trans>Run a command inside the app's container: a migration, a quick look at a file. Each command starts from scratch, so cd and variables don't carry over.</Trans>
          </p>
        )}
        {runs.map((r, i) => (
          <div key={i} className="mb-3">
            <p className="text-zinc-400">$ {r.command}</p>
            {r.noShell && (
              <p className="text-amber-400">
                <Trans>This image has no shell: the command ran directly, so pipes, quotes and variables don't work.</Trans>
              </p>
            )}
            {r.output}
            {r.truncated && <p className="text-amber-400"><Trans>Output cut: it was too long.</Trans></p>}
            {r.exitCode === undefined ? null : r.exitCode === 0 ? (
              <p className="text-emerald-400"><Trans>Finished (exit code 0)</Trans></p>
            ) : (
              <p className="text-red-400">{t`Failed (exit code ${r.exitCode})`}</p>
            )}
          </div>
        ))}
        <div ref={end} />
      </div>
      <form onSubmit={submit} className="flex items-end gap-2">
        <div className="flex-1 space-y-1.5">
          <Label htmlFor="app-console-command">
            <Trans>Command</Trans>
          </Label>
          <Input
            id="app-console-command"
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            placeholder="ls -la"
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
          />
        </div>
        <Button type="submit" disabled={running || !command.trim()}>
          <Play /> {running ? <Trans>Running…</Trans> : <Trans>Run</Trans>}
        </Button>
      </form>
    </div>
  );
}
