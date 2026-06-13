# Vue conventions (pack: vue)

Vue is the client-side UI layer of the frontend. Author UI as Single-File
Components (`.vue`) under `assets/`, each using `<script setup>` and the
Composition API; keep component logic inside the SFC, never in the
server-rendered templates.

Shared, reusable components live under `packages/ui/**` — treat that directory
as the component library and reuse an existing shared component before
authoring a new one. Application-specific views compose those shared components
rather than re-implementing them.

The review and machine-checkable rules that enforce these conventions — SFC
location and style, shared-component reuse, boolean-prop shape, date handling,
`<script setup>` ordering, and design-token styling — live in `code-rules.yaml`
in this pack.
