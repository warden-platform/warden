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
      // exhaustive-deps 对存量代码报点较多，先降为 warn 逐步消化
      'react-hooks/exhaustive-deps': 'warn',
      // React Compiler 系新规则对「挂载时拉数据 + setState」的经典模式全线报错，
      // 存量页面尚未全部迁移到 useApiQuery，先降为 warn 保持可见
      'react-hooks/set-state-in-effect': 'warn',
      'react-hooks/purity': 'warn',
      'react-hooks/refs': 'warn',
      'react-hooks/static-components': 'warn',
    },
  },
  prettier,
]
