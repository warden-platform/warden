import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import { parse } from 'yaml'

const projectRoot = fileURLToPath(new URL('..', import.meta.url))

type ConfigSection = Record<string, string | number | boolean>

function flattenConfig(config: Record<string, ConfigSection>): Record<string, string> {
  return Object.fromEntries(Object.entries(config).flatMap(([section, values]) =>
    Object.entries(values).map(([key, value]) => {
      const name = section === 'server' && key === 'public_url'
        ? 'WARDEN_PUBLIC_CONTROL_URL'
        : section === 'server'
        ? `WARDEN_${key.toUpperCase()}`
        : section === 'web' && key === 'control_target'
          ? 'WARDEN_CONTROL_TARGET'
          : `WARDEN_${section.toUpperCase()}_${key.toUpperCase()}`
      return [name, String(value)]
    }),
  ))
}

export default defineConfig(({ mode }) => {
  // Share config.yaml (non-secret) and .env (secret) with warden-control.
  // Explicit process environment wins for one-off overrides.
  const fileConfig = flattenConfig(parse(readFileSync(`${projectRoot}/config.yaml`, 'utf8')) as Record<string, ConfigSection>)
  const fileEnv = loadEnv(mode, projectRoot, '')
  const env = { ...fileConfig, ...fileEnv, ...process.env }
  const webPort = Number(env.WARDEN_WEB_PORT || 4173)
  const webHost = env.WARDEN_WEB_HOST || 'localhost'
  const controlTarget = env.WARDEN_CONTROL_TARGET || 'http://localhost:8080'

  return {
    envDir: projectRoot,
    plugins: [react()],
    server: {
      host: webHost,
      port: webPort,
      proxy: {
        '/api': {
          target: controlTarget,
          changeOrigin: true,
        },
      },
    },
  }
})
