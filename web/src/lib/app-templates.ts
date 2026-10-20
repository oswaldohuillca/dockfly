import type { AppInput } from '@/lib/api';

// Plantillas para las apps más habituales: rellenan imagen, puerto,
// volúmenes y variables. Todo se puede cambiar antes de crear la app.

function password(length = 24) {
  const chars = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  const bytes = crypto.getRandomValues(new Uint8Array(length));
  return Array.from(bytes, (b) => chars[b % chars.length]).join('');
}

export interface AppTemplate {
  id: string;
  label: string;
  description: string;
  build: () => Partial<AppInput> & { env?: Record<string, string> };
}

export const appTemplates: AppTemplate[] = [
  {
    id: 'postgres',
    label: 'PostgreSQL',
    description: 'Base de datos. Otras apps conectan en postgres:5432.',
    build: () => ({
      name: 'postgres',
      image: 'postgres:17',
      volumes: [{ name: 'data', path: '/var/lib/postgresql/data' }],
      env: { POSTGRES_PASSWORD: password(), POSTGRES_USER: 'postgres', POSTGRES_DB: 'app' },
    }),
  },
  {
    id: 'mysql',
    label: 'MySQL',
    description: 'Base de datos. Otras apps conectan en mysql:3306.',
    build: () => ({
      name: 'mysql',
      image: 'mysql:8.4',
      volumes: [{ name: 'data', path: '/var/lib/mysql' }],
      env: { MYSQL_ROOT_PASSWORD: password(), MYSQL_DATABASE: 'app' },
      memory_mb: 1024,
    }),
  },
  {
    id: 'redis',
    label: 'Redis',
    description: 'Caché y colas. Otras apps conectan en redis:6379.',
    build: () => ({
      name: 'redis',
      image: 'redis:7-alpine',
      command: 'redis-server --appendonly yes',
      volumes: [{ name: 'data', path: '/data' }],
    }),
  },
  {
    id: 'n8n',
    label: 'n8n',
    description: 'Automatización de flujos de trabajo.',
    build: () => ({
      name: 'n8n',
      image: 'n8nio/n8n',
      port: 5678,
      volumes: [{ name: 'data', path: '/home/node/.n8n' }],
      env: { GENERIC_TIMEZONE: Intl.DateTimeFormat().resolvedOptions().timeZone },
    }),
  },
  {
    id: 'uptime-kuma',
    label: 'Uptime Kuma',
    description: 'Monitorización de webs y servicios.',
    build: () => ({ name: 'uptime-kuma', image: 'louislam/uptime-kuma:2', port: 3001, volumes: [{ name: 'data', path: '/app/data' }] }),
  },
  {
    id: 'whoami',
    label: 'whoami (prueba)',
    description: 'Servidor de prueba que muestra la petición recibida.',
    build: () => ({ name: 'whoami', image: 'traefik/whoami', port: 80, health_path: '/' }),
  },
];
