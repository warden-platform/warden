import js from '@eslint/js'
import reactHooks from 'eslint-plugin-react-hooks'
import prettier from 'eslint-config-prettier'
import babelParser from '@babel/eslint-parser'
import globals from 'globals'

export default [
  { ignores: ['dist', 'node_modules'] },
  js.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      parser: babelParser,
      parserOptions: {
        requireConfigFile: false,
        babelOptions: {
          plugins: ['@babel/plugin-syntax-typescript', '@babel/plugin-syntax-jsx'],
        },
      },
      globals: { ...globals.browser },
    },
    rules: {
      // TS 文件中类型位置的使用会导致 no-unused-vars / no-undef 误报，
      // 未用变量与类型检查统一交给 tsc（tsconfig 已开 noUnusedLocals/
      // noUnusedParameters）。
      'no-unused-vars': 'off',
      'no-undef': 'off',
    },
  },
  {
    files: ['**/*.{ts,tsx}'],
    plugins: {
      'react-hooks': reactHooks,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // 全部页面已迁移到 useApiQuery，hooks 规则恢复为 error 门禁。
      'react-hooks/exhaustive-deps': 'error',
      'react-hooks/set-state-in-effect': 'error',
      'react-hooks/purity': 'error',
      'react-hooks/refs': 'error',
      'react-hooks/static-components': 'error',
    },
  },
  prettier,
]
