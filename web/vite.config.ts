import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Defaults preserve the original single-service topology (vite :4173 ->
// warden-control :8080). During local OIDC联调 unauth owns :8080, so override all
// three via environment variables, e.g.:
//   WARDEN_WEB_PORT=8081 WARDEN_WEB_HOST=127.0.0.1 \
//   WARDEN_CONTROL_TARGET=http://127.0.0.1:8090 npm --prefix web run dev
const webPort = Number(process.env.WARDEN_WEB_PORT || 4173)
const webHost = process.env.WARDEN_WEB_HOST || 'localhost'
const controlTarget = process.env.WARDEN_CONTROL_TARGET || 'http://localhost:8080'

export default defineConfig({
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
})
