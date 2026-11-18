/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // Mission Control identity: signal cyan replaces the old orange/blue.
        brand: {
          DEFAULT: 'rgb(2 132 199)', // sky-600 — daylight accent (contrast on white)
          dark: 'rgb(56 189 248)', // sky-400 — signal cyan on graphite
        },
        surface: {
          DEFAULT: '#ffffff',
          muted: '#f4f6fa',
          dark: '#101722', // graphite panel
          'dark-muted': '#182233', // inset / raised well
        },
        border: {
          DEFAULT: '#d8dee9',
          dark: '#1d2634',
        },
        content: {
          DEFAULT: '#1a2332',
          subtle: '#5b6b82',
          dark: '#e7edf5',
          'dark-subtle': '#8fa0b6',
        },
        // Signal semantics used by the ops wall (status LEDs, funnel, sparks).
        signal: {
          green: '#4ade80',
          amber: '#fbbf24',
          red: '#f87171',
          cyan: '#38bdf8',
        },
      },
      fontFamily: {
        sans: ['Inter', 'Noto Sans SC', 'system-ui', 'sans-serif'],
        mono: ['JetBrains Mono', 'ui-monospace', 'SFMono-Regular', 'monospace'],
      },
      boxShadow: {
        // Flat, crisp elevation — no frosted glass.
        card: '0 1px 2px 0 rgb(2 8 20 / 0.06)',
        lift: '0 8px 28px rgb(2 8 20 / 0.16)',
        'lift-dark': '0 12px 36px rgb(0 0 0 / 0.5)',
      },
    },
  },
  plugins: [require('@tailwindcss/forms')],
}