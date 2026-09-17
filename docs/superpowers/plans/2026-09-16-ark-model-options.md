# Ark model options implementation plan

**Goal:** Configure supported per-model Ark request parameters in TOML and WebUI.

**Design:** `ark_config.model_options` maps exact model/Endpoint IDs to typed `service_tier` and `reasoning_effort` fields. The dynamic key `ark_model_options` replaces the complete map according to existing bot-scoped config precedence. Missing fields preserve existing behavior; explicit reasoning effort overrides the legacy allowlist. Empty maps disable configured overrides. WebUI edits model rows with validated selects.

**Implementation:**
- Add typed options, strict JSON/TOML validation and configuration tests.
- Register the dynamic key, TOML fallback and typed accessor; validate writes centrally.
- Inject a scoped resolver into Ark initialization to avoid an application/infrastructure import cycle.
- Apply options at both Responses entry points and before cache reasoning keys are built.
- Isolate SDK flex enum compatibility and verify request/response serialization with local HTTP tests.
- Add a WebUI editor, API validation tests and component interaction tests.
- Document configuration and run Go 1.26.0 tests with `custom_skip_vips`, `-v`, and the existing development config; run WebUI tests/build.

**Verification:** No live model calls are required. Cover non-targeted models, defaults, invalid fields/values, request immutability, scope precedence, flex response decoding and both streaming and non-streaming wire requests.
