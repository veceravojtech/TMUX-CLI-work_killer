# Twig conventions (pack: twig)

Twig is the server-side rendering layer. Pages render from a single base layout
that defines the shared document shell (head, navigation, footer) through named
blocks; individual page templates extend that base layout and fill its blocks.

Build reusable markup as components: small partials included where needed,
`embed` blocks for parameterised fragments, and macros for repeated inline
snippets. Compose pages from these pieces rather than copying markup between
templates.

Templates are presentation only — they carry NO business logic. Compute and
shape data in the controller (or a view model / presenter) and pass it ready to
render; a Twig template only formats and lays out the values it is given.
