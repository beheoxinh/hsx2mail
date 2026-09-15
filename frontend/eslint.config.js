import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import svelte from 'eslint-plugin-svelte'
import stylistic from '@stylistic/eslint-plugin'
import svelteParser from 'svelte-eslint-parser'
import globals from 'globals'

export default tseslint.config(
  // Global ignores
  {
    ignores: [
      'wailsjs/**',
      'dist/**',
      'node_modules/**',
      '**/*.config.{js,ts,cjs,mjs}',
      'src/vite-env.d.ts',
    ],
  },

  // Base recommended sets
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...svelte.configs['flat/recommended'],

  // Svelte file parser — use TS parser inside <script> blocks
  {
    files: ['**/*.svelte', '**/*.svelte.ts'],
    languageOptions: {
      parser: svelteParser,
      parserOptions: {
        parser: tseslint.parser,
        extraFileExtensions: ['.svelte'],
      },
    },
  },

  // Project-wide rules
  {
    plugins: { '@stylistic': stylistic },
    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.node,
      },
    },
    rules: {
      // Stylistic — match existing project style
      '@stylistic/quotes': ['error', 'single', { allowTemplateLiterals: 'always', avoidEscape: true }],
      '@stylistic/semi': ['error', 'never'],
      '@stylistic/indent': ['error', 2, { SwitchCase: 1 }],
      '@stylistic/comma-dangle': ['error', 'only-multiline'],
      '@stylistic/no-trailing-spaces': 'error',
      '@stylistic/eol-last': ['error', 'always'],

      // Code quality
      '@typescript-eslint/no-unused-vars': ['error', {
        argsIgnorePattern: '^_',
        varsIgnorePattern: '^_',
        caughtErrorsIgnorePattern: '^_',
      }],
      // Allow @ts-ignore with description — needed because Wails-generated
      // paths sometimes resolve in TS context, where @ts-expect-error would
      // itself become an error. Description is required so suppression is
      // never silent.
      '@typescript-eslint/ban-ts-comment': ['error', {
        'ts-ignore': 'allow-with-description',
        'ts-expect-error': 'allow-with-description',
        minimumDescriptionLength: 3,
      }],
      // Wails-bound types pervasively use `any`; off for now.
      // Revisit if we tighten typing in a future release.
      '@typescript-eslint/no-explicit-any': 'off',

      // Stylistic Svelte 5 idiom preferences — existing immutable-reassign
      // patterns work correctly. Off for now; revisit if migrating.
      'svelte/prefer-svelte-reactivity': 'off',
      'svelte/prefer-writable-derived': 'off',

      // Disable base rule in favor of TS-aware version
      'no-unused-vars': 'off',
    },
  },
)
