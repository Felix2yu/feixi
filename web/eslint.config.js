import js from '@eslint/js';
import prettier from 'eslint-config-prettier';
import react from 'eslint-plugin-react';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import { defineConfig } from 'eslint/config';
import globals from 'globals';
import ts from 'typescript-eslint';

export default defineConfig(
  // ESLint 9 而非 10：eslint-plugin-react@7 的 peer 只到 eslint ^9.7，
  // 在 ESLint 10 上跑 react/display-name 会直接抛
  // "contextOrFilename.getFilename is not a function"（规则 context API 变更）。
  // 其余插件（react-hooks 7 / react-refresh 0.5 / typescript-eslint 8）两者都支持。
  // dist/ 是 vite 的真实输出目录，漏掉它会把压缩产物当源码扫出上千条假错误。
  // brand/ 是品牌素材（图标源文件），不是源码。
  // 测试文件交给 vitest 跑，不进 lint 口径（本仓测试用 node environment，
  // 与浏览器全局假设不符，强行纳入只会得到一串假阳性）。
  {
    ignores: [
      'dist/',
      'node_modules/',
      'brand/',
      '**/*.test.ts',
      '**/*.test.tsx',
      '**/*.spec.ts',
      '**/*.spec.tsx',
      '**/__tests__/**'
    ]
  },
  js.configs.recommended,
  ts.configs.recommended,
  react.configs.flat.recommended,
  // 仓库用 React 17+ 的自动 JSX runtime（@vitejs/plugin-react 默认），
  // JSX 不引入 React 作用域变量，react-in-jsx-scope 会全场误报。
  react.configs.flat['jsx-runtime'],
  // 注意：react-hooks 的正确 flat 配置在 configs.flat.* 下。
  // configs['recommended-latest'] 是 eslintrc 旧式（plugins 为字符串数组），
  // 喂给 ESLint 10 的 flat config 会直接报 "plugins key defined as an array"。
  reactHooks.configs.flat['recommended-latest'],
  reactRefresh.configs.recommended,
  prettier,
  {
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
      parserOptions: { ecmaFeatures: { jsx: true } }
    },
    settings: { react: { version: 'detect' } },
    rules: {
      // 类型由 tsc --noEmit 负责，no-undef 在 TS 项目里会误报全局类型
      'no-undef': 'off',
      // 存量放宽：any 在既有代码里成片存在，一次性改造等于重写前端。
      // 降为 warn 保留可见性，新代码不应再引入。
      '@typescript-eslint/no-explicit-any': 'warn',
      // prop 校验交给 TS 类型系统，JS 版规则在 TSX 里只会误报
      'react/prop-types': 'off',
      // Vite HMR 边界提示（文件里同时导出组件与非组件），不是缺陷
      'react-refresh/only-export-components': 'warn',
      // 存量放宽：「外部状态 / 属性变化 → 同步本地 state」与「挂载取数」是既有代码的
      // 主体范式。按 React 官方建议改造为「渲染期派生 / key 重挂载 / 数据获取库」
      // 属于重写级重构，且直接改动数据流有时序风险。降为 warn 保留可见性。
      'react-hooks/set-state-in-effect': 'warn',
      // 存量放宽：本仓是纯客户端 SPA（vite 静态产物，无 SSR，不存在 hydration
      // 不一致场景），Date.now() 只用于倒计时与时间轴展示，且页面本就有轮询
      // （Dashboard 3s/15s、CheckDetail 15s）驱动重算，取值不会陈旧。
      // 改成 tick 驱动的 state 等于新增定时器与渲染节奏，属行为变更，
      // 不放进 lint 基线 PR。
      'react-hooks/purity': 'warn',
      // `_` 是「有意丢弃」的约定写法（忽略的回调参数）
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrors: 'none' }
      ]
    }
  }
);
