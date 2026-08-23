// The shared lint configuration. It is generated and drift-checked, so a rule
// added here reaches every repository that follows the template.
import js from "@eslint/js";
import jsxA11y from "eslint-plugin-jsx-a11y";
import reactHooks from "eslint-plugin-react-hooks";
import prettier from "eslint-config-prettier";
import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: ["dist/**", "coverage/**", "node_modules/**", "tests/fixtures/**"],
  },
  js.configs.recommended,
  tseslint.configs.recommendedTypeChecked,
  {
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  reactHooks.configs["recommended-latest"],
  jsxA11y.flatConfigs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    rules: {
      // An unused name is either a leftover or a mistake. The underscore
      // prefix is the deliberate exception.
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
      // A floating promise swallows its rejection, which is how an error
      // disappears from a browser without a stack.
      "@typescript-eslint/no-floating-promises": "error",
      "@typescript-eslint/consistent-type-imports": "error",
      // Business logic belongs in lib/, where it is testable without a render.
      "no-restricted-syntax": [
        "error",
        {
          selector: "TSEnumDeclaration",
          message: "use a union of string literals rather than an enum",
        },
      ],
    },
  },
  {
    // The configuration file itself is not part of the TypeScript program, so
    // the rules that need type information cannot run on it.
    files: ["**/*.js"],
    extends: [tseslint.configs.disableTypeChecked],
  },
  {
    files: ["tools/**/*.ts", "prerender/**/*.ts", "vite.config.ts"],
    rules: {
      "no-console": "off",
    },
  },
  {
    // act() takes an asynchronous callback whether or not the work inside it
    // awaits, because that is how React flushes the queue it schedules.
    files: ["tests/**/*.{ts,tsx}"],
    rules: {
      "@typescript-eslint/require-await": "off",
    },
  },
  prettier,
);
