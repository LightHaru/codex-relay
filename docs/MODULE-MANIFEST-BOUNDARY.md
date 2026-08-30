# Relay module manifest boundary

The `internal/modules` package defines a small, versioned, declarative manifest for future Relay control-plane modules. It is an architectural boundary inspired by Cockpit packages, not a plugin runtime: manifests do not load Go/JavaScript, spawn processes, reference filesystem paths, or carry credentials.

Only validated metadata is accepted: a safe module name, display label/version, capability names, and routes restricted to `/v1/` or `/control/` with a finite HTTP method set. Unknown JSON fields are rejected. Registry listing is sorted by module name and canonicalizes capability/route ordering so diagnostics and tests remain deterministic.

This package is deliberately unused by request routing until a later design phase defines authorization, lifecycle, and compatibility policy.
