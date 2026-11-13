/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        brand: {
          DEFAULT: 'rgb(249 115 22)', // orange-500 (Keep light mode accent)
          dark: 'rgb(59 130 246)', // blue-500 (Keep dark mode accent)
        },
        surface: {
          DEFAULT: '#ffffff',
          muted: '#f9fafb',
          dark: '#0B1229',
          'dark-muted': '#172554',
        },
        border: {
          DEFAULT: '#e5e7eb',
          dark: '#1e293b',
        },
        content: {
          DEFAULT: '#333333',
          subtle: '#646464',
          dark: '#e2e8f0',
          'dark-subtle': '#94a3b8',
        },
      },
      fontFamily: {
        sans: ['Inter', 'Noto Sans SC', 'system-ui', 'sans-serif'],
        mono: ['JetBrains Mono', 'ui-monospace', 'monospace'],
      },
      boxShadow: {
        card: '0 1px 2px 0 rgb(0 0 0 / 0.05)',
      },
    },
  },
  plugins: [require('@tailwindcss/forms')],
}
