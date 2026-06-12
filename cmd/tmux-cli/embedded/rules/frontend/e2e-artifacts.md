# E2E failure artifacts (pack: frontend — HAS_FRONTEND)

Binding planning convention, loaded when the project has a frontend /
Playwright toolchain. Extracted verbatim from the planner's `<conventions>`
block.

<rule critical="true" id="E2E-ARTIFACT-CONV" condition="HAS_FRONTEND">E2E FAILURE ARTIFACT CONVENTION — BINDING. When E2E runner is present (HAS_FRONTEND==true), scaffold MUST deliver a playwright.config.ts with: trace: 'retain-on-failure' (captures traces on original failure without requiring in-runner retries), screenshot: 'only-on-failure', reporter: [['list'], ['html', { open: 'never' }]] (non-interactive — list for daemon stdout, html for post-mortem with no browser auto-open), outputDir: 'test-results', retries: 0 (daemon retries at goal level via code_retries budget; zero in-runner retries preserve maximum signal fidelity for the convergence guard). Scaffold MUST add test-results/ to the project .gitignore. Every E2E investigator fail criteria in generated goals MUST cite test-results/ as the artifact path for traces and screenshots.</rule>
