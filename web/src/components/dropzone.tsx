import { useRef, useState } from 'react';
import { FileArchive, FolderUp, UploadCloud } from 'lucide-react';
import { Trans } from '@lingui/react/macro';

import { Button } from '@/components/ui/button';
import { Progress } from '@/components/ui/progress';
import { fromDrop, fromInput, type Selection } from '@/lib/collect-files';
import { formatBytes } from '@/lib/format';
import { cn } from '@/lib/utils';

interface Props {
  onSelect: (selection: Selection) => void;
  /** Progreso de la subida en curso (0–1), o null si no hay ninguna. */
  progress: number | null;
  uploading?: string;
}

/** Zona para arrastrar una carpeta o un .zip, al estilo de Cloudflare Pages. */
export function Dropzone({ onSelect, progress, uploading }: Props) {
  const [over, setOver] = useState(false);
  const folderInput = useRef<HTMLInputElement>(null);
  const zipInput = useRef<HTMLInputElement>(null);
  const busy = progress !== null;

  function pick(list: FileList | null) {
    const sel = list && fromInput(list);
    if (sel) onSelect(sel);
  }

  async function onDrop(e: React.DragEvent) {
    e.preventDefault();
    setOver(false);
    if (busy) return;
    const sel = await fromDrop(e.dataTransfer);
    if (sel) onSelect(sel);
  }

  if (busy) {
    return (
      <div className="space-y-3 rounded-lg border border-dashed p-8 text-center">
        <UploadCloud className="mx-auto size-8 animate-pulse text-primary" />
        <p className="text-sm font-medium">
          {progress < 1 ? <Trans>Uploading {uploading}…</Trans> : <Trans>Publishing…</Trans>}
        </p>
        <Progress value={progress * 100} className="mx-auto max-w-sm" />
        <p className="text-xs text-muted-foreground">{Math.round(progress * 100)}%</p>
      </div>
    );
  }

  return (
    <div
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={onDrop}
      className={cn(
        'flex flex-col items-center gap-3 rounded-lg border border-dashed p-8 text-center transition-colors',
        over && 'border-primary bg-primary/5',
      )}
    >
      <UploadCloud className="size-8 text-muted-foreground" />
      <div>
        <p className="text-sm font-medium">
          <Trans>Drop your build folder or a .zip here</Trans>
        </p>
        <p className="text-xs text-muted-foreground">
          <Trans>
            For example <code className="font-mono">dist/</code> or <code className="font-mono">build/</code>. It goes
            live as soon as the upload finishes.
          </Trans>
        </p>
      </div>
      <div className="flex gap-2">
        <Button variant="outline" size="sm" onClick={() => folderInput.current?.click()}>
          <FolderUp /> <Trans>Choose folder</Trans>
        </Button>
        <Button variant="outline" size="sm" onClick={() => zipInput.current?.click()}>
          <FileArchive /> <Trans>Choose .zip</Trans>
        </Button>
      </div>
      <input
        ref={folderInput}
        type="file"
        className="hidden"
        multiple
        // webkitdirectory no está en los tipos de React, pero todos los navegadores lo soportan.
        {...{ webkitdirectory: '' }}
        onChange={(e) => {
          pick(e.target.files);
          e.target.value = '';
        }}
      />
      <input
        ref={zipInput}
        type="file"
        accept=".zip,application/zip"
        className="hidden"
        onChange={(e) => {
          pick(e.target.files);
          e.target.value = '';
        }}
      />
    </div>
  );
}

export function selectionSummary(sel: Selection) {
  return `${sel.label} · ${formatBytes(sel.totalBytes)}`;
}
