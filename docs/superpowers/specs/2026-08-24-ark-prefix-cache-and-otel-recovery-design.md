# Ark Prefix Cache and OTel Recovery Design

## Goal

Eliminate repeated Ark `InvalidParameter` failures for prompts shorter than the
provider's prefix-cache minimum, and restore trace/span correlation after the
OpenTelemetry dependency upgrade.

## Scope

This change covers two production failures:

1. Conversation evaluation judge and candidate prompts are shorter than Ark's
   strict `input tokens > 256` prefix-cache requirement.
2. OTel resource initialization panics because `resource.Default()` and the
   application resource carry different non-empty semantic-convention schema
   URLs.

It also makes optional runtime module startup failures visible so future OTel
degradation is not silent.

## Ark Request Design

`CachedResponseRequest` will expose an explicit prefix-cache policy. Existing
cache-oriented callers remain enabled by default, while the conversation
evaluation candidate and judge callers explicitly disable prefix caching.

When prefix caching is disabled, the Ark adapter sends one Responses request
containing both the system and user messages. The direct request preserves the
model, text format, reasoning, thinking, usage scope, and normal response-text
extraction behavior. It does not read or write the Redis response-ID cache.

When prefix caching is enabled, the current two-request flow remains intact.
If Ark rejects the cache-head request specifically because the prefix input is
below the minimum token count, the adapter retries once through the direct
request path. Other errors are returned unchanged. This protects dynamic or
externally configured prompts without masking authentication, model, schema,
rate-limit, or transport failures.

The fallback is intentionally error-specific. The code will inspect the Ark
error text for both the prefix-cache context and the minimum-token rejection;
it will not treat an arbitrary HTTP 400 as cache incompatibility.

## OpenTelemetry Resource Design

The application resource will be created without its own SchemaURL, then
merged with `resource.Default()`. OpenTelemetry merge semantics retain the
default resource's schema when the other resource is schemaless, so dependency
upgrades cannot create a two-schema conflict.

Resource construction and provider initialization will return errors rather
than panic. `otel.Init` will keep its existing no-op fallback behavior when
provider creation fails, including a visible diagnostic message.

Optional runtime module startup failures will also be logged at the lifecycle
boundary. The module remains optional: a telemetry outage does not stop the
bot, but operators can see the degraded reason even when the management HTTP
surface is disabled.

## Error and Retry Behavior

- Known short prefix: one direct Ark call, no rejected cache-head call.
- Unexpected provider minimum-token rejection: one rejected cache-head call,
  then one direct call.
- Any other cache-head failure: return immediately without fallback.
- Direct-call failure: return the direct-call error once; do not recurse or
  retry indefinitely.
- OTel resource/provider failure: fall back to no-op telemetry and emit the
  concrete initialization error.

## Tests

Ark adapter tests will verify:

- Explicitly disabled prefix caching sends exactly one request with ordered
  system and user messages and no prefix-cache settings.
- A minimum-token cache-head error falls back once to the direct request.
- An unrelated cache-head error does not fall back.
- Existing successful cache seeding and response-ID reuse still work.

OTel and runtime tests will verify:

- Application resources merge with `resource.Default()` without a SchemaURL
  conflict.
- OTel initialization with a valid configuration creates a span with valid
  trace and span IDs.
- An optional module startup error is written to the process log while the app
  continues in degraded mode.

All Go verification uses Go 1.26, `-tags=custom_skip_vips`,
`BETAGO_CONFIG_PATH=.dev/config.toml`, and the active VS Code `-v` test flag.

## Non-Goals

- Padding prompts solely to exceed 256 tokens.
- Adding a model-specific tokenizer dependency.
- Changing Ark backoff, evaluation scoring, or episode selection semantics.
- Making OTel a critical process dependency.
