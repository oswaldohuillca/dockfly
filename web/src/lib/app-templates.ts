import { msg } from '@lingui/core/macro';
import type { MessageDescriptor } from '@lingui/core';

import type { AppInput } from '@/lib/api';

// Templates for the most common apps: they fill in image, port, volumes and
// variables. Everything can still be changed before the app is created.

function password(length = 24) {
  const chars = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  const bytes = crypto.getRandomValues(new Uint8Array(length));
  return Array.from(bytes, (b) => chars[b % chars.length]).join('');
}

export interface AppTemplate {
  id: string;
  /** A product name (left untranslated) or a translatable message. */
  label: string | MessageDescriptor;
  description: MessageDescriptor;
  build: () => Partial<AppInput> & { env?: Record<string, string> };
}

export const appTemplates: AppTemplate[] = [
  {
    id: 'postgres',
    label: 'PostgreSQL',
    description: msg`Database. Other apps connect at postgres:5432.`,
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
    description: msg`Database. Other apps connect at mysql:3306.`,
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
    description: msg`Cache and queues. Other apps connect at redis:6379.`,
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
    description: msg`Workflow automation.`,
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
    description: msg`Website and service monitoring.`,
    build: () => ({ name: 'uptime-kuma', image: 'louislam/uptime-kuma:2', port: 3001, volumes: [{ name: 'data', path: '/app/data' }] }),
  },
  {
    id: 'whoami',
    label: msg`whoami (test)`,
    description: msg`Test server that echoes the incoming request.`,
    build: () => ({ name: 'whoami', image: 'traefik/whoami', port: 80, health_path: '/' }),
  },
];
