// ESLint config — scoped to npm/ shim only.
// This repo is Go-first; ESLint should not run on YAML, JSON, or Markdown.
const js = { files: ["**/*.js", "**/*.mjs", "**/*.cjs"] };

export default [
  { ignores: ["**/*", "!npm/**/*"] },
  { files: js.files },
];
