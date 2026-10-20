import type { UploadPayload } from '@/lib/api';
import { plural } from '@/lib/format';

export interface Selection {
  payload: UploadPayload;
  fileCount: number;
  totalBytes: number;
  /** Texto para mostrar: "dist/ · 34 archivos" o "sitio.zip". */
  label: string;
}

const isZip = (f: File) => f.name.toLowerCase().endsWith('.zip');

function fromFiles(files: { path: string; file: File }[]): Selection | null {
  if (files.length === 0) return null;
  if (files.length === 1 && isZip(files[0].file)) {
    const archive = files[0].file;
    return { payload: { archive }, fileCount: 1, totalBytes: archive.size, label: archive.name };
  }
  const root = files[0].path.includes('/') ? files[0].path.split('/')[0] + '/' : '';
  const sameRoot = root && files.every((f) => f.path.startsWith(root));
  const n = files.length;
  return {
    payload: { files },
    fileCount: n,
    totalBytes: files.reduce((sum, f) => sum + f.file.size, 0),
    label: `${sameRoot ? root + ' · ' : ''}${plural(n, 'archivo', 'archivos')}`,
  };
}

/** Archivos elegidos con <input type="file"> (con o sin webkitdirectory). */
export function fromInput(list: FileList): Selection | null {
  return fromFiles(Array.from(list, (file) => ({ path: file.webkitRelativePath || file.name, file })));
}

/** Archivos o carpetas soltados en la zona de arrastre. */
export async function fromDrop(dt: DataTransfer): Promise<Selection | null> {
  // Hay que leer las entradas de forma síncrona, antes del primer await:
  // el navegador vacía el DataTransfer al terminar el evento.
  const entries = Array.from(dt.items)
    .map((item) => item.webkitGetAsEntry())
    .filter((e): e is FileSystemEntry => e !== null);

  const files: { path: string; file: File }[] = [];
  for (const entry of entries) await walk(entry, files);
  return fromFiles(files);
}

async function walk(entry: FileSystemEntry, out: { path: string; file: File }[]) {
  if (entry.isFile) {
    const file = await new Promise<File>((resolve, reject) => (entry as FileSystemFileEntry).file(resolve, reject));
    out.push({ path: entry.fullPath.replace(/^\//, ''), file });
    return;
  }
  if (entry.isDirectory) {
    const reader = (entry as FileSystemDirectoryEntry).createReader();
    // readEntries devuelve los hijos por lotes (Chrome: 100). Hay que llamarlo
    // hasta recibir un lote vacío, o las carpetas grandes quedan incompletas.
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((resolve, reject) => reader.readEntries(resolve, reject));
      if (batch.length === 0) break;
      for (const child of batch) await walk(child, out);
    }
  }
}
